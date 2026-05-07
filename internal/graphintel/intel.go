package graphintel

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Temerai/twig/internal/parser"
	"github.com/Temerai/twig/internal/types"
)

// GraphIntel provides human-queryable code intelligence through pure graph
// traversal. All methods use heuristic pattern matching -- no LLM calls.
type GraphIntel struct {
	store *parser.Store
}

// NewGraphIntel creates a GraphIntel backed by the given store.
func NewGraphIntel(store *parser.Store) *GraphIntel {
	return &GraphIntel{
		store: store,
	}
}

// resolveSymbol looks up a symbol name and returns all matching nodes.
// Falls back to suffix matching and fuzzy matching when exact lookup fails.
func (gi *GraphIntel) resolveSymbol(symbol string) ([]types.Node, error) {
	nodes, err := gi.store.GetNodeByName(symbol)
	if err != nil {
		return nil, fmt.Errorf("resolving symbol %q: %w", symbol, err)
	}
	if len(nodes) > 0 {
		return nodes, nil
	}

	// Try suffix match (e.g., "Index" → "Indexer.Index").
	nodes, err = gi.store.SearchNodesBySuffix(symbol)
	if err == nil && len(nodes) > 0 {
		return nodes, nil
	}

	// Try fuzzy/substring match (e.g., "indexing" → "Index").
	nodes, err = gi.store.SearchNodesFuzzy(symbol)
	if err == nil && len(nodes) > 0 {
		return nodes, nil
	}

	return nil, fmt.Errorf("symbol %q not found", symbol)
}

// Callers finds all callers of the given symbol up to depth levels using BFS
// traversal following incoming CALLS edges.
func (gi *GraphIntel) Callers(ctx context.Context, symbol string, depth int) ([]types.Node, error) {
	seeds, err := gi.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []types.Node

	// Initialize the BFS frontier with seed node IDs.
	frontier := make([]string, 0, len(seeds))
	for _, n := range seeds {
		seen[n.ID] = true
		frontier = append(frontier, n.ID)
	}

	for level := 0; level < depth && len(frontier) > 0; level++ {
		var nextFrontier []string
		for _, nodeID := range frontier {
			edges, err := gi.store.GetInEdges(nodeID, "CALLS")
			if err != nil {
				return nil, fmt.Errorf("getting incoming CALLS edges for %s: %w", nodeID, err)
			}
			for _, e := range edges {
				if seen[e.Src] {
					continue
				}
				seen[e.Src] = true
				node, err := gi.store.GetNode(e.Src)
				if err != nil {
					return nil, fmt.Errorf("getting node %s: %w", e.Src, err)
				}
				if node != nil {
					result = append(result, *node)
					nextFrontier = append(nextFrontier, node.ID)
				}
			}
		}
		frontier = nextFrontier
	}

	return result, nil
}

// Callees finds all functions called by the given symbol up to depth levels
// using BFS traversal following outgoing CALLS edges.
func (gi *GraphIntel) Callees(ctx context.Context, symbol string, depth int) ([]types.Node, error) {
	seeds, err := gi.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []types.Node

	frontier := make([]string, 0, len(seeds))
	for _, n := range seeds {
		seen[n.ID] = true
		frontier = append(frontier, n.ID)
	}

	for level := 0; level < depth && len(frontier) > 0; level++ {
		var nextFrontier []string
		for _, nodeID := range frontier {
			edges, err := gi.store.GetEdges(nodeID, "CALLS")
			if err != nil {
				return nil, fmt.Errorf("getting outgoing CALLS edges for %s: %w", nodeID, err)
			}
			for _, e := range edges {
				if seen[e.Dst] {
					continue
				}
				seen[e.Dst] = true
				node, err := gi.store.GetNode(e.Dst)
				if err != nil {
					return nil, fmt.Errorf("getting node %s: %w", e.Dst, err)
				}
				if node != nil {
					result = append(result, *node)
					nextFrontier = append(nextFrontier, node.ID)
				}
			}
		}
		frontier = nextFrontier
	}

	return result, nil
}

// isTypeKind returns true if the node kind represents a type definition.
func isTypeKind(kind string) bool {
	return kind == "struct" || kind == "interface" || kind == "class" || kind == "type"
}

// Dependencies finds what the given symbol depends on by following all outgoing
// CALLS and IMPORTS edges. For type nodes (struct, interface, class, type), it
// also includes dependencies of the type's methods.
func (gi *GraphIntel) Dependencies(ctx context.Context, symbol string) ([]types.Node, error) {
	seeds, err := gi.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}

	// Expand type nodes: collect their methods so we can query edges from those too.
	var queryNodes []types.Node
	queryNodes = append(queryNodes, seeds...)
	for _, seed := range seeds {
		if isTypeKind(seed.Kind) {
			methods, err := gi.store.GetMethodsOfType(seed.Name)
			if err != nil {
				return nil, fmt.Errorf("getting methods of type %s: %w", seed.Name, err)
			}
			queryNodes = append(queryNodes, methods...)
		}
	}

	seen := make(map[string]bool)
	var result []types.Node

	// Mark all seed + method nodes as seen so they are not included in results.
	for _, n := range queryNodes {
		seen[n.ID] = true
	}

	for _, node := range queryNodes {
		edges, err := gi.store.GetEdges(node.ID, "")
		if err != nil {
			return nil, fmt.Errorf("getting edges for %s: %w", node.ID, err)
		}
		for _, e := range edges {
			if e.Kind != "CALLS" && e.Kind != "IMPORTS" {
				continue
			}
			if seen[e.Dst] {
				continue
			}
			seen[e.Dst] = true
			target, err := gi.store.GetNode(e.Dst)
			if err != nil {
				return nil, fmt.Errorf("getting node %s: %w", e.Dst, err)
			}
			if target != nil {
				result = append(result, *target)
			}
		}
	}

	return result, nil
}

// ImpactOf assesses the impact of changing the given symbol. It returns an
// ImpactReport with direct callers, transitive dependents, affected files, and
// a risk score based on the count of direct incoming CALLS edges.
func (gi *GraphIntel) ImpactOf(ctx context.Context, symbol string) (*types.ImpactReport, error) {
	// Resolve the symbol first to compute the risk score.
	seeds, err := gi.resolveSymbol(symbol)
	if err != nil {
		return nil, err
	}

	// Direct callers (depth=1).
	directCallers, err := gi.Callers(ctx, symbol, 1)
	if err != nil {
		return nil, fmt.Errorf("getting direct callers: %w", err)
	}

	// Transitive callers (depth=5).
	transitiveDeps, err := gi.Callers(ctx, symbol, 5)
	if err != nil {
		return nil, fmt.Errorf("getting transitive callers: %w", err)
	}

	// Collect unique affected files from all transitive nodes.
	fileSet := make(map[string]bool)
	for _, n := range transitiveDeps {
		if n.File != "" {
			fileSet[n.File] = true
		}
	}
	affectedFiles := make([]string, 0, len(fileSet))
	for f := range fileSet {
		affectedFiles = append(affectedFiles, f)
	}

	// Compute RiskScore as the total count of direct call sites (incoming CALLS
	// edges to any of the resolved seed nodes).
	riskScore := 0
	for _, seed := range seeds {
		edges, err := gi.store.GetInEdges(seed.ID, "CALLS")
		if err != nil {
			return nil, fmt.Errorf("counting call sites for %s: %w", seed.ID, err)
		}
		riskScore += len(edges)
	}

	return &types.ImpactReport{
		DirectCallers:  directCallers,
		TransitiveDeps: transitiveDeps,
		AffectedFiles:  affectedFiles,
		RiskScore:      riskScore,
	}, nil
}

// stopWords contains common English words to filter out during symbol extraction.
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

// extractSymbol attempts to find a symbol name in a natural language question.
// It looks for CamelCase words, dotted identifiers (e.g., "Store.Close"), and
// words starting with uppercase, then tries each candidate against the store.
func (gi *GraphIntel) extractSymbol(question string) string {
	// Tokenize the question on non-identifier characters (keeping dots for
	// qualified names like "Store.Close").
	tokens := strings.FieldsFunc(question, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.'
	})

	// Score candidates: prefer dotted names and CamelCase over plain words.
	type candidate struct {
		name  string
		score int
	}
	var candidates []candidate

	for _, tok := range tokens {
		if len(tok) < 2 {
			continue
		}
		if stopWords[strings.ToLower(tok)] {
			continue
		}

		score := 0
		// Dotted names get highest priority.
		if strings.Contains(tok, ".") {
			score = 3
		} else if unicode.IsUpper(rune(tok[0])) {
			// Starts with uppercase.
			score = 2
			// Bonus for camelCase (has mixed case).
			hasLower := false
			hasInnerUpper := false
			for i, r := range tok {
				if i > 0 && unicode.IsUpper(r) {
					hasInnerUpper = true
				}
				if unicode.IsLower(r) {
					hasLower = true
				}
			}
			if hasLower && hasInnerUpper {
				score = 3
			}
		} else {
			score = 1
		}

		candidates = append(candidates, candidate{name: tok, score: score})
	}

	// Sort by score descending so we try the most likely symbols first.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// Try each candidate against the store and return the first match.
	for _, c := range candidates {
		nodes, err := gi.store.GetNodeByName(c.name)
		if err == nil && len(nodes) > 0 {
			return c.name
		}
	}

	// Try fuzzy matching when no exact match is found.
	for _, c := range candidates {
		nodes, err := gi.store.SearchNodesFuzzy(c.name)
		if err == nil && len(nodes) > 0 {
			return nodes[0].Name
		}
	}

	// No match found in store; return the highest-scored candidate as a
	// best-effort guess.
	if len(candidates) > 0 {
		return candidates[0].name
	}
	return ""
}

// classifyQuestion determines the query type from a natural language question
// using keyword/pattern matching. Returns the query type and the extracted
// symbol name.
func (gi *GraphIntel) classifyQuestion(question string) (queryType string, symbol string) {
	lower := strings.ToLower(question)
	symbol = gi.extractSymbol(question)

	// Rule 1: impact/break/change/affect -> impact analysis.
	if strings.Contains(lower, "impact") || strings.Contains(lower, "break") ||
		strings.Contains(lower, "change") || strings.Contains(lower, "affect") {
		return "impact", symbol
	}

	// Rule 2: "who/what calls" -> callers.
	if strings.Contains(lower, "call") {
		callIdx := strings.Index(lower, "call")
		// Check if "who" or "what" appears before "call".
		prefix := lower[:callIdx]
		if strings.Contains(prefix, "who") || strings.Contains(prefix, "what") {
			return "callers", symbol
		}
		// Rule 3: "calls <symbol>" -> callees.
		return "callees", symbol
	}

	// Rule 4: depend/import/use/need -> deps.
	if strings.Contains(lower, "depend") || strings.Contains(lower, "import") ||
		strings.Contains(lower, "use") || strings.Contains(lower, "need") {
		return "deps", symbol
	}

	// Default: callers (most common query).
	return "callers", symbol
}

// NaturalQuery classifies a natural language question using pattern matching
// and dispatches to the appropriate graph traversal method.
func (gi *GraphIntel) NaturalQuery(ctx context.Context, question string) (*types.GraphAnswer, error) {
	queryType, symbol := gi.classifyQuestion(question)

	if symbol == "" {
		return nil, fmt.Errorf("no symbol identified in question")
	}

	depth := 3

	var nodes []types.Node
	var edges []types.Edge
	var summary string
	var err error

	switch queryType {
	case "callers":
		nodes, err = gi.Callers(ctx, symbol, depth)
		if err != nil {
			return nil, err
		}
		summary = formatCallersSummary(symbol, nodes)

	case "callees":
		nodes, err = gi.Callees(ctx, symbol, depth)
		if err != nil {
			return nil, err
		}
		summary = formatCalleesSummary(symbol, nodes)

	case "deps":
		nodes, err = gi.Dependencies(ctx, symbol)
		if err != nil {
			return nil, err
		}
		summary = formatDepsSummary(symbol, nodes)

	case "impact":
		report, err := gi.ImpactOf(ctx, symbol)
		if err != nil {
			return nil, err
		}
		nodes = report.TransitiveDeps
		summary = formatImpactSummary(symbol, report)

	default:
		// Fallback: treat as callers query.
		nodes, err = gi.Callers(ctx, symbol, depth)
		if err != nil {
			return nil, err
		}
		summary = formatCallersSummary(symbol, nodes)
	}

	// Collect edges related to the result nodes for context.
	nodeIDSet := make(map[string]bool)
	for _, n := range nodes {
		nodeIDSet[n.ID] = true
	}
	for _, n := range nodes {
		outEdges, err := gi.store.GetEdges(n.ID, "")
		if err == nil {
			for _, e := range outEdges {
				if nodeIDSet[e.Dst] {
					edges = append(edges, e)
				}
			}
		}
	}

	return &types.GraphAnswer{
		Summary: summary,
		Nodes:   nodes,
		Edges:   edges,
	}, nil
}

func formatCallersSummary(symbol string, nodes []types.Node) string {
	if len(nodes) == 0 {
		return fmt.Sprintf("No callers found for %s.", symbol)
	}
	names := nodeNames(nodes)
	return fmt.Sprintf("Found %d caller(s) of %s: %s", len(nodes), symbol, strings.Join(names, ", "))
}

func formatCalleesSummary(symbol string, nodes []types.Node) string {
	if len(nodes) == 0 {
		return fmt.Sprintf("%s does not call any other functions.", symbol)
	}
	names := nodeNames(nodes)
	return fmt.Sprintf("%s calls %d function(s): %s", symbol, len(nodes), strings.Join(names, ", "))
}

func formatDepsSummary(symbol string, nodes []types.Node) string {
	if len(nodes) == 0 {
		return fmt.Sprintf("%s has no dependencies.", symbol)
	}
	names := nodeNames(nodes)
	return fmt.Sprintf("%s depends on %d symbol(s): %s", symbol, len(nodes), strings.Join(names, ", "))
}

func formatImpactSummary(symbol string, report *types.ImpactReport) string {
	return fmt.Sprintf(
		"Changing %s impacts %d direct caller(s), %d transitive dependent(s) across %d file(s). Risk score: %d.",
		symbol,
		len(report.DirectCallers),
		len(report.TransitiveDeps),
		len(report.AffectedFiles),
		report.RiskScore,
	)
}

func nodeNames(nodes []types.Node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}
