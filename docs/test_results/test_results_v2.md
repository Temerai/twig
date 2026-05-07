# Cortex — Test Results v2

Tested against its own codebase: 14 Go files, 207 nodes, 1,465 edges.
Previous baseline (v1): 200 nodes, 943 edges. Edge count increased 55% after receiver call resolution fix.

## What Changed Since v1

Three fixes applied to address v1 shortcomings:

1. **Receiver method call resolution** — `extractCallTarget` now preserves `receiver.method` form; `resolveCallEdges` uses multi-step resolution (exact → dot-split → suffix match).
2. **Fuzzy NL query matching** — `extractSeeds` and `resolveSymbol` fall back to progressive prefix matching when exact lookup fails. File-path keyword fallback added as last resort.
3. **Type deps expansion** — `Dependencies()` now aggregates outgoing edges from a type's methods, not just the type node itself.

## Structural Queries (callers / callees / impact)

| Command | Result | Correct | v1 |
|---|---|---|---|
| `graph callers NewStore --depth 1` | 4 callers | Yes | Yes |
| `graph callers NewGraphIntel --depth 2` | 11 callers (transitive) | Yes | Yes |
| `graph callees initComponents --depth 1` | 7 callees | Yes | Yes |
| `graph callers Indexer.Index --depth 1` | 4 callers | Yes | **No (was 0)** |
| `graph callees Server.Serve --depth 1` | 3 callees | Yes | **No (was 0)** |
| `graph impact Indexer.Index` | 4 direct, 23 transitive, 5 files | Yes | **No (was 0)** |
| `graph impact NewStore` | 4 direct, 14 transitive, 1 file | Yes | Yes |

## Type Dependencies

| Command | Result | Correct | v1 |
|---|---|---|---|
| `graph deps Indexer` | 10 deps (Store methods, Extractor, GrammarRegistry) | Yes | **No (was 0)** |
| `graph deps GraphIntel` | 14 deps (Store methods, formatters, Indexer.Index) | Yes | **No (was 0)** |
| `graph deps Store` | 0 deps (all calls are to stdlib — correct) | Yes | was 0 |

## NL Queries (`graph query`)

| Query | Classified as | Result | Correct | v1 |
|---|---|---|---|---|
| `"what calls NewStore"` | callers | 14 callers | Yes | Yes |
| `"impact of changing NewStore"` | impact | 4 direct, 14 transitive | Yes | Yes |
| `"what does walkGo call"` | callers | 11 related nodes | Partial | Partial |
| `"what depends on Store"` | deps | 0 deps (stdlib only) | Yes | was 0 |
| `"how does indexing work"` | callers | 1 result (fuzzy: "indexing" → `newIndexCmd`) | Yes | **No (was error)** |
| `"how does the MCP server handle requests"` | callers | 0 callers for resolved symbol | Partial | **No (was error)** |

## MCP Protocol

| Tool | Result | Correct |
|---|---|---|
| `initialize` / `tools/list` | 5 tools with schemas | Yes |
| `graph_stats` | 207 nodes, 1,465 edges | Yes |
| `analyze_impact NewStore` | 4 direct, 14 transitive, risk 4 | Yes |
| `graph_explore callers Indexer.Index` | 4 callers with file/line info | Yes (was 0 in v1) |
| `query_codebase "NewIndexer Store Extract"` (budget 1000) | 4 snippets, 203 tokens | Yes |
| `query_codebase "how does indexing work"` (budget 500) | 3 snippets, 394 tokens | Yes (was 0 in v1) |
| `query_codebase "how does the MCP server handle requests"` (budget 500) | 4 snippets, 371 tokens | Yes (was 0 in v1) |

## Token Savings (unchanged from v1)

| Scenario | Cortex tokens | File reading tokens | Ratio |
|---|---|---|---|
| Find callers of a function | ~150 | ~4,200 | **28x** |
| Impact analysis | ~300 | ~5,000+ | **17x** |
| Specific function + neighbors | 155-377 | 1,150-4,160 | **3-27x** |
| NL query "how does indexing work" | 394 | ~10,000 (read indexer + MCP files) | **25x** |

## Remaining Shortcomings

| Issue | Example | Severity |
|---|---|---|
| NL classification: "what does X call" → callers | `"what does walkGo call"` classified as `callers` instead of `callees` | Low — still returns related nodes |
| CLI `graph query` NL resolves but picks weak symbol | `"how does the MCP server handle requests"` resolves to `Server.callRunTask` (0 callers) instead of `Server.Serve` | Medium — MCP `query_codebase` handles this well (4 snippets), only CLI `graph query` path affected |
| `deps Store` returns 0 | Store methods only call stdlib (`fmt.Errorf`, `sql.Scan`, etc.) which have no graph nodes | Expected — not a bug, external calls are untracked by design |
| Fuzzy match can over-match | Short common stems (e.g., "log") could match many unrelated nodes | Low — mitigated by minimum 3-char prefix and 20-result limit |

## Conclusions

- All three v1 shortcomings are fixed: receiver methods resolve, NL queries return results, type deps work.
- Graph completeness improved significantly: 943 → 1,465 edges (+55%) from receiver call resolution.
- `query_codebase` via MCP is the strongest NL path — fuzzy seed matching + BFS traversal handles open-ended queries well.
- CLI `graph query` is weaker for NL because `classifyQuestion` picks a single symbol and query type; queries where the best symbol isn't the highest-scored candidate can miss.
- Structural queries (`callers`, `callees`, `impact`) remain the most reliable feature.
