---
name: signal-validator
description: Signal Validation Report feature — cmd/validate-signals + internal/validator; key design decisions & gotchas
metadata: 
  node_type: memory
  type: project
  originSessionId: 05d462cf-c8ce-41d6-9288-7ad91ddbcd5f
---

Independent module validating past `reports/report_YYYYMMDD.html` judgments against forward
returns: `cmd/validate-signals` + `internal/validator` (parser/price/benchmark/evaluator/csv/report/filter).
Does NOT touch `cmd/scanner`. Added 2026-07-06.

Non-obvious decisions worth remembering:
- Reports use **two action enums**: `scanner.Action` (STRONG BUY/BUY/WATCH/HOLD/REDUCE/TAKE PROFIT/
  STOP LOSS/SELL, rendered `.action-badge`) and `scanner.WatchAction` (WAIT/WATCH_CLOSELY/PREPARE_ENTRY/
  BREAKOUT_BUY/PULLBACK_BUY/TAKE_PROFIT/REMOVE_FROM_WATCHLIST, rendered `.act-badge`). Parser keys off
  these stable classes + `.sym`/`.name-col`; no embedded JSON exists.
- The **市場掃描 (market) tab is a ~1000-stock universe scan** (mostly SELL/HOLD screening states, not
  calls). Default filter validates `watchlist,positions` fully + market tab **BUY-only**
  (`--market-buy-only`), else hit rate drowns in market breadth. Operational judgments live in
  watchlist/positions tabs.
- **Forward-price boundary**: prices come from the read-only `.cache/*.json` OHLCV store, which only
  extends to the last fetch date. Running `--from/--to` on very recent reports (e.g. the CLAUDE.md
  acceptance July window) yields all **PENDING/NO_PRICE_DATA**. Meaningful validation = look back far
  enough that T+10/T+20 already exist (e.g. today−2..3 weeks). June-window run gave overall hit 62%,
  BUY T+5 up-rate 64%, REDUCE走弱率 ~48% (REDUCE calls weakly predictive).
- Entry price = next-day open (fallback close); `signal_price` = signal-day close. `score` column is
  best-effort (only newer reports carry `data-score` on the row; older ones only in the detail card).

Root-cause dig (2026-07-06, `docs/SIGNAL_VALIDATION_REDUCE_FEEDBACK.md`): the "weak REDUCE"
is NOT position exits — 204/227 watchlist REDUCE_GROUP signals are `OVERHEATED;追高`
(WatchAction.TAKE_PROFIT), which is an ENTRY caution, not a bearish call. Trigger is
`rocket.go:257 extended := extFrom5>12 || ret5>25` → StageOverheated, too tight for momentum tape;
benched KY/小型股 that then ran +26% median max_runup. Real positions-tab exits hit 64% (fine).
Fixes: (A) validator split OVERHEATED into ENTRY_CAUTION_GROUP — DONE; (B) rocket OVERHEATED→PULLBACK_BUY
when trend intact, bind TAKE_PROFIT to climax not extension — DONE; (C) trailing-stop positionAdvice — not done.
A+B IMPLEMENTED 2026-07-06 (tests pass, NOT committed, awaiting user OK):
 A `signal.go GroupForSnapshot`/`isOverheatedCaution` + `evaluator.go evalEntryCaution` → REDUCE 60%→62%,
 ENTRY_CAUTION 204ct hit 76%, overall 62%→65% on June window.
 B `rocket.go overheatExit := climax || !(bullAlign&&aboveMA20)` threaded into watchActionFor/jointWatchAction
 → 2026-07-06 watchlist 130 OVERHEATED: 130 TP → 98 TP + 32 PULLBACK_BUY. NOTE PULLBACK_BUY renders as
 CSS `act-buy` (shared w/ BREAKOUT_BUY), not a distinct badge. B門檻(ret5>25) not yet loosened.

See `cmd/validate-signals/README.md`. Related: [[scanner-master-design]], [[autonomous-execution-rules]].
