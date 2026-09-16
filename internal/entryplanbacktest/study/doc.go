// Package study is EP-9 Phase 2b: the outcome measurement itself.
//
// It computes fills, forward outcomes, excursions, level hits and metrics for plans the
// committed EntryPlan baseline actually produced, under the temporal contract
// internal/entryplanbacktest fixed BEFORE any of these numbers existed.
//
// # The sentence every result in here has to be read with
//
// EVERY METRIC IS MEASURED UNDER A REPLAYED REGIME, NOT UNDER PRODUCTION'S OWN REGIME.
//
// EP-9's conclusions therefore hold UNDER REPLAYED REGIME. They are not a validation of
// production behaviour, because production reads a live market snapshot and a replay differs
// from one in two measured ways that are carried on every output row:
//
//   - POSTURE IS ALWAYS UNKNOWN. Nothing reconstructs institutional posture retroactively, so
//     analyzer rules R4 and R5 can never fire in a replay
//     (internal/market/analyzer/regime_replay.go:20-23).
//   - BREADTH IS SURVIVORSHIP-BIASED. It is measured over the price cache's CURRENT
//     membership, not the symbols listed on the replayed session. On 2026-08-31, the one
//     session both archives cover: breadth_above_ma20 46.65 live vs 50.56 replayed (+3.91pp),
//     advancing_ratio 22.39 vs 30.60 (+8.21pp), while all ten price metrics matched exactly
//     (internal/market/model/regime_replay.go:49-54).
//
// The regime-blind arm is a COVERAGE STATEMENT ONLY — "what the committed baseline does on a
// session with no market snapshot", which is production's real behaviour today — and no
// metric is computed on it. Phase 2a measured that arm at 0 executable plans in 798,458, so
// there is nothing there to average anyway.
//
// # HEURISTIC — NOT BACKTEST-FITTED
//
// The same label entryplan.RuleVersion carries. Nothing in this package feeds back into a
// threshold: the 0.5-ATR zone half-width, the chase ATR multipliers, the 2R/3.5R targets, the
// BaseQualityScore >= 60 gate, the regime permission table and the status precedence are all
// inputs here and none is tuned by anything measured here. Anything the results suggest is a
// CANDIDATE_FOLLOWUP with evidence attached, never an EP-9 change.
//
// A positive historical mean return is NOT "validated profitable". These are historical
// measurements of a heuristic rule set over one cache of one market over about eighteen
// months, with the biases above, under one reconstruction of the universe.
//
// # What is fixed here and must not move after results are seen
//
// Nothing. Every convention this package needs was fixed in internal/entryplanbacktest before
// Phase 2 began — the T+1 rule, the horizon counting, the 20-session excursion window, the
// same-bar ambiguity class and the observation identity — and this package only applies them.
// The one thing it adds is the fill model, which is declared in fill.go and pinned by test
// before any metric reads it.
package study
