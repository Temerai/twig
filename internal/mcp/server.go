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
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server implements a minimal MCP server over stdio using JSON-RPC 2.0.
type Server struct {
	store   *parser.Store
	indexer *parser.Indexer
	agent   *graphagent.GraphAgent
	intel   *graphintel.GraphIntel
}

// NewServer creates a new MCP server wired to the provided components.
func NewServer(
	store *parser.Store,
	indexer *parser.Indexer,
	agent *graphagent.GraphAgent,
	intel *graphintel.GraphIntel,
) *Server {
	return &Server{
		store:   store,
		indexer: indexer,
		agent:   agent,
		intel:   intel,
	}
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
			Name:        "query_codebase",
			Description: "Query the codebase graph using natural language. Returns relevant code snippets assembled via graph traversal.",
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"question": {
						Type:        "string",
						Description: "Natural language question about the codebase",
					},
					"token_budget": {
						Type:        "integer",
						Description: "Maximum tokens of code context to return",
						Default:     4000,
					},
					"strategy": {
						Type:        "string",
						Description: "Graph traversal strategy",
						Default:     "scored",
						Enum:        []string{"bfs", "scored", "deep"},
					},
				},
				Required: []string{"question"},
			},
		},
		{
			Name:        "analyze_impact",
			Description: "Analyze the blast radius of changing a symbol. Returns direct callers, transitive dependents, affected files, and a risk score.",
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"symbol": {
						Type:        "string",
						Description: "Name of the function, method, or type to analyze",
					},
					"depth": {
						Type:        "integer",
						Description: "Maximum depth for transitive dependency analysis",
						Default:     3,
					},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "graph_explore",
			Description: "Explore the codebase graph by listing callers, callees, or dependencies of a symbol.",
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"mode": {
						Type:        "string",
						Description: "Exploration mode",
						Enum:        []string{"callers", "callees", "deps"},
					},
					"symbol": {
						Type:        "string",
						Description: "Name of the function, method, or type to explore",
					},
					"depth": {
						Type:        "integer",
						Description: "Maximum traversal depth",
						Default:     3,
					},
				},
				Required: []string{"mode", "symbol"},
			},
		},
		{
			Name:        "graph_stats",
			Description: "Return the number of nodes and edges currently in the codebase graph.",
			InputSchema: toolSchema{
				Type:       "object",
				Properties: map[string]schemaProperty{},
			},
		},
		{
			Name:        "index_codebase",
			Description: "Index (or re-index) the codebase at the given path. Returns node and edge counts after indexing.",
			InputSchema: toolSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"path": {
						Type:        "string",
						Description: "Root directory to index (defaults to the configured codebase root)",
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
	case "scored", "":
		strategy = types.StrategyScored
	default:
		return makeErrorResult(fmt.Sprintf("unknown strategy %q; use bfs, scored, or deep", args.Strategy))
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

	report, err := s.intel.ImpactOf(ctx, args.Symbol)
	if err != nil {
		return makeErrorResult("impact analysis failed: " + err.Error())
	}

	return makeTextResult(formatImpactReport(args.Symbol, report))
}

func formatImpactReport(symbol string, r *types.ImpactReport) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Impact analysis for %q:\n", symbol))
	sb.WriteString(fmt.Sprintf("  Risk score: %d\n", r.RiskScore))
	sb.WriteString(fmt.Sprintf("  Direct callers: %d\n", len(r.DirectCallers)))
	for _, n := range r.DirectCallers {
		sb.WriteString(fmt.Sprintf("    - %s (%s, %s)\n", n.Name, n.Kind, n.File))
	}
	sb.WriteString(fmt.Sprintf("  Transitive dependents: %d\n", len(r.TransitiveDeps)))
	for _, n := range r.TransitiveDeps {
		sb.WriteString(fmt.Sprintf("    - %s (%s, %s)\n", n.Name, n.Kind, n.File))
	}
	sb.WriteString(fmt.Sprintf("  Affected files: %d\n", len(r.AffectedFiles)))
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
	nodeCount, edgeCount, err := s.store.Stats()
	if err != nil {
		return makeErrorResult("stats failed: " + err.Error())
	}
	return makeTextResult(fmt.Sprintf("Graph contains %d nodes and %d edges.", nodeCount, edgeCount))
}

// --- index_codebase ---

type indexCodebaseArgs struct {
	Path string `json:"path"`
}

func (s *Server) callIndexCodebase(raw json.RawMessage) toolResult {
	var args indexCodebaseArgs
	if raw != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return makeErrorResult("invalid arguments: " + err.Error())
		}
	}

	rootPath := args.Path
	if rootPath == "" {
		rootPath = s.indexer.RootPath()
	}

	if err := s.indexer.Index(rootPath); err != nil {
		return makeErrorResult("indexing failed: " + err.Error())
	}

	nodeCount, edgeCount, err := s.store.Stats()
	if err != nil {
		return makeErrorResult("indexing succeeded but stats failed: " + err.Error())
	}

	return makeTextResult(fmt.Sprintf("Indexing complete. Graph now contains %d nodes and %d edges.", nodeCount, edgeCount))
}
