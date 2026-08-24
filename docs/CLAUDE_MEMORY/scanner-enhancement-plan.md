---
name: scanner-enhancement-plan
description: "Location and scope of the Scanner Enhancement Plan (design-only roadmap, Revision 2)"
metadata: 
  node_type: memory
  type: reference
  originSessionId: 2ab912aa-bcc1-490d-ae4a-f03d45b96782
---

The Scanner Enhancement Plan lives at `docs/SCANNER_ENHANCEMENT_PLAN.md` (in-repo, design only — no code). Built on [[scanner-master-design]]. Currently at **Revision 2**, re-prioritized for selection hit-rate (not quant completeness). Positioning unchanged: EOD swing helper, 1–4 week horizon, hold 5–20 trading days. No intraday/L2/tick; not a quant platform.

**Revised Roadmap (Rev 2):**
- R1 — full-market Relative Strength Ranking (RSScore + RSRank percentile 1–99; IBD-style weighted multi-quarter return) → rewrites RocketScore group 2 + sort soft-gate. Highest ROI.
- R2 — New High Analysis (20/60/120/250d + distance-to-52w-high; 60d=core, ≤25% from 52w high = leadership gate) + VCPScore (extend Consolidation: multi-leg contraction, depths monotonically tightening, volume drying) folded into BaseQualityScore / PRE_BREAKOUT.
- R3 — MomentumFlow elevated to co-primary with RocketStage; joint decision table (e.g. PRE_BREAKOUT+BUILDING→HIGH prob PREPARE_ENTRY; PRE_BREAKOUT+FADING→WAIT/LOW; MAIN_RUN+FADING→early TAKE_PROFIT; any+SHIFT_DOWN→REMOVE pre-FAILED).
- R4 — Daily+Weekly multi-timeframe + 200d-MA long filter (Monthly DEFERRED — too slow for 5–20d hold, only ~24 monthly bars in 2y).
- R5 — Backtest tier-1: WinRate/AvgReturn/ProfitFactor/MaxDD WITH cost+slippage, forward 5/10/20d. Validates R1–R4.
- R6 — backtest tier-2 (Sharpe/Sortino/equity curve/exit-rule). R7 (deferred) — Monthly, Regime layering, Factor IC, config-ization.

Core loop: add factor → measure hit-rate via tier-1 backtest → keep if effective. Factor IC is LAST (avoid premature quantification while signals still being built).

Detailed implementation specs live alongside as `docs/SPEC_R<n>_*.md`. R1 spec exists: `docs/SPEC_R1_RS_RANKING.md`. Two code facts confirmed there: `fetcher.FetchAll()` already covers TWSE+TPEX (RS universe complete), but `Candle.Close` is UNADJUSTED (Yahoo `indicators.quote.close`; no `adjclose` parsed yet) — so R1 sub-task R1.0 must add adjclose parsing for the 252d RS lookback or returns get split/dividend-distorted.
