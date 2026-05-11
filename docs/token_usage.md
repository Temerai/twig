# twig Token Usage Log

Dev log of actual token consumption from twig queries. Used to calibrate token budgets and validate graph quality.

> **Note:** All token counts use twig's word-run estimator (not BPE). Figures are consistent for comparing rows against each other but will diverge from actual model `usage.input_tokens` by 20–35% for code. Baseline figures use the same estimator, so the savings ratio is reliable.

| Date | Command | Task / Question | Strategy | Snippets | Tokens Used | Budget | Baseline (no twig) | Savings | Notes |
|---|---|---|---|---|---|---|---|---|---|
| 2026-05-06 | `twig run explain` | "how does the orchestrator assemble prompts and what task types are available" | scored | 24 | 3301 | 5000 | ~4050 min / ~7760 full | ~1.2x–2.4x | Doc generation — agentic_mode.md |
