# Cortex — Test Results Summary

Three iterations tested against Cortex's own codebase.

## Graph Stats

| Metric | v1 | v2 | v3 |
|---|---|---|---|
| Go files indexed | 14 | 14 | 16 |
| Nodes | 200 | 207 | 213 |
| Edges | 943 | 1,465 (+55%) | 1,490 |

The v2 edge jump (+55%) came entirely from receiver call resolution. The v3 increase reflects the new `internal/tokenizer/` package (2 files, 6 nodes, 25 edges).

## What Each Version Fixed

| Fix | v1 | v2 | v3 |
|---|---|---|---|
| Receiver method call resolution (`Indexer.Index`, `Server.Serve`) | — | Done | — |
| Type dependency expansion (`graph deps Indexer`) | — | Done | — |
| Fuzzy/prefix NL query matching | — | Done | — |
| File-path keyword fallback | — | Done | — |
| Consistent token counting (single estimator) | — | — | Done |

## Structural Queries

| Command | v1 | v2 | v3 |
|---|---|---|---|
| `graph callers NewStore --depth 1` | 4 ✓ | 4 ✓ | 4 ✓ |
| `graph callers NewGraphIntel --depth 2` | 11 ✓ | 11 ✓ | 11 ✓ |
| `graph callees initComponents --depth 1` | 7 ✓ | 7 ✓ | 7 ✓ |
| `graph callers Indexer.Index --depth 1` | **0 ✗** | 4 ✓ | 4 ✓ |
| `graph callees Server.Serve --depth 1` | **0 ✗** | 3 ✓ | 3 ✓ |
| `graph impact Indexer.Index` | **0 callers ✗** | 4 direct, 23 transitive ✓ | 4 direct, 23 transitive ✓ |
| `graph deps Indexer` | **0 ✗** | 10 deps ✓ | 10 deps ✓ |
| `graph deps Store` | 0 (expected) | 0 (expected) | 0 (expected) |

## NL Queries (`graph query`)

| Query | v1 | v2 | v3 |
|---|---|---|---|
| `"what calls NewStore"` | callers, 14 ✓ | callers, 14 ✓ | callers, 14 ✓ |
| `"impact of changing NewStore"` | impact, correct ✓ | impact, correct ✓ | impact, correct ✓ |
| `"what does walkGo call"` | misclassified → callers, 9 nodes ⚠ | misclassified → callers, 11 nodes ⚠ | misclassified → callers, 11 nodes ⚠ |
| `"how does indexing work"` | **no results ✗** | 1 result (fuzzy) ✓ | 1 result (fuzzy) ✓ |
| `"how does the MCP server handle requests"` | **no results ✗** | resolves to weak symbol, 0 callers ⚠ | resolves to weak symbol, 0 callers ⚠ |

## MCP `query_codebase`

| Query | v1 snippets / tokens | v2 snippets / tokens | v3 snippets / tokens |
|---|---|---|---|
| `"NewIndexer Store Extract"` (budget 1000) | 4 / 203 | 4 / 203 | 4 / 270 |
| `"how does indexing work"` (budget 500) | **0 / 0 ✗** | 3 / 394 ✓ | 3 / 500 ✓ |
| `"how does the MCP server handle requests"` (budget 500) | **0 / 0 ✗** | 4 / 371 ✓ | 5 / 500 ✓ |

Token counts are higher in v3 because the estimator is more accurate (each punctuation token now counts as ~1, not 0.25). Budget utilization improved from ~74-79% to ~100%.

## Token Savings vs File Reading

| Scenario | Cortex tokens | File reading tokens | Ratio |
|---|---|---|---|
| Find callers of a function | ~150 | ~4,200 | **28x** |
| Impact analysis | ~300 | ~5,000+ | **17x** |
| Specific function + neighbors (budget 1000) | 270 | 1,150–4,160 | **4–15x** |
| NL query "how does indexing work" (budget 500) | 500 | ~10,000 | **20x** |
| NL query "MCP server requests" (budget 500) | 500 | ~8,000 | **16x** |

## Remaining Open Issues (as of v3)

| Issue | Severity |
|---|---|
| NL classification: `"what does X call"` → callers instead of callees | Low |
| CLI `graph query` picks weak symbol for some NL queries (MCP path unaffected) | Medium |
| Ambiguous name resolution (multiple nodes with same bare name) | Medium |
| Cross-file INHERITS edges | Low |
| Arrow function assignments in JS/TS (nested / module.exports) | Low |

## Conclusions

- **v2 was the high-impact release.** Receiver call resolution alone added 522 edges (+55%) and fixed the most visible failures (receiver-qualified callers/callees returning 0). Fuzzy NL matching eliminated silent empty results on open-ended queries.
- **v3 fixed correctness, not features.** Token counting was inconsistent between traversal and assembly; unifying them improved budget utilization without changing what the graph contains.
- **Structural queries are the strongest feature** — accurate, instant, and consistent across all three versions for plain function names. Receiver-qualified names are now equally reliable after v2.
- **MCP `query_codebase` is the preferred NL path.** Multi-seed BFS handles open-ended queries well. The CLI `graph query` single-symbol path is weaker for NL and still mis-routes some queries.
- **No regressions introduced in any version.** All queries that passed in v1 continued to pass in v2 and v3.
