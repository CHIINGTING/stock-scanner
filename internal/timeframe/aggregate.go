// Package timeframe aggregates daily OHLCV bars into higher timeframes (weekly).
// It is built purely from daily data — no intraday / tick / level-2 input — so the
// scanner keeps its EOD positioning. R4-1 provides weekly aggregation only; the
// multi-timeframe scoring/views live in the scanner package (R4-2+).
package timeframe

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// taipei is the fixed UTC+8 zone used to bucket bars into Taiwan trading weeks, so
// week boundaries don't drift with the UTC timestamps stored on each Candle.
var taipei = time.FixedZone("CST", 8*3600)

// WeeklyBar is one weekly OHLCV bar. It embeds the existing fetcher.Candle (so it is
// directly usable wherever a Candle is expected, and WeeklyCandles can strip it back)
// plus the week metadata.
//
//   - Date / AdjClose come from the week's LAST trading day.
//   - Open comes from the week's FIRST trading day; High/Low are the week extremes;
//     Volume is the in-week sum.
//   - ISOYear + ISOWeek together are the unique week key (handles year boundaries:
//     e.g. 2025-12-29..2026-01-02 all fall in ISO 2026-W1).
//   - Days is how many daily bars were aggregated (may be <5 on holiday-short weeks).
//   - Partial marks an unfinished week — see ToWeekly for the (data-only) heuristic.
type WeeklyBar struct {
	fetcher.Candle
	ISOYear int
	ISOWeek int
	Days    int
	Partial bool
}

// ToWeekly aggregates oldest-first daily candles into oldest-first weekly bars,
// grouping by ISO (year, week) in Taipei time.
//
// Contract:
//   - input MUST be oldest-first (as fetcher returns it); output is oldest-first.
//   - Open = first day's Open; Close/AdjClose/Date = last day's; High/Low = extremes;
//     Volume = sum; ISOYear+ISOWeek = unique week key.
//
// Partial heuristic: ToWeekly has no concept of "now" or the exchange calendar, so it
// can only infer an unfinished week from the INPUT's last bar. Only the LAST weekly
// bar may be Partial, and only when its last trading day's weekday is Monday–Thursday
// (i.e. the week has not reached Friday in the data). This does NOT reflect the
// exchange's real week completeness; a week whose Friday was a holiday can be flagged
// Partial. Interior weeks are never Partial even when Days < 5 — use Days for that.
func ToWeekly(daily []fetcher.Candle) []WeeklyBar {
	var out []WeeklyBar
	var curY, curW int
	have := false

	for _, d := range daily {
		y, w := d.Date.In(taipei).ISOWeek()
		if !have || y != curY || w != curW {
			out = append(out, WeeklyBar{
				Candle:  d, // first day seeds Open/High/Low/Close/Volume/AdjClose/Date
				ISOYear: y,
				ISOWeek: w,
				Days:    1,
			})
			curY, curW = y, w
			have = true
			continue
		}
		b := &out[len(out)-1]
		if d.High > b.High {
			b.High = d.High
		}
		if d.Low < b.Low {
			b.Low = d.Low
		}
		b.Close = d.Close
		b.AdjClose = d.AdjClose
		b.Volume += d.Volume
		b.Date = d.Date
		b.Days++
	}

	if len(out) > 0 {
		last := &out[len(out)-1]
		if wd := last.Date.In(taipei).Weekday(); wd >= time.Monday && wd <= time.Thursday {
			last.Partial = true
		}
	}
	return out
}

// WeeklyCandles strips weekly bars back to plain candles for the indicator layer.
func WeeklyCandles(ws []WeeklyBar) []fetcher.Candle {
	out := make([]fetcher.Candle, len(ws))
	for i, w := range ws {
		out[i] = w.Candle
	}
	return out
}
