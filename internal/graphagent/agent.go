package graphagent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Temerai/twig/internal/parser"
	"github.com/Temerai/twig/internal/tokenizer"
	"github.com/Temerai/twig/internal/types"
)

// GraphAgent queries a codebase graph using heuristic seed extraction and
// configurable traversal strategies to assemble relevant code snippets.
type GraphAgent struct {
	store     *parser.Store
	fileCache map[string][]string
}

// NewGraphAgent creates a GraphAgent backed by the given store.
func NewGraphAgent(store *parser.Store) *GraphAgent {
	return &GraphAgent{
		store: store,
	}
}

// stopWords contains common English words to filter out during seed extraction.
var stopWords = map[string]bool{
	"the": true, "a": true, "is": true, "what": true, "where": true,
	"how": true, "does": true, "which": true, "this": true, "that": true,
	"from": true, "with": true, "in": true, "of": true, "for": true,
	"to": true, "and": true, "or": true, "all": true, "are": true,
	"can": true, "do": true, "my": true, "by": true, "has": true,
	"had": true, "have": true, "not": true, "but": true, "was": true,
	"were": true, "been": true, "will": true, "would": true, "could": true,
	"should": true, "be": true,
}

// Query executes a natural-language query against the codebase graph.
// It extracts seed symbols, traverses the graph according to the requested
// strategy, and assembles source code snippets up to the token budget.
func (ga *GraphAgent) Query(ctx context.Context, req types.QueryRequest) (*types.QueryResult, error) {
	// Step 1: Extract seed symbol names from the natural language query.
	seedNames := ga.extractSeeds(req.NaturalLanguage)

	// Step 2: Look up seed nodes in the graph store.
	var seedNodes []types.Node
	seen := make(map[string]bool)
	for _, name := range seedNames {
		nodes, err := ga.store.GetNodeByName(name)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			if !seen[n.ID] {
				seen[n.ID] = true
				seedNodes = append(seedNodes, n)
			}
		}
	}

	if len(seedNodes) == 0 {
		seedNodes = ga.fileKeywordFallback(req.NaturalLanguage)
	}

	if len(seedNodes) == 0 {
		return &types.QueryResult{
			Snippets:     nil,
			TokensUsed:   0,
			NodesVisited: nil,
		}, nil
	}

	// Step 3: Traverse the graph using the requested strategy.
	budget := req.TokenBudget
	if budget <= 0 {
		budget = 4000
	}

	var visitedIDs []string
	var err error
	switch req.Strategy {
	case types.StrategyScored:
		visitedIDs, err = ga.traverseScored(seedNodes, budget)
	case types.StrategyDeep:
		visitedIDs, err = ga.traverseDeep(seedNodes, budget)
	default:
		// BFS is the default strategy.
		visitedIDs, err = ga.traverseBFS(seedNodes, budget)
	}
	if err != nil {
		return nil, fmt.Errorf("traversing graph: %w", err)
	}

	// Step 4: Assemble snippets from visited nodes.
	snippets, tokensUsed := ga.assembleSnippets(visitedIDs, budget)

	return &types.QueryResult{
		Snippets:     snippets,
		TokensUsed:   tokensUsed,
		NodesVisited: visitedIDs,
	}, nil
}

// splitCamelCase splits a camelCase or PascalCase string into individual words.
// For example, "GetNodeByName" becomes ["Get", "Node", "By", "Name"].
func splitCamelCase(s string) []string {
	var words []string
	var current []rune
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			// Check if this starts a new word: either previous char was lowercase,
			// or next char is lowercase (handles "HTTPServer" -> "HTTP", "Server").
			prev := rune(s[i-1])
			isNewWord := unicode.IsLower(prev)
			if !isNewWord && i+1 < len(s) && unicode.IsLower(rune(s[i+1])) {
				isNewWord = true
			}
			if isNewWord && len(current) > 0 {
				words = append(words, string(current))
				current = nil
			}
		}
		current = append(current, r)
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}

// extractSeeds uses heuristic analysis to extract likely symbol names from a
// natural-language query by splitting on various boundaries, filtering stop
// words, and matching against the store.
func (ga *GraphAgent) extractSeeds(query string) []string {
	var seeds []string
	seen := make(map[string]bool)

	addSeed := func(name string) {
		if !seen[name] {
			seen[name] = true
			seeds = append(seeds, name)
		}
	}

	// Try the full query as-is (exact symbol name).
	trimmed := strings.TrimSpace(query)
	if nodes, err := ga.store.GetNodeByName(trimmed); err == nil && len(nodes) > 0 {
		addSeed(trimmed)
	}

	// Split on spaces, dots, underscores, and other non-identifier characters
	// to get raw tokens.
	rawTokens := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.'
	})

	// Build a list of candidate words by further splitting on dots and
	// camelCase boundaries.
	var candidates []string
	for _, token := range rawTokens {
		// Keep the dotted form as a candidate (e.g., "Store.Close").
		candidates = append(candidates, token)

		// Split on dots.
		dotParts := strings.Split(token, ".")
		for _, part := range dotParts {
			candidates = append(candidates, part)

			// Split on underscores.
			underscoreParts := strings.Split(part, "_")
			for _, upart := range underscoreParts {
				candidates = append(candidates, upart)

				// Split on camelCase boundaries.
				camelParts := splitCamelCase(upart)
				candidates = append(candidates, camelParts...)
			}
		}
	}

	// Try each candidate against the store, filtering stop words and short tokens.
	for _, w := range candidates {
		if len(w) < 2 {
			continue
		}
		if stopWords[strings.ToLower(w)] {
			continue
		}
		if seen[w] {
			continue
		}
		nodes, err := ga.store.GetNodeByName(w)
		if err != nil {
			continue
		}
		if len(nodes) > 0 {
			addSeed(w)
		}
	}

	// Try two-word combinations joined with "." (e.g., "Store Close" -> "Store.Close").
	for i := 0; i < len(rawTokens)-1; i++ {
		a := rawTokens[i]
		b := rawTokens[i+1]
		if stopWords[strings.ToLower(a)] || stopWords[strings.ToLower(b)] {
			continue
		}
		combined := a + "." + b
		if seen[combined] {
			continue
		}
		nodes, err := ga.store.GetNodeByName(combined)
		if err != nil {
			continue
		}
		if len(nodes) > 0 {
			addSeed(combined)
		}
	}

	// Fuzzy fallback: when no exact matches found, try substring matching.
	if len(seeds) == 0 {
		for _, w := range candidates {
			if len(w) < 4 {
				continue
			}
			if stopWords[strings.ToLower(w)] {
				continue
			}
			nodes, err := ga.store.SearchNodesFuzzy(w)
			if err != nil || len(nodes) == 0 {
				continue
			}
			for _, n := range nodes {
				addSeed(n.Name)
			}
		}
	}

	return seeds
}

func (ga *GraphAgent) fileKeywordFallback(query string) []types.Node {
	words := strings.Fields(strings.ToLower(query))
	var result []types.Node
	seen := make(map[string]bool)
	for _, w := range words {
		if len(w) < 4 || stopWords[w] {
			continue
		}
		files, err := ga.store.SearchFilesByKeyword(w)
		if err != nil {
			continue
		}
		for _, f := range files {
			if seen[f] {
				continue
			}
			seen[f] = true
			nodes, err := ga.store.GetNodesByFile(f)
			if err != nil {
				continue
			}
			result = append(result, nodes...)
		}
	}
	return result
}

// traverseBFS performs a breadth-first traversal from the seed nodes, following
// CALLS edges. It stops when the estimated token budget would be exhausted.
func (ga *GraphAgent) traverseBFS(seeds []types.Node, budget int) ([]string, error) {
	visited := make(map[string]bool)
	var result []string
	var queue []string
	tokensUsed := 0

	// Enqueue seed nodes.
	for _, s := range seeds {
		if !visited[s.ID] {
			visited[s.ID] = true
			queue = append(queue, s.ID)
			result = append(result, s.ID)
			tokensUsed += ga.estimateNodeTokens(s.ID)
			if tokensUsed >= budget {
				return result, nil
			}
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		edges, err := ga.store.GetEdges(current, "CALLS")
		if err != nil {
			continue
		}

		for _, e := range edges {
			if visited[e.Dst] {
				continue
			}
			visited[e.Dst] = true

			est := ga.estimateNodeTokens(e.Dst)
			if tokensUsed+est > budget {
				return result, nil
			}
			tokensUsed += est
			result = append(result, e.Dst)
			queue = append(queue, e.Dst)
		}
	}

	return result, nil
}

// scoredNode holds a node ID and its computed relevance score.
type scoredNode struct {
	id    string
	score int
}

// traverseScored collects all neighbor nodes from the seeds, scores each by
// popularity (incoming CALLS edge count) with a bonus for same-file nodes,
// then greedily adds them in descending score order until the budget is
// exhausted.
func (ga *GraphAgent) traverseScored(seeds []types.Node, budget int) ([]string, error) {
	visited := make(map[string]bool)
	var result []string
	tokensUsed := 0

	// Collect file paths of seed nodes for same-file bonus.
	seedFiles := make(map[string]bool)
	for _, s := range seeds {
		seedFiles[s.File] = true
	}

	// Add seed nodes first.
	for _, s := range seeds {
		if !visited[s.ID] {
			visited[s.ID] = true
			result = append(result, s.ID)
			tokensUsed += ga.estimateNodeTokens(s.ID)
			if tokensUsed >= budget {
				return result, nil
			}
		}
	}

	// Collect all unvisited neighbors from seed nodes.
	var candidates []scoredNode
	candidateSet := make(map[string]bool)
	for _, s := range seeds {
		edges, err := ga.store.GetEdges(s.ID, "CALLS")
		if err != nil {
			continue
		}
		for _, e := range edges {
			if visited[e.Dst] || candidateSet[e.Dst] {
				continue
			}
			candidateSet[e.Dst] = true

			score := ga.scoreNode(e.Dst, seedFiles)
			candidates = append(candidates, scoredNode{id: e.Dst, score: score})
		}
	}

	// Sort by score descending.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Greedily add until budget exhausted.
	for _, c := range candidates {
		est := ga.estimateNodeTokens(c.id)
		if tokensUsed+est > budget {
			continue // Try smaller nodes.
		}
		tokensUsed += est
		visited[c.id] = true
		result = append(result, c.id)
	}

	return result, nil
}

// scoreNode computes a relevance score for a node based on its incoming CALLS
// edge count (popularity) and a same-file bonus.
func (ga *GraphAgent) scoreNode(nodeID string, seedFiles map[string]bool) int {
	score := 0

	// Popularity: count incoming CALLS edges.
	inEdges, err := ga.store.GetInEdges(nodeID, "CALLS")
	if err == nil {
		score += len(inEdges)
	}

	// Same-file bonus.
	node, err := ga.store.GetNode(nodeID)
	if err == nil && node != nil && seedFiles[node.File] {
		score += 3
	}

	return score
}

// traverseDeep performs a depth-first traversal from the seed nodes, following
// each call chain as deep as possible before backtracking.
func (ga *GraphAgent) traverseDeep(seeds []types.Node, budget int) ([]string, error) {
	visited := make(map[string]bool)
	var result []string
	tokensUsed := 0

	for _, s := range seeds {
		if visited[s.ID] {
			continue
		}
		ga.dfs(s.ID, visited, &result, &tokensUsed, budget)
		if tokensUsed >= budget {
			break
		}
	}

	return result, nil
}

// dfs is the recursive helper for depth-first traversal.
func (ga *GraphAgent) dfs(nodeID string, visited map[string]bool, result *[]string, tokensUsed *int, budget int) {
	if visited[nodeID] || *tokensUsed >= budget {
		return
	}

	est := ga.estimateNodeTokens(nodeID)
	if *tokensUsed+est > budget {
		return
	}

	visited[nodeID] = true
	*result = append(*result, nodeID)
	*tokensUsed += est

	edges, err := ga.store.GetEdges(nodeID, "CALLS")
	if err != nil {
		return
	}

	for _, e := range edges {
		if *tokensUsed >= budget {
			return
		}
		ga.dfs(e.Dst, visited, result, tokensUsed, budget)
	}
}

// estimateNodeTokens estimates how many tokens a node's source text will
// consume. When possible it reads the actual source and runs the tokenizer
// heuristic; otherwise it falls back to a line-count-based estimate.
func (ga *GraphAgent) estimateNodeTokens(nodeID string) int {
	node, err := ga.store.GetNode(nodeID)
	if err != nil || node == nil {
		return 50
	}

	startLine, endLine, err := parseLineRange(node.Lines)
	if err != nil {
		return 50
	}

	src, ok := ga.readNodeSource(node.File, startLine, endLine)
	if ok {
		count := tokenizer.EstimateTokens(src)
		if count == 0 {
			return 1
		}
		return count
	}

	lineCount := endLine - startLine + 1
	return lineCount * 7
}

// readNodeSource extracts source text for a line range from a file, using
// the file cache to avoid redundant reads.
func (ga *GraphAgent) readNodeSource(file string, startLine, endLine int) (string, bool) {
	if ga.fileCache == nil {
		ga.fileCache = make(map[string][]string)
	}

	lines, ok := ga.fileCache[file]
	if !ok {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", false
		}
		lines = strings.Split(string(data), "\n")
		ga.fileCache[file] = lines
	}

	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) {
		return "", false
	}

	return strings.Join(lines[startLine-1:endLine], "\n"), true
}

// assembleSnippets reads source files and extracts code for each visited node,
// accumulating snippets until the token budget is reached.
func (ga *GraphAgent) assembleSnippets(nodeIDs []string, budget int) ([]types.CodeSnippet, int) {
	var snippets []types.CodeSnippet
	tokensUsed := 0

	for _, id := range nodeIDs {
		node, err := ga.store.GetNode(id)
		if err != nil || node == nil {
			continue
		}

		startLine, endLine, err := parseLineRange(node.Lines)
		if err != nil {
			continue
		}

		sourceText, ok := ga.readNodeSource(node.File, startLine, endLine)
		if !ok {
			continue
		}

		tokenCount := tokenizer.EstimateTokens(sourceText)
		if tokenCount == 0 {
			tokenCount = 1
		}

		if tokensUsed+tokenCount > budget {
			remaining := budget - tokensUsed
			if remaining > 0 {
				maxChars := remaining * tokenizer.CharsPerToken
				if maxChars < len(sourceText) {
					sourceText = sourceText[:maxChars]
					tokenCount = remaining
				}
			} else {
				break
			}
		}

		tokensUsed += tokenCount
		snippets = append(snippets, types.CodeSnippet{
			NodeName:   node.Name,
			FilePath:   node.File,
			LineRange:  node.Lines,
			SourceText: sourceText,
			TokenCount: tokenCount,
		})

		if tokensUsed >= budget {
			break
		}
	}

	return snippets, tokensUsed
}

// parseLineRange parses a "startLine-endLine" string into integers.
func parseLineRange(lineRange string) (int, int, error) {
	parts := strings.SplitN(lineRange, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid line range format: %q", lineRange)
	}

	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("parsing start line: %w", err)
	}

	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("parsing end line: %w", err)
	}

	return start, end, nil
}
