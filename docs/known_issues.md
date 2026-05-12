# twig — Known Issues

## Parser / Indexer

- **Ambiguous bare-name resolution.** When multiple nodes share the same unqualified name (e.g., `Index`, `Close`), the
  resolver cannot pick between them and the CALLS edge is dropped. Affects any codebase with common method names across
  types.

- **Cross-file INHERITS edges missing.** Class inheritance is only extracted within a single parse unit. A class
  extending one defined in another file produces no INHERITS edge.

- **Arrow function assignments in JS/TS (nested/exports).** `const foo = () => {}` is detected at the top level but is
  missed when nested or exported via `module.exports = { handler: () => {} }`.

## GraphAgent / query_codebase

- **CLI `graph query` resolves to weak symbol for some NL queries.** `classifyQuestion` picks a single seed symbol; when
  the highest-scored candidate isn't the most relevant one (e.g., `"how does the MCP server handle requests"` resolves
  to `Server.callRunTask` with 0 callers instead of `Server.Serve`), the result is effectively empty. MCP
  `query_codebase` is not affected — it uses multi-seed BFS and handles these queries correctly.

- **Fuzzy seed matching can over-match on short stems.** A query word like `"log"` can match many unrelated nodes via
  prefix matching. Currently mitigated by a 3-character minimum and a 20-result cap, but noisy results are still
  possible on common short identifiers.

## GraphIntel

- **NL classification misroutes direction queries.** `"what does X call"` is classified as `callers` instead of
  `callees` because the keyword rules treat `"call"` as a callers signal regardless of grammatical direction. The query
  still returns related nodes but from the wrong direction.

## USES edges (v0.2.0)

- **USES edges only implemented for Go, Java, and C#.** Python and JS/TS walkers still only emit CALLS/IMPORTS edges.

- **Java/C# USES edges cover method params, return types, and field types.** Generic type arguments (e.g., `List<Foo>`) emit a USES edge for the outer container type only; the type argument `Foo` itself is not captured.

- **Cross-file inheritance is not tracked.** No walker produces INHERITS edges, so `extends`/`:` relationships
  across files are invisible to `analyze_impact`.

## Dev Notes

- **`stopWords` map duplicated** between `graphagent/agent.go` and `graphintel/intel.go`. Extract to shared package
  when either map changes.

- **`Callers`/`Users`/`Callees` in `graphintel/intel.go` are near-identical.** Refactor to a single parameterised
  BFS helper (`bfsTraverse(ctx, symbol, depth, direction, edgeKind)`) to reduce ~80 lines of duplication.

- **`ImpactOf` resolves the symbol redundantly.** Each sub-call (`Callers`, `Users`) re-resolves the same symbol
  against the database. Pass resolved seeds directly to avoid 5x DB lookups.

- **`fileCache` in `graphagent` is not goroutine-safe.** Currently safe because the MCP server is single-threaded.
  Add a `sync.Mutex` before adding concurrent request handling.

- **`SearchNodesFuzzy` runs up to 8 unindexed full-table scans.** Consider a trigram index or cached prefix table
  for large codebases.

- **`readNodeSourceText` in `indexer.go` is unused in production.** Kept for tests but could be removed if
  `extractLinesFromSlice` covers all needs.
