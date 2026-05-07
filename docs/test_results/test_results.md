# Cortex — Test Results

Tested against its own codebase: 14 Go files, 4,680 lines, 200 nodes, 943 edges.

## Token Savings

Test query: `"NewIndexer Store Extract"`, 1000-token budget.

| Approach | Lines read | Est. tokens | vs Cortex |
|---|---|---|---|
| Cortex `query_codebase` | 17 lines, 3 files | 203 | 1x |
| Read the 3 source files | 538 lines | ~4,000 | 20x |
| Read the full package | 1,458 lines | ~10,000 | 49x |
| Read the full codebase | 4,680 lines | ~31,000 | 153x |

`analyze_impact NewStore` returned 4 direct callers, 14 transitive dependents, 1 affected file in ~300 tokens. Same answer from file reading: ~5,000 tokens.

## What Worked

| Feature | Result |
|---|---|
| `graph callers` | Correct — found all 4 direct callers of NewStore |
| `graph callees` | Correct — found all 7 callees of initComponents |
| `graph impact` | Correct — transitive callers, affected files, risk score |
| `graph query "what calls NewStore"` | Correct — keyword match classified as callers query, returned 14 results |
| MCP `initialize` / `tools/list` | Correct protocol, 5 tools with schemas |
| MCP `graph_stats` | Returned 200 nodes, 943 edges |
| MCP `analyze_impact` | Full structured report |
| MCP `query_codebase` (symbol terms) | Returned 4 targeted snippets, 203 tokens |

## What Didn't Work

| Issue | Example |
|---|---|
| Receiver method calls not resolved | `graph impact Indexer.Index` returns 0 callers — `indexer.Index()` in code produces bare `Index` edge |
| Open-ended NL queries miss | `"how does indexing work"` returns nothing — no word matches a symbol name |
| `graph deps` on types | `deps Store` returns nothing — types don't have outgoing CALLS edges |

## Extended Test Runs

### MCP `query_codebase` — symbol-based queries

| Query | Budget | Snippets | Tokens used | Files touched | File reading cost | Savings |
|---|---|---|---|---|---|---|
| `"NewIndexer Store Extract"` | 1000 | 4 | 203 | 3 | ~4,000 (538 lines) | **20x** |
| `"GraphAgent Query traversal"` | 500 | 2 | 377 | 1 | ~3,500 (525 lines) | **9x** |
| `"NewRegistry PromptTemplate Render"` | 500 | 1 | 349 | 1 | ~1,150 (173 lines) | **3x** |
| `"Server dispatch handleToolsCall"` | 800 | 2 | 155 | 1 | ~4,160 (624 lines) | **27x** |

### MCP `query_codebase` — open-ended NL queries

| Query | Budget | Result |
|---|---|---|
| `"how does the MCP server handle requests"` | 500 | No snippets — no word matched a symbol |
| `"how does indexing work"` | 500 | No snippets — same issue |

### CLI `graph query` — NL classification

| Query | Classified as | Result |
|---|---|---|
| `"what calls NewStore"` | callers | Correct — 14 callers found |
| `"impact of changing NewStore"` | impact | Correct — 4 direct, 14 transitive, risk 4 |
| `"what does walkGo call"` | callers | Misclassified (should be callees) — but still returned 9 related nodes |
| `"what depends on Store"` | deps | Correct classification — 0 results (types have no outgoing CALLS) |

### CLI `graph callers/callees/impact`

| Command | Result | Correct |
|---|---|---|
| `graph callers NewStore --depth 1` | 4 callers | Yes |
| `graph callers NewGraphIntel --depth 2` | 11 callers (transitive) | Yes |
| `graph callers NewLogger --depth 1` | 2 callers | Yes |
| `graph callees initComponents --depth 1` | 7 callees | Yes |
| `graph callees Server.Serve --depth 1` | 0 callees | No — receiver calls unresolved |
| `graph impact NewOrchestrator` | 1 direct, 11 transitive | Yes |
| `graph impact NewRegistry` | 1 direct, 11 transitive | Yes |
| `graph impact Indexer.Index` | 0 callers | No — receiver calls unresolved |

### MCP `graph_explore`

| Mode | Symbol | Depth | Result | Correct |
|---|---|---|---|---|
| callers | NewStore | 1 | 4 callers with file/line info | Yes |
| callees | initComponents | 1 | 7 callees across 7 packages | Yes |

### Token comparison summary

| Scenario | Cortex tokens | File reading tokens | Ratio |
|---|---|---|---|
| Find callers of a function | ~150 (structured list) | ~4,200 (read main.go to scan for calls) | **28x** |
| Impact analysis | ~300 (structured report) | ~5,000+ (read multiple files, trace callers) | **17x** |
| Retrieve specific function + neighbors | 155-377 (snippets) | 1,150-4,160 (full source files) | **3-27x** |
| Retrieve 4 related definitions | 203 (4 snippets) | ~31,000 (full codebase to find them) | **153x** |

## Conclusions

- **3-150x token reduction** depending on query specificity and how many files the LLM would otherwise need.
- Structural queries (`callers`, `impact`, `callees`) are the strongest feature — instant, accurate, and impossible to replicate by file reading without cross-referencing multiple files.
- `query_codebase` works well when the query contains symbol names. Savings are proportional to how spread out the relevant code is across files.
- Open-ended NL queries are the weak spot. Needs fuzzy/substring matching and file-level fallback.
- Method call resolution is the main parser gap. `Server.Serve` and `Indexer.Index` return 0 callees because receiver calls aren't resolved to their qualified names. Fixing this would improve graph completeness significantly.
- NL classification occasionally misclassifies ("what does X call" → callers instead of callees). Keyword rules need refinement.
