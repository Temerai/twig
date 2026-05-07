package types

// Node represents a code element in the codebase graph.
type Node struct {
	ID        string
	File      string
	Language  string
	Kind      string // function, method, class, struct, interface
	Name      string
	Signature string
	Lines     string // "10-25"
}

// Edge represents a relationship between two nodes in the codebase graph.
type Edge struct {
	Src  string
	Dst  string
	Kind string // CALLS, IMPORTS, DEFINES, INHERITS
}

// CodeSnippet holds a retrieved source code fragment with metadata.
type CodeSnippet struct {
	NodeName   string
	FilePath   string
	LineRange  string
	SourceText string
	TokenCount int
}

// TraversalStrategy determines how the graph is traversed during queries.
type TraversalStrategy string

const (
	StrategyBFS    TraversalStrategy = "bfs"
	StrategyScored TraversalStrategy = "scored"
	StrategyDeep   TraversalStrategy = "deep"
)

// QueryRequest describes a natural-language query against the codebase graph.
type QueryRequest struct {
	NaturalLanguage string
	TokenBudget     int
	Strategy        TraversalStrategy
}

// QueryResult holds the snippets and metadata returned from a graph query.
type QueryResult struct {
	Snippets     []CodeSnippet
	TokensUsed   int
	NodesVisited []string
}

// ImpactReport describes the blast radius of a change to a given node.
type ImpactReport struct {
	DirectCallers  []Node
	TransitiveDeps []Node
	AffectedFiles  []string
	RiskScore      int
}

// GraphAnswer is a structured response combining a summary with graph data.
type GraphAnswer struct {
	Summary string
	Nodes   []Node
	Edges   []Edge
}

// Task represents a unit of work for the orchestrator.
type Task struct {
	Type    string
	Input   string
	Options map[string]string
}

// Result holds the output and usage metrics from processing a task.
type Result struct {
	Output       string
	GraphQueries []QueryRequest
	TokensIn     int
	TokensOut    int
	LatencyMs    int64
}
