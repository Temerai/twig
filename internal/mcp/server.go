package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Temerai/twig/internal/graphagent"
	"github.com/Temerai/twig/internal/graphintel"
	"github.com/Temerai/twig/internal/parser"
	"github.com/Temerai/twig/internal/types"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 message types
// ---------------------------------------------------------------------------

// JSONRPCRequest represents an incoming JSON-RPC 2.0 request or notification.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError carries error information in a JSON-RPC 2.0 response.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// ---------------------------------------------------------------------------
// MCP content types
// ---------------------------------------------------------------------------

// textContent is a single text content block returned inside a tool result.
type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolResult wraps one or more content blocks as required by the MCP protocol.
type toolResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func makeTextResult(text string) toolResult {
	return toolResult{
		Content: []textContent{{Type: "text", Text: text}},
	}
}

func makeErrorResult(text string) toolResult {
	return toolResult{
		Content: []textContent{{Type: "text", Text: text}},
		IsError: true,
	}
}

// ---------------------------------------------------------------------------
// MCP tool schema types (for tools/list)
// ---------------------------------------------------------------------------

type toolInfo struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema toolSchema `json:"inputSchema"`
}

type toolSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type schemaProperty struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Default     any             `json:"default,omitempty"`
	Enum        []string        `json:"enum,omitempty"`
	Items       *schemaProperty `json:"items,omitempty"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server implements a minimal MCP server over stdio using JSON-RPC 2.0.
type Server struct {
	rootPath string
	store    *parser.Store
	indexer  *parser.Indexer
	agent    *graphagent.GraphAgent
	intel    *graphintel.GraphIntel
}

// NewServer creates a new MCP server. Components are initialised lazily on the
// first index_codebase call (or on the first read-tool call if a DB already
// exists at the derived path).
func NewServer(rootPath string) *Server {
	return &Server{rootPath: rootPath}
}

// Close releases any open database connection.
func (s *Server) Close() {
	if s.store != nil {
		s.store.Close()
		s.store = nil
	}
}

// ensureStore opens (or creates) the store for root, replacing the existing one
// if the root has changed.
func (s *Server) ensureStore(root string) error {
	if s.store != nil && s.rootPath == root {
		return nil
	}
	if s.store != nil {
		s.store.Close()
		s.store = nil
		s.indexer = nil
		s.agent = nil
		s.intel = nil
	}
	s.rootPath = root
	store, err := parser.NewStore(parser.DBPathForRoot(root))
	if err != nil {
		return err
	}
	s.store = store
	s.indexer = parser.NewIndexer(store, root)
	s.agent = graphagent.NewGraphAgent(store)
	s.intel = graphintel.NewGraphIntel(store)
	return nil
}

// openIfExists opens an existing DB for the configured root without creating one.
// Returns false if no DB exists yet, indicating the codebase has not been indexed.
func (s *Server) openIfExists() bool {
	if s.store != nil {
		return true
	}
	dbPath := parser.DBPathForRoot(s.rootPath)
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	return s.ensureStore(s.rootPath) == nil
}

// Serve runs the main read-dispatch-write loop on stdin/stdout. It blocks
// until ctx is cancelled or stdin is closed. Errors are logged to stderr;
// stdout is reserved exclusively for JSON-RPC protocol traffic.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	// Allow up to 10 MB per line to handle large tool results.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}
			return nil // EOF
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &JSONRPCError{Code: codeParseError, Message: "parse error: " + err.Error()},
			})
			continue
		}

		// Notifications have no id field — process but do not respond.
		if req.ID == nil || len(req.ID) == 0 {
			s.handleNotification(&req)
			continue
		}

		resp := s.dispatch(ctx, &req)
		s.writeResponse(resp)
	}
}

// writeResponse marshals a JSONRPCResponse and writes it to stdout followed
// by a newline.
func (s *Server) writeResponse(resp JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: failed to marshal response: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stdout, "%s\n", data)
}

// handleNotification processes notifications (requests without an id).
func (s *Server) handleNotification(req *JSONRPCRequest) {
	switch req.Method {
	case "notifications/initialized":
		// No-op acknowledgement.
	default:
		fmt.Fprintf(os.Stderr, "mcp: unknown notification %q\n", req.Method)
	}
}

// dispatch routes a request to the appropriate handler and returns a response.
func (s *Server) dispatch(ctx context.Context, req *JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return s.handlePing(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: codeMethodNotFound, Message: "method not found: " + req.Method},
		}
	}
}

// ---------------------------------------------------------------------------
// initialize
// ---------------------------------------------------------------------------

type initializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	Capabilities    serverCapability `json:"capabilities"`
	ServerInfo      serverInfo       `json:"serverInfo"`
}

type serverCapability struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *Server) handleInitialize(req *JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities:    serverCapability{Tools: toolsCapability{ListChanged: false}},
			ServerInfo:      serverInfo{Name: "twig", Version: "0.1.0"},
		},
	}
}

// ---------------------------------------------------------------------------
// ping
// ---------------------------------------------------------------------------

func (s *Server) handlePing(req *JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{},
	}
}

// ---------------------------------------------------------------------------
// tools/list
// ---------------------------------------------------------------------------

func (s *Server) handleToolsList(req *JSONRPCRequest) JSONRPCResponse {
	tools := []toolInfo{
		{
			Name: "query_codebase",
			Description: `Explore the codebase by question when you don't know the exact symbol name. Extracts seeds from the question, traverses the graph, and returns ranked code snippets within the token budget.

USE THIS when: understanding how a feature works, tracing data flow, answering "how does X happen", exploring unfamiliar areas, or when get_symbol/search_codebase return nothing useful.

Strategy guide (default "scored" is right most of the time):
- "scored"  — best general-purpose; ranks nodes by relevance score (default)
- "bfs"     — breadth-first; good for shallow exploration of nearby symbols
- "deep"    — depth-first; best for tracing an end-to-end call chain
- "callers" — seeds on callers first; use when you want to understand how something is used

Prefer get_symbol if you already know the exact name. Prefer search_codebase for keyword/string literal matches.`,
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"question": {
						Type:        "string",
						Description: "Natural language question about the codebase, e.g. 'how does authentication work' or 'where is the cache evicted'",
					},
					"token_budget": {
						Type:        "integer",
						Description: "Maximum tokens of code context to return (default 4000; increase to 8000+ for complex flows)",
						Default:     4000,
					},
					"strategy": {
						Type:        "string",
						Description: "Graph traversal strategy: scored (default), bfs, deep, or callers",
						Default:     "scored",
						Enum:        []string{"bfs", "scored", "deep", "callers"},
					},
				},
				Required: []string{"question"},
			},
		},
		{
			Name: "analyze_impact",
			Description: `Measure the blast radius of modifying or deleting a symbol. Returns direct callers, direct users (non-call references), transitive dependents, all affected files, and a risk score.

USE THIS before: renaming, refactoring, changing a signature, or deleting any function, method, or type. The risk score and affected-file list tell you how safe the change is and what tests to run.

Prefer graph_explore when you only want a lightweight list of callers or callees without the full impact report.`,
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"symbol": {
						Type:        "string",
						Description: "Exact name of the function, method, or type to analyze",
					},
					"depth": {
						Type:        "integer",
						Description: "Maximum depth for transitive dependent traversal (default 3; increase for deep dependency chains)",
						Default:     3,
					},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name: "graph_explore",
			Description: `List callers, callees, or type dependencies of a symbol up to a given depth. Returns names, kinds, file paths, and line ranges — no risk scoring.

USE THIS when: browsing who calls a function (callers), what a function calls (callees), or what types a symbol depends on (deps). Lighter than analyze_impact when you just need the graph neighbourhood without a full blast-radius report.

Modes:
- "callers" — symbols that call this symbol
- "callees" — symbols this symbol calls
- "deps"    — types/interfaces this symbol depends on (USES edges)`,
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"mode": {
						Type:        "string",
						Description: "callers: who calls this symbol | callees: what this symbol calls | deps: type dependencies (USES edges)",
						Enum:        []string{"callers", "callees", "deps"},
					},
					"symbol": {
						Type:        "string",
						Description: "Exact name of the function, method, or type to explore",
					},
					"depth": {
						Type:        "integer",
						Description: "Maximum traversal depth (default 3)",
						Default:     3,
					},
				},
				Required: []string{"mode", "symbol"},
			},
		},
		{
			Name: "graph_stats",
			Description: `Return a full breakdown of the indexed codebase graph: total node count, edge count, file count, nodes grouped by kind (function/method/struct/…), edges grouped by kind (CALLS/USES/…), and node counts per language.

USE THIS to: verify the index is populated before querying, check which languages were indexed, or diagnose an unexpectedly sparse graph. Run index_codebase first if counts are zero.`,
			InputSchema: toolSchema{
				Type:       "object",
				Properties: map[string]schemaProperty{},
			},
		},
		{
			Name: "get_symbol",
			Description: `Fetch source code and metadata for a symbol by exact name. Returns file path, line range, signature, and full source for every match.

USE THIS first whenever you already know the symbol name — it is the fastest and most precise lookup. Falls back to kind filter if multiple symbols share the same name.

If the name is uncertain or partially known, use search_codebase instead. If you need semantic context around the symbol, follow up with query_codebase.`,
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"name": {Type: "string", Description: "Exact symbol name (function, method, struct, class, interface, or type)"},
					"kind": {Type: "string", Description: "Optional: narrow results to one kind when multiple symbols share the same name", Enum: []string{"function", "method", "struct", "interface", "class", "type"}},
				},
				Required: []string{"name"},
			},
		},
		{
			Name: "search_codebase",
			Description: `Full-text search (FTS5) over indexed symbol names and source code. Returns matching symbols with source snippets.

USE THIS when: you know a keyword, error string, constant, partial name, or code pattern but not the exact symbol name. Faster than query_codebase for keyword lookups.

FTS5 syntax tips: use OR for alternatives (e.g. "parse OR lex"), wrap phrases in quotes (e.g. '"token budget"'), prefix-match with * (e.g. "extract*").

If exact name is known, prefer get_symbol. If you need semantic/conceptual results, use query_codebase.`,
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"query": {Type: "string", Description: "FTS5 search query: keywords, OR alternatives, \"quoted phrases\", or prefix* matches"},
					"limit": {Type: "integer", Description: "Maximum results to return (default 20)", Default: 20},
				},
				Required: []string{"query"},
			},
		},
		{
			Name: "index_codebase",
			Description: `Build or refresh the codebase graph index. Returns total node and edge counts when done.

USE THIS: at the start of a session if graph_stats shows zero nodes, or after editing source files to keep the graph current.

For full reindex omit changed_files (or provide a path). For incremental updates after editing a few files, pass changed_files — this is much faster than a full reindex and avoids stale graph data.`,
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"path": {
						Type:        "string",
						Description: "Root directory to index (defaults to the configured codebase root); ignored when changed_files is provided",
					},
					"changed_files": {
						Type:        "array",
						Description: "Incremental mode: list of edited file paths to reindex. Much faster than a full reindex — use after saving edits.",
						Items:       &schemaProperty{Type: "string"},
					},
				},
			},
		},
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{"tools": tools},
	}
}

// ---------------------------------------------------------------------------
// tools/call
// ---------------------------------------------------------------------------

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, req *JSONRPCRequest) JSONRPCResponse {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: codeInvalidParams, Message: "invalid tools/call params: " + err.Error()},
		}
	}

	var result toolResult
	switch params.Name {
	case "query_codebase":
		result = s.callQueryCodebase(ctx, params.Arguments)
	case "analyze_impact":
		result = s.callAnalyzeImpact(ctx, params.Arguments)
	case "graph_explore":
		result = s.callGraphExplore(ctx, params.Arguments)
	case "graph_stats":
		result = s.callGraphStats()
	case "get_symbol":
		result = s.callGetSymbol(ctx, params.Arguments)
	case "search_codebase":
		result = s.callSearchCodebase(ctx, params.Arguments)
	case "index_codebase":
		result = s.callIndexCodebase(params.Arguments)
	default:
		result = makeErrorResult(fmt.Sprintf("unknown tool: %s", params.Name))
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

// ---------------------------------------------------------------------------
// Tool implementations
// ---------------------------------------------------------------------------

// --- query_codebase ---

type queryCodebaseArgs struct {
	Question    string `json:"question"`
	TokenBudget int    `json:"token_budget"`
	Strategy    string `json:"strategy"`
}

func (s *Server) callQueryCodebase(ctx context.Context, raw json.RawMessage) toolResult {
	if !s.openIfExists() {
		return makeErrorResult("codebase not indexed — call index_codebase first")
	}
	var args queryCodebaseArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return makeErrorResult("invalid arguments: " + err.Error())
	}
	if args.Question == "" {
		return makeErrorResult("missing required parameter: question")
	}
	if args.TokenBudget <= 0 {
		args.TokenBudget = 4000
	}

	strategy := types.StrategyScored
	switch args.Strategy {
	case "bfs":
		strategy = types.StrategyBFS
	case "deep":
		strategy = types.StrategyDeep
	case "callers":
		strategy = types.StrategyCallers
	case "scored", "":
		strategy = types.StrategyScored
	default:
		return makeErrorResult(fmt.Sprintf("unknown strategy %q; use bfs, scored, deep, or callers", args.Strategy))
	}

	qr, err := s.agent.Query(ctx, types.QueryRequest{
		NaturalLanguage: args.Question,
		TokenBudget:     args.TokenBudget,
		Strategy:        strategy,
	})
	if err != nil {
		return makeErrorResult("query failed: " + err.Error())
	}

	return makeTextResult(formatQueryResult(qr))
}

func formatQueryResult(qr *types.QueryResult) string {
	if qr == nil || len(qr.Snippets) == 0 {
		return "No relevant code snippets found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d snippet(s), %d tokens used, %d nodes visited.\n\n",
		len(qr.Snippets), qr.TokensUsed, len(qr.NodesVisited)))

	for i, sn := range qr.Snippets {
		sb.WriteString(fmt.Sprintf("--- %d. %s (%s lines %s, %d tokens) ---\n",
			i+1, sn.NodeName, sn.FilePath, sn.LineRange, sn.TokenCount))
		sb.WriteString(sn.SourceText)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// --- analyze_impact ---

type analyzeImpactArgs struct {
	Symbol string `json:"symbol"`
	Depth  int    `json:"depth"`
}

func (s *Server) callAnalyzeImpact(ctx context.Context, raw json.RawMessage) toolResult {
	if !s.openIfExists() {
		return makeErrorResult("codebase not indexed — call index_codebase first")
	}
	var args analyzeImpactArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return makeErrorResult("invalid arguments: " + err.Error())
	}
	if args.Symbol == "" {
		return makeErrorResult("missing required parameter: symbol")
	}
	if args.Depth <= 0 {
		args.Depth = 3
	}

	report, err := s.intel.ImpactOf(ctx, args.Symbol, args.Depth)
	if err != nil {
		return makeErrorResult("impact analysis failed: " + err.Error())
	}

	return makeTextResult(formatImpactReport(args.Symbol, report))
}

func formatImpactReport(symbol string, r *types.ImpactReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Impact analysis for %q:\n", symbol))
	sb.WriteString(fmt.Sprintf("  Risk score:       %d\n", r.RiskScore))
	sb.WriteString(fmt.Sprintf("  Direct callers:   %d\n", len(r.DirectCallers)))
	for _, n := range r.DirectCallers {
		sb.WriteString(fmt.Sprintf("    - %s (%s, %s)\n", n.Name, n.Kind, n.File))
	}
	sb.WriteString(fmt.Sprintf("  Direct users:     %d\n", len(r.DirectUsers)))
	for _, n := range r.DirectUsers {
		sb.WriteString(fmt.Sprintf("    - %s (%s, %s)\n", n.Name, n.Kind, n.File))
	}
	sb.WriteString(fmt.Sprintf("  Transitive dependents: %d\n", len(r.TransitiveDeps)))
	for _, n := range r.TransitiveDeps {
		sb.WriteString(fmt.Sprintf("    - %s (%s, %s)\n", n.Name, n.Kind, n.File))
	}
	sb.WriteString(fmt.Sprintf("  Affected files:   %d\n", len(r.AffectedFiles)))
	for _, f := range r.AffectedFiles {
		sb.WriteString(fmt.Sprintf("    - %s\n", f))
	}
	return sb.String()
}

// --- graph_explore ---

type graphExploreArgs struct {
	Mode   string `json:"mode"`
	Symbol string `json:"symbol"`
	Depth  int    `json:"depth"`
}

func (s *Server) callGraphExplore(ctx context.Context, raw json.RawMessage) toolResult {
	if !s.openIfExists() {
		return makeErrorResult("codebase not indexed — call index_codebase first")
	}
	var args graphExploreArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return makeErrorResult("invalid arguments: " + err.Error())
	}
	if args.Symbol == "" {
		return makeErrorResult("missing required parameter: symbol")
	}
	if args.Mode == "" {
		return makeErrorResult("missing required parameter: mode")
	}
	if args.Depth <= 0 {
		args.Depth = 3
	}

	var nodes []types.Node
	var err error
	var label string

	switch args.Mode {
	case "callers":
		nodes, err = s.intel.Callers(ctx, args.Symbol, args.Depth)
		label = "Callers"
	case "callees":
		nodes, err = s.intel.Callees(ctx, args.Symbol, args.Depth)
		label = "Callees"
	case "deps":
		nodes, err = s.intel.Dependencies(ctx, args.Symbol)
		label = "Dependencies"
	default:
		return makeErrorResult(fmt.Sprintf("unknown mode %q; use callers, callees, or deps", args.Mode))
	}

	if err != nil {
		return makeErrorResult(fmt.Sprintf("%s lookup failed: %s", label, err.Error()))
	}

	return makeTextResult(formatNodeList(label, args.Symbol, nodes))
}

func formatNodeList(label, symbol string, nodes []types.Node) string {
	if len(nodes) == 0 {
		return fmt.Sprintf("%s of %q: none found.", label, symbol)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s of %q (%d):\n", label, symbol, len(nodes)))
	for _, n := range nodes {
		sb.WriteString(fmt.Sprintf("  - %s [%s] %s lines %s\n", n.Name, n.Kind, n.File, n.Lines))
	}
	return sb.String()
}

// --- graph_stats ---

func (s *Server) callGraphStats() toolResult {
	if !s.openIfExists() {
		return makeErrorResult("codebase not indexed — call index_codebase first")
	}
	stats, err := s.store.DetailedStats()
	if err != nil {
		return makeErrorResult(fmt.Sprintf("stats failed: %v", err))
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("marshal stats: %v", err))
	}
	return makeTextResult(string(data))
}

// --- get_symbol ---

func (s *Server) callGetSymbol(ctx context.Context, raw json.RawMessage) toolResult {
	if !s.openIfExists() {
		return makeErrorResult("codebase not indexed — call index_codebase first")
	}
	var args struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return makeErrorResult(fmt.Sprintf("invalid args: %v", err))
	}
	if args.Name == "" {
		return makeErrorResult("name is required")
	}

	nodes, err := s.store.GetNodeByName(args.Name)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("lookup failed: %v", err))
	}

	if args.Kind != "" {
		var filtered []types.Node
		for _, n := range nodes {
			if n.Kind == args.Kind {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	if len(nodes) == 0 {
		return makeTextResult(fmt.Sprintf("symbol %q not found", args.Name))
	}

	var sb strings.Builder
	for i, n := range nodes {
		sb.WriteString(fmt.Sprintf("--- %d. %s [%s] %s lines %s ---\n", i+1, n.Name, n.Kind, n.File, n.Lines))
		sb.WriteString(fmt.Sprintf("Signature: %s\n", n.Signature))
		if n.Source != "" {
			sb.WriteString(n.Source)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return makeTextResult(sb.String())
}

// --- search_codebase ---

func (s *Server) callSearchCodebase(ctx context.Context, raw json.RawMessage) toolResult {
	if !s.openIfExists() {
		return makeErrorResult("codebase not indexed — call index_codebase first")
	}
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return makeErrorResult(fmt.Sprintf("invalid args: %v", err))
	}
	if args.Query == "" {
		return makeErrorResult("query is required")
	}

	results, err := s.store.SearchFTS(args.Query, args.Limit)
	if err != nil {
		return makeErrorResult(fmt.Sprintf("search failed: %v", err))
	}
	if len(results) == 0 {
		return makeTextResult("No matches found.")
	}

	var sb strings.Builder
	for i, n := range results {
		sb.WriteString(fmt.Sprintf("--- %d. %s [%s] %s lines %s ---\n", i+1, n.Name, n.Kind, n.File, n.Lines))
		if n.Source != "" {
			sb.WriteString(n.Source)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return makeTextResult(sb.String())
}

// --- index_codebase ---

func (s *Server) callIndexCodebase(raw json.RawMessage) toolResult {
	var args struct {
		Path         string   `json:"path"`
		ChangedFiles []string `json:"changed_files"`
	}
	if raw != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return makeErrorResult("invalid arguments: " + err.Error())
		}
	}

	if len(args.ChangedFiles) > 0 {
		if err := s.ensureStore(s.rootPath); err != nil {
			return makeErrorResult("opening store: " + err.Error())
		}
		if err := s.indexer.Reindex(args.ChangedFiles); err != nil {
			return makeErrorResult(fmt.Sprintf("reindex failed: %v", err))
		}
	} else {
		root := args.Path
		if root == "" {
			root = s.rootPath
		}
		if err := s.ensureStore(root); err != nil {
			return makeErrorResult("opening store: " + err.Error())
		}
		if err := s.indexer.Index(root); err != nil {
			return makeErrorResult(fmt.Sprintf("index failed: %v", err))
		}
	}

	nodeCount, edgeCount, err := s.store.Stats()
	if err != nil {
		return makeErrorResult("indexing succeeded but stats failed: " + err.Error())
	}

	return makeTextResult(fmt.Sprintf("Indexing complete. Graph now contains %d nodes and %d edges.", nodeCount, edgeCount))
}
