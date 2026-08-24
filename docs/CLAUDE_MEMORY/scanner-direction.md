---
name: scanner-direction
description: Stock-scanner is repositioned for 1-4 week swing trades; intended core flow is Rotation→Sector→Stock→Watchlist→Position
metadata: 
  node_type: memory
  type: project
  originSessionId: 6af09206-d5a4-447f-a1b2-e72364c50041
---

The stock-scanner project is positioned to **find stocks with opportunity over the next 1–4 weeks**, NOT day trading. 

The user's intended future core flow (law-of-institutional stock picking — find the sector first, then the stock):

```
Rotation → Sector → Stock → Watchlist → Position
```

The "輪動 (Rotation)" page (added 2026-06-03) is the sector-level entry point: it answers "where might money rotate to next?" rather than "what's strongest today?", and deliberately de-emphasizes already-hot sectors (HOT/LATE stages) in favor of EARLY/CONFIRMED via opportunity-weighted ranking. Rotation uses a three-layer model: short_term_flow (1–5d, leading), mid_term_strength (20d Sector Score), trend_strength (60d). Sectors are defined in `configs/sectors.yaml` (seed data — codes need user verification).

The **Watchlist** has been redefined as a **飆股候選追蹤系統 (rocket-candidate tracker)**: "Scanner finds candidates, Watchlist judges whether one is about to become a 飆股." Each watchlist stock gets a rocket_candidate_score (0–100), rocket_stage (NOT_READY→...→OVERHEATED/FAILED), watch_action, a backtest (own-history + sector-pooled, same pattern + consolidation bucket), and consolidation-as-classification (MICRO/SHORT/SWING/MID/LONG base — duration is NOT a reward; quality = base_quality_score). UI is two-layer: compact list → expandable decision card. Watchlist links to its sector's rotation flow. Fetch history default is 2y (`fetcher.history_range`) for backtest samples.

**Why:** Guides how new features should be framed — swing-trade horizon, sector-first thinking, and "longer consolidation is NOT inherently better."
**How to apply:** When extending the scanner, prefer surfacing early/confirmed setups over chasing current leaders; keep the Rotation→Sector→Stock→Watchlist→Position pipeline in mind; treat consolidation duration as a pattern type, not a score input.
