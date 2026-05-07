# Twig

Reduces LLM token usage by pre-indexing your codebase into a graph. Instead of reading entire files, the LLM queries the graph and gets only the relevant snippets. Built with Tree-sitter and SQLite, exposed via CLI and MCP. No LLM calls, no API keys.

## Build & Test

Always build and install using zig as the C compiler (required for cgo on this machine):

```powershell
$env:CC="zig cc"; go install ./cmd/twig/
```

Other useful commands:

```powershell
go vet ./...                            # lint
twig index .                            # index this repo as a smoke test
twig graph callers NewStore --depth 1   # verify graph works
```

## Project Layout

- `cmd/twig/main.go` — CLI entrypoint (cobra), all commands wired here
- `internal/parser/` — Tree-sitter parsing, grammar registry, SQLite store, indexer
- `internal/graphagent/` — Graph query engine (heuristic seed extraction + traversal)
- `internal/graphintel/` — Structural queries (callers, callees, deps, impact)
- `internal/orchestrator/` — Context assembler (prompt template + graph context)
- `internal/registry/` — Versioned prompt templates from `config/prompts/`
- `internal/logger/` — Append-only SQLite execution log
- `internal/eval/` — A/B prompt eval with keyword-match grading
- `internal/mcp/` — MCP server (JSON-RPC 2.0 over stdio)
- `internal/types/` — Shared types (Node, Edge, Task, Result, etc.)
- `internal/config/` — Config loading from `config.yaml`

## Key Points

- No API clients, no network calls. Fully offline.
- The LLM is the editor (Claude Code, Copilot). Twig just serves graph context.
- Seed extraction is heuristic (camelCase split, stop words, dot-join).
- MCP server exposes 5 tools: query_codebase, analyze_impact, graph_explore, graph_stats, index_codebase.

## Adding a Language

1. `go get github.com/smacker/go-tree-sitter/{language}`
2. Register extension in `internal/parser/grammar.go`
3. Add walker in `internal/parser/extract.go`

## Adding a Task Type

1. Create `config/prompts/{task}_v1.yaml`
2. Automatically available via `twig run {task}`
