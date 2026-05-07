# Twig

Pre-indexes your codebase into a semantic graph using Tree-sitter. LLMs query the graph and get back only the relevant
snippets - no more reading entire files. No API keys, no network, fully offline.

---

## Prerequisites

- **Go 1.26+**
- **C compiler** (cgo dependency for SQLite + Tree-sitter) — any of:
    - **[Zig](https://ziglang.org/download/)** (recommended, cross-platform)
    - Windows: [MSYS2](https://www.msys2.org/) or [TDM-GCC](https://jmeubank.github.io/tdm-gcc/)
    - macOS: `xcode-select --install`
    - Linux: `apt install build-essential`

---

## Quick Start

**Building with Zig (recommended):**

Set `CC` once permanently so every `go build`/`go install` picks it up:
```powershell
[System.Environment]::SetEnvironmentVariable("CC", "zig cc", "User")
```
Then reopen your terminal and build normally:
```powershell
go install ./cmd/twig/
```

Or set it inline per-command:
```powershell
$env:CC="zig cc"; go install ./cmd/twig/
```

**Building with GCC:**
```bash
go build -o twig.exe ./cmd/twig/   # local binary
go install ./cmd/twig/             # install to $GOPATH/bin

twig index ./path/to/codebase
twig graph callers MyFunction --depth 3
```

---

## Claude Code Integration (MCP)

**User-level (all projects):**

```bash
claude mcp add --scope user twig -- twig serve --mcp
```

**Project-level** — create `.mcp.json` in the project root:

```json
{
  "mcpServers": {
    "twig": {
      "command": "twig",
      "args": ["serve", "--mcp"]
    }
  }
}
```

> Note: `~/.claude/mcp.json` is not read by Claude Code CLI. Use `claude mcp add` or `.mcp.json` (dot-prefixed, in project root).

Available tools: `query_codebase`, `analyze_impact`, `graph_explore`, `graph_stats`, `index_codebase`.

---

## Configuration

Optional `config.yaml` in working directory:

```yaml
default_token_budget: 4000
db_path: ./twig.db
codebase_root: ./
```

---

## CLI Reference

Run any command without arguments for usage. Key commands:

```bash
twig index <path>               # parse + store the graph
twig graph callers <symbol>     # who calls this?
twig graph callees <symbol>     # what does this call?
twig graph deps <symbol>        # dependencies
twig graph impact <symbol>      # blast radius
twig graph query "<text>"       # keyword search
twig run <task> --input <...>   # assemble prompt with graph context
twig serve --mcp                # start MCP server
twig log list                   # view run history
twig eval <fixtures.yaml>       # A/B prompt comparison
```

Supported languages: C#, Java, Go, Python, JavaScript, TypeScript.

---

## Extending

**New language:** `go get` the tree-sitter grammar, register extension in `internal/parser/grammar.go`, add walker in
`internal/parser/extract.go`.

**New task type:** Create `config/prompts/{task}_v1.yaml`. Automatically available via `twig run {task}`.

---

## Dependencies

| Package          | Purpose             |
|------------------|---------------------|
| `go-tree-sitter` | AST parsing         |
| `go-sqlite3`     | graph + log storage |
| `cobra`          | CLI                 |
| `yaml.v3`        | config + templates  |
