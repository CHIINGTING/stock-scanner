// Package entryplanbacktest is the EP-9 RESEARCH package: the temporal contract an EntryPlan
// outcome study must obey, codified as types, helpers and tests rather than as prose.
//
// It is RESEARCH ONLY. It changes no production semantics, it is imported by no production
// path, and it is not a strategy. EP-9 exists to find out whether the committed EntryPlan
// baseline can be measured at all; a package that could alter the thing being measured would
// have destroyed the measurement before it started.
//
// # What Phase 1 is, and what it deliberately is NOT
//
// Phase 1 is the CONTRACT. Fills, metrics and the study driver are Phase 2 and are absent on
// purpose: every convention below — when a plan may first execute, what Return@5 counts, what
// window MFE and MAE are measured over, what "the stop and the target were both touched"
// means, what makes two observations the same observation — has to be fixed BEFORE any
// number exists, because each of them is a knob that silently improves results when it is
// chosen after the fact.
//
// This package therefore computes NO return, NO excursion value, NO fill price and NO
// summary statistic. It resolves INDICES and CLASSIFIES situations. Phase 2 does the
// arithmetic, and it does it inside the windows this file already fixed.
//
// # The temporal contract, in four lines
//
//	Signal session T            = a COMPLETED trading session T
//	Plan information set        = evidence available no later than T
//	Earliest executable session = the next valid trading session after T (T+1 session)
//	NO same-bar fill on T, even if T.Low <= Zone.High
//
// The fourth line is the one that needs the machine. A daily bar's Low is printed after the
// close; a plan computed FROM that close cannot have been filled at it. "T.Low touched the
// zone" is the single most attractive way to manufacture an edge that does not exist, and
// ExecutionWindow is where it is refused rather than promised.
//
// # SESSIONS, NOT CALENDAR DAYS — and where they come from
//
// This package does not own a Taiwan holiday calendar and must never grow one. The repo
// already has ONE answer to "did this day trade", and it is the data:
//
//	internal/dailydata/session.go:113 ConfirmSession — "The calendar says a Tuesday is a
//	trading day; only the data says whether it traded."
//
// and the concrete axis that answer produces is r6backtest.LoadUniverse
// (internal/r6backtest/engine.go:37), which builds Universe.Axis as the sorted unique set of
// dates on which bars actually exist, plus a per-stock date→bar-index map behind
// Stock.IndexOf (internal/r6backtest/types.go:54). cmd/foreignflow-backfill/main.go:130
// already uses exactly that as "the universe date axis from the price cache", so reusing it
// here adds no second definition of a trading day to the repo.
//
// A second, approximate calendar is the failure this note exists to prevent: it would
// disagree with the bars on holidays and on unscheduled closures, and the disagreement would
// be silent. Where session identity cannot be reconstructed from the bars, this package
// answers UNAVAILABLE and never a guess — see Eligibility.
//
// # Its own scope boundary
//
// No filesystem, no network, no clock, no database. AxisFromUniverse takes an ALREADY LOADED
// *r6backtest.Universe; the loading (and the read-only discipline that goes with it) belongs
// to whatever drives Phase 2.
package entryplanbacktest
