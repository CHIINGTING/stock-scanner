// Package recon reconstructs ONE historical scanner session at full fidelity, so EP-9 Phase 2
// can measure the committed EntryPlan baseline instead of an approximation of it.
//
// It is RESEARCH ONLY and changes no production semantics. Everything it calls —
// Scanner.ScanRotation, Scanner.BuildRSTable, Scanner.EnrichWatchlist, scanner.AttachEntryPlan
// — is the production function, unmodified. What this package supplies is the INPUT: the same
// candles the production path would have had, truncated at the signal session.
//
// # Why reconstruction rather than reading an archive
//
// Nothing stored can answer the question. data/analysis_history carries code/action/score/bias
// only; data/research/r13.db carries no base low, no previous close and zero ep_* rows. The
// evidence an entryplan.Snapshot needs exists nowhere but in the scanner's own output, so the
// scanner has to be re-run.
//
// It is re-runnable because internal/scanner is PURE: its production files contain no `os.`,
// no `http.`, no `sql.` and no `time.Now`. The one alignment rule that makes truncation
// mandatory rather than merely allowed is internal/scanner/entryplan_attach.go:698-707 —
// entryPlanSeriesFor refuses a series whose last bar is not the analysis session, so a plan
// computed from an over-long series would silently lose its previous close and its adjustment
// age instead of being wrong loudly.
//
// # Fidelity is not negotiable; scope is
//
// The user's ruling, transcribed: if cost forces a cut, cut SESSIONS or SYMBOLS, never
// fidelity. Sector rotation and the RS table are therefore rebuilt per session from the same
// truncated candles, because passing them empty would change analyzeConsolidation's `inflow`
// argument and therefore the plans — at which point the study would be evaluating something
// other than the committed semantics. EmptyContext exists ONLY to measure what that would
// have cost (Phase 2a deliverable 2) and must never be the arm a result is reported from.
//
// # Two regime arms, never mixed
//
// RegimeBlind passes scanner.EntryPlanMarket{Available: false}, which is exactly what
// production does when no dashboard snapshot covers the session (cmd/scanner/main.go:126).
// ReplayedPIT passes the point-in-time replayed regime read from data/market_replay. The two
// are separate arms with separate outputs and are never merged in any metric: a replay cannot
// see institutional posture (so analyzer rules R4/R5 can never fire) and its breadth is
// measured over the price cache's CURRENT membership, which is survivorship bias — measured
// at +3.91pp on breadth_above_ma20 and +8.21pp on advancing_ratio for 2026-08-31, the one
// session both archives cover (internal/market/model/regime_replay.go:49-54).
package recon
