---
name: scanner-ab-guardrail-status
description: Guardrail scoring A/B status — B validated on cache only; keep A daily / B control; never commit B as config default yet
metadata: 
  node_type: memory
  type: project
  originSessionId: 19003089-9b50-4abc-b28a-075d50a8075a
---

Scanner guardrail-scoring (C6b) A/B validation status as of 2026-06-07.

- **A mode** = `enable_signal_guardrail_scoring: false` (shadow-only). This is the **daily observation mode** and the current `configs/config.yaml` default — all other guardrail flags (rs_rank, new_high, vcp, momentum_flow, multi_timeframe, show_guardrail_signals) are already `true`.
- **B mode** = same but `enable_signal_guardrail_scoring: true` (guardrail actually moves score/action/prob + MTF tie-break sort). This is **control / validation mode only**.

**Decision:** Do NOT make B the committed config default, and do NOT treat B as the official daily ranking, until it is re-validated on **fresh full-market data** (not the .cache). The 2026-06-04-cache A/B passed all safety checks (MTF overturnViolations=0, no score inflation, no over-filtering, REMOVE 0→0, score/action/prob changes all explainable via RS/NewHigh/VCP/MomentumFlow-FADING), but cache data ≠ live ranking.

**Pending next step (user will manually trigger):** re-run official A/B once Yahoo lifts the 429 IP block, using fresh data and an RS universe as close to full market as possible. Method = throwaway harness that fetches once then runs both configs on identical data, emits report_A_shadow_only.html / report_B_guardrail_scoring.html + diff; never change code, never change/commit config defaults, leave tree clean.

Aligns with [[scanner-direction]] and the R5 calibration ethos in [[scanner-enhancement-plan]] (validate on our own TW data before fixing params).
