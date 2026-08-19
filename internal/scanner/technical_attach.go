package scanner

import (
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/technical"
)

// ──────────────────────────────────────────────────────────────────────────────
// Technical indicator attach (R14) — shadow-only, POST-PASS.
//
// This is the thin glue between the pure technical package and a watchlist entry: it adapts
// fetcher.Candle → technical.Bar and nothing else. It runs AFTER computeRocket and after the
// decision, and its output is written to exactly one place — WatchlistEntry.Technical — so it
// is trivially checkable that no indicator here can change a Score, an Action, a RocketScore,
// a WatchAction, a sort position or a stop. That mirrors how R10-1, R10-2 and R12 are wired,
// and the R14 regression test proves it holds rather than asserting it in prose.
//
// The direction of information is one-way by construction: the technical layer reads candles,
// never the decision; the decision reads its own inputs, never this. There is deliberately no
// return value that the caller could feed back.
// ──────────────────────────────────────────────────────────────────────────────

// computeTechnical runs the R14 indicator set over an entry's candles.
//
// Only called when EnableTechnicalIndicators is on, so the analysis never runs-and-discards.
// When enabled it ALWAYS returns non-nil: each sub-indicator carries its own Status, so
// "there was not enough history" stays distinguishable from "the feature is off" (which the
// caller represents as a nil field).
func computeTechnical(candles []fetcher.Candle, cfg technical.Config) *technical.Result {
	bars := make([]technical.Bar, len(candles))
	for i, c := range candles {
		bars[i] = technical.Bar{
			Date:   c.Date,
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: float64(c.Volume),
		}
	}
	return technical.Analyze(bars, cfg)
}
