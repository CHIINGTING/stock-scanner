---
name: segment-backtest-panel
description: "區間策略回測面板 (internal/segbacktest + cmd/backtest-panel → backtest.html); JS engine tested via jsc; embedded in the report's 回測洞察 tab as a lazy iframe"
metadata: 
  node_type: memory
  type: project
  originSessionId: 8ceb65a8-6d4c-4312-b25f-05c6315fdb8e
  modified: 2026-08-09T00:06:03.608Z
---

Built 2026-07-31 → 2026-08-06: an interactive 區間策略回測面板 modelled on the user's
old reference tool at goodskangtaiwanstock.netlify.app ("挑一檔股票 + 買賣日期 → 六種
加碼/停利紀律事後會怎樣").

**That reference site asserts copyright** in its own source (`未經授權禁止複製、改作`) —
its code, UI and 文案 are off limits. The strategy *concepts* (定期定額、逢跌狙擊、
停利接回、落袋為安) are generic finance and were reimplemented from scratch in the
project's own visual language. Do not copy from it if this work is revisited.

Layout:
- `internal/segbacktest/dataset.go` — reads `.cache/*.json` → shared date axis +
  per-stock close series (gaps = 0). Full 2y × ~1990 檔 ≈ 4.8 MB page.
- `internal/segbacktest/engine.js` — the strategy engine, **single source of truth**,
  pure functions. It runs in the browser but is unit-tested from Go:
  `engine_script_test.go` executes it under macOS's built-in `jsc`
  (`/System/Library/Frameworks/JavaScriptCore.framework/Versions/A/Helpers/jsc`),
  skipping when no JS runtime exists. Keep that test alive — it is the only thing
  guarding the maths.
- `internal/segbacktest/panel.js` / `panel.go` — UI + self-contained HTML render.
- `cmd/backtest-panel` → `backtest.html` (root, stable URL); `-archive reports` for a
  dated copy (opt-in on purpose: a 4.8 MB blob per day would bloat git).

Non-obvious decisions:
- Threshold comparisons go through `gte`/`lte` with epsilon. Binary FP made
  `120/100-1 = 0.19999999999999996`, so an exact +20% move failed a +20% 停利.
- 停利 measures against the **fee-inclusive** average cost, so with costs on a bare
  +20% close does NOT clear a 20% target. Intended, and asserted in tests.
- 預留現金 counts toward 總投入 even when never deployed, else idle-cash rules win on
  percentage terms.
- A Go file named `*_js_test.go` is silently ignored — `_js` is a GOOS build
  constraint. Hence `engine_script_test.go`.

Report integration (2026-08-06): the report's `🔬 回測洞察` tab
(`show_backtest_insights`) now contains **only** this panel, as a lazily-loaded
iframe — no `src` until the user clicks, so the daily report stays small. Path is
resolved from `location` (`../backtest.html` under `reports/`). The R6 static
insight cards that used to fill that tab were **removed at the user's request**
(still in git history); `report_test.go` now asserts they never come back.

Open: `backtest.html` is untracked — it must be committed for the GitHub Pages
iframe to resolve. See [[r6-backtest-status]] for the R6 work whose UI this replaced,
and [[autonomous-execution-rules]] for commit discipline.
