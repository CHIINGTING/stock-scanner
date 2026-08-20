package scanner

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/candlestick"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────
// Candlestick attach (R10-2) — display/shadow-only, POST-PASS. This is the thin scanner
// glue between the pure candlestick analyzer and the watchlist entry: it adapts
// fetcher.Candle → candlestick.Candle and injects the already-decided RocketStage as the
// analyzer's Stage. It runs AFTER computeRocket + the decision and never feeds back — so it
// is trivially provable that candlestick data changes no Score / Action / RocketScore /
// WatchAction / sort / stop (mirrors the R10-1 shadow layers). SPEC R10-2 §11, §1(attach).
// ──────────────────────────────────────────────────────────────────────────────

// computeCandlestick runs the analyzer over an entry's candles with the injected stage.
// Only called when EnableCandlestick is on, so the (potentially heavy) analyzer never runs
// otherwise. Returns a non-nil Result whose Status distinguishes NO_PATTERN / INVALID_BAR /
// NO_DATA from "disabled" (which the caller represents as a nil field).
func computeCandlestick(candles []fetcher.Candle, stage RocketStage) *candlestick.Result {
	cs := make([]candlestick.Candle, len(candles))
	for i, c := range candles {
		cs[i] = candlestick.Candle{
			Date:   c.Date,
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: c.Volume,
		}
	}
	r := candlestick.Analyze(cs, candlestick.Stage(string(stage)), candlestick.DefaultThresholds())
	return &r
}

// Validate reports configuration errors that must fail at startup.
//
// It is the CONFIG-WIDE validator despite living in this file — it started with the R10-2
// rule and each feature that needs a startup check adds its own here rather than growing a
// second entry point that a caller could forget.
//
// The rule every entry shares: a display flag may not be on while its data flag is off. That
// combination renders a section for data nobody computed — an empty shell that reads as
// "there was nothing to show" rather than "nobody looked". The reverse (compute without
// display) is legitimate and deliberately allowed: R14 in particular is meant to be run that
// way, persisting evidence for later validation while the report stays unchanged.
//
//	ShowCandlestick         requires EnableCandlestick         (SPEC R10-2 §9)
//	ShowTechnicalIndicators requires EnableTechnicalIndicators (R14 §3)
func (c Config) Validate() error {
	if c.ShowCandlestick && !c.EnableCandlestick {
		return fmt.Errorf("config: show_candlestick=true requires enable_candlestick=true")
	}
	if c.ShowTechnicalIndicators && !c.EnableTechnicalIndicators {
		return fmt.Errorf("config: show_technical_indicators=true requires enable_technical_indicators=true")
	}
	return nil
}
