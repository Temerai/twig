# twig

Reduces LLM token usage by pre-indexing your codebase into a graph. Instead of reading entire files, the LLM queries the graph and gets only the relevant snippets. Built with Tree-sitter and SQLite, exposed via CLI and MCP. No LLM calls, no API keys.

## Build & Test

Cross-platform build script (auto-detects zig, applies FTS5 tag):

```powershell
go run build.go          # build
go run build.go test     # run tests
go run build.go smoke    # full verification (vet + build + index + smoke tests)
go run build.go clean    # remove build artifacts
```

Manual build (if needed):

```powershell
$env:CC="zig cc"; go build -tags sqlite_fts5 -o .\twig.exe ./cmd/twig/
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
- The LLM is the editor (Claude Code, Copilot). twig just serves graph context.
- Seed extraction is heuristic (camelCase split, stop words, dot-join).
- MCP server exposes 7 tools: query_codebase, analyze_impact, graph_explore, graph_stats, get_symbol, index_codebase, search_codebase.

## Adding a Language

1. `go get github.com/smacker/go-tree-sitter/{language}`
2. Register extension in `internal/parser/grammar.go`
3. Add walker in `internal/parser/extract.go`

## Functionality Tests

End-to-end integration tests that index twig's own source and exercise every major feature. Run when asked to verify functionality:

```powershell
go test -tags sqlite_fts5 -v -count=1 ./internal/integration/
```

Tests cover: graph indexing, USES edges (Go/Java/C#), analyze_impact, query_codebase (all 4 strategies), get_symbol, search_codebase (FTS5 + special chars + OR), graph_explore (callers/callees/deps), incremental reindex, detailed stats, and budget enforcement. Located in `internal/integration/functionality_test.go`.

## Adding a Task Type

1. Create `config/prompts/{task}_v1.yaml`
2. Automatically available via `twig run {task}`
