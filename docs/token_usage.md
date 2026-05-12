# twig Token Usage Log

Dev log of actual token consumption from twig queries. Used to calibrate token budgets and validate graph quality.

> **Note:** All token counts use twig's word-run estimator (not BPE). Figures are consistent for comparing rows against each other but will diverge from actual model `usage.input_tokens` by 20–35% for code. Baseline figures use the same estimator, so the savings ratio is reliable.

| Date | Command | Task / Question | Strategy | Snippets | Tokens Used | Budget | Baseline (no twig) | Savings | Notes |
|---|---|---|---|---|---|---|---|---|---|
| 2026-05-06 | `twig run explain` | "how does the orchestrator assemble prompts and what task types are available" | scored | 24 | 3301 | 5000 | ~4050 min / ~7760 full | ~1.2x–2.4x | Doc generation — agentic_mode.md |
| 2026-05-11 | `analyze_impact` | Store (pre-v0.2.0) | — | 0 | ~50 | — | ~2400 (8 files) | ~48x | Risk score 0 — no USES edges |
| 2026-05-11 | `analyze_impact` | Store (v0.2.0) | — | 12 | ~350 | — | ~2400 (8 files) | ~7x | Risk score 12, 12 direct users, 19 transitive deps |
| 2026-05-11 | `analyze_impact` | Node (v0.2.0) | — | 53 | ~800 | — | ~5000 (full codebase) | ~6x | Risk score 53, 53 direct users |
| 2026-05-11 | `graph_stats` | Full stats (v0.2.0) | — | — | ~120 | — | N/A | N/A | 241 nodes, 1462 edges (253 USES, 1076 CALLS, 133 IMPORTS) |
| 2026-05-11 | `search_codebase` | "Store" via FTS5 | — | 5 | ~150 | — | ~2400 (grep all files) | ~16x | FTS5 returns top-5 ranked matches |
