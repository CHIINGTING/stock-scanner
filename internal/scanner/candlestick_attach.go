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

// Validate reports configuration errors that must fail at startup. Currently: a display flag
// may not be on while its data flag is off — that would silently show nothing (or imply the
// analyzer ran when it did not). ShowCandlestick requires EnableCandlestick (SPEC R10-2 §9).
func (c Config) Validate() error {
	if c.ShowCandlestick && !c.EnableCandlestick {
		return fmt.Errorf("config: show_candlestick=true requires enable_candlestick=true")
	}
	return nil
}
