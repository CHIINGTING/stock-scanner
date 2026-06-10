package r6backtest

import (
	"math"
	"time"
)

// R6-6 Recent Bull Regime Validation.
//
// This is NOT a cross-regime validation. It slices the SAME entries the rest of
// R6 produces into recent signal-date windows (2m / 4m / 6m, nested) and judges
// them PRIMARILY on the 20d horizon, which corresponds to the recent TW
// strong-stock rhythm: 強勢 → 處置(~10d) → 冷卻/下跌 → 出處置 → 再攻擊 (≈ 20 td).
//
// 5d/10d are early-reaction / path observation; 60d is an optional reference.
// Conclusions apply ONLY to the recent strong-bull window and must not be
// extrapolated to bear / sideways / crash regimes. It reuses the existing
// engine (collectEntries / buildTrade / ComputeStats) read-only and never
// changes engine behaviour, the Trade struct, or any default.

// PrimaryHorizon is the R6-6 main judgement horizon (trading days).
const PrimaryHorizon = 20

// RecentBullWindow is one nested recency window, by signal date (inclusive).
type RecentBullWindow struct {
	Name  string // recent_2m / recent_4m / recent_6m
	Start string // inclusive date key (YYYY-MM-DD)
	End   string // inclusive date key = axis end
}

// RecentBullCell is one (setup × stop_policy × window) result. All reported
// metrics are computed over the 20d-MATURED cohort only (signals whose 20d
// forward bar exists), so the 20d statistic is never inflated by signals that
// have not yet had time to play out.
type RecentBullCell struct {
	SetupName  string
	Bucket     int
	StopPolicy string
	Window     string

	SignalCount       int // all signals whose signal_date falls in the window
	Matured20dCount   int // signal_date <= last_available_date − 20 trading days
	Unmatured20dCount int // last 20 trading days → UNMATURED_20D (excluded from 20d)
	Available20d      int // matured signals with a usable (non-NaN) 20d return
	Status            string // OK / LOW_SAMPLE / INSUFFICIENT

	// 20d primary (matured cohort). AvgReturn20d is the stop-adjusted return
	// (MAIN); HoldReturn20d ignores stops; StopDelta20d = avg − hold.
	Win20d        float64
	AvgReturn20d  float64
	MedReturn20d  float64
	HoldReturn20d float64
	StopDelta20d  float64
	StopHitRate   float64
	RddAvg        float64
	RddP90        float64

	// Early reaction (matured cohort, observation only).
	Win5d, Avg5d, Win10d, Avg10d float64
	// Optional reference (matured cohort; often thin → Avail60d shown).
	Win60d, Avg60d float64
	Avail60d       int
}

// DefaultRecentBullWindows builds the nested 2m/4m/6m windows from the axis end.
func DefaultRecentBullWindows(axis []string) []RecentBullWindow {
	if len(axis) == 0 {
		return nil
	}
	end := axis[len(axis)-1]
	endT, err := time.Parse("2006-01-02", end)
	if err != nil {
		return nil
	}
	mk := func(name string, months int) RecentBullWindow {
		start := endT.AddDate(0, -months, 0).Format("2006-01-02")
		return RecentBullWindow{Name: name, Start: start, End: end}
	}
	return []RecentBullWindow{
		mk("recent_2m", 2),
		mk("recent_4m", 4),
		mk("recent_6m", 6),
	}
}

// maturityCutoffKey returns the date key h trading days before the axis end.
// A signal is 20d-matured iff its signal date is <= this key. Empty when the
// axis is too short.
func maturityCutoffKey(axis []string, h int) string {
	if len(axis) <= h {
		return ""
	}
	return axis[len(axis)-1-h]
}

// recentBullStatus maps available 20d-matured sample count to a status label.
func recentBullStatus(available20d int) string {
	switch {
	case available20d >= 30:
		return "OK"
	case available20d >= 12:
		return "LOW_SAMPLE"
	default:
		return "INSUFFICIENT"
	}
}

func inWindow(key string, w RecentBullWindow) bool {
	return key >= w.Start && key <= w.End
}

// RunRecentBull applies every stop policy to the SAME entries per setup, slices
// the resulting trades into the recency windows by signal date, and computes one
// RecentBullCell per (setup × policy × window). Setup D is intentionally NOT
// included (no crash regime in a bull window). Read-only; mutates nothing.
func RunRecentBull(u *Universe, rs *RSPanel, setups []Setup, policies []StopPolicy, p Params, windows []RecentBullWindow) []RecentBullCell {
	maxH := maxHorizon(p)
	cutoff := maturityCutoffKey(u.Axis, PrimaryHorizon)

	var cells []RecentBullCell
	for _, setup := range setups {
		eps := collectEntries(u, rs, setup, p)
		bucket := 0
		if len(eps) > 0 {
			bucket = eps[0].trig.Bucket
		}
		for _, pol := range policies {
			// Build trades once per (setup, policy) over all entries.
			trades := make([]Trade, 0, len(eps))
			for _, ep := range eps {
				end := ep.entryIdx + maxH
				if end > len(ep.s.Candles)-1 {
					end = len(ep.s.Candles) - 1
				}
				sr := pol.Eval(ep.s, ep.entryIdx, ep.entry, p, end)
				if t := buildTrade(setup.Name(), rs, ep, sr, maxH); t != nil {
					trades = append(trades, *t)
				}
			}
			for _, w := range windows {
				cells = append(cells, buildRecentBullCell(setup.Name(), bucket, pol.Name(), w, cutoff, trades, p))
			}
		}
	}
	return cells
}

// buildRecentBullCell slices trades to the window, splits matured / unmatured by
// the 20d cutoff, and aggregates the matured cohort.
func buildRecentBullCell(name string, bucket int, policy string, w RecentBullWindow, cutoff string, trades []Trade, p Params) RecentBullCell {
	c := RecentBullCell{SetupName: name, Bucket: bucket, StopPolicy: policy, Window: w.Name}

	var matured []Trade
	for _, t := range trades {
		sk := dateKey(t.SignalDate)
		if !inWindow(sk, w) {
			continue
		}
		c.SignalCount++
		// 20d-matured iff signal on/before the cutoff (the 20d forward bar exists)
		// AND the axis is long enough for a cutoff to exist.
		if cutoff != "" && sk <= cutoff {
			c.Matured20dCount++
			matured = append(matured, t)
			if !math.IsNaN(t.Return20d) {
				c.Available20d++
			}
		} else {
			c.Unmatured20dCount++
		}
	}
	c.Status = recentBullStatus(c.Available20d)

	if len(matured) == 0 {
		return c
	}
	st := ComputeStats(name, bucket, matured, p.Horizons, p)
	c.Win20d, c.AvgReturn20d, c.MedReturn20d = st.WinRate[20], st.AvgReturn[20], st.MedianReturn[20]
	c.HoldReturn20d, c.StopDelta20d = st.HoldAvgReturn[20], st.StopDelta[20]
	c.StopHitRate, c.RddAvg, c.RddP90 = st.StopHitRate, st.RealizedDDAvg, st.RealizedDDP90
	c.Win5d, c.Avg5d = st.WinRate[5], st.AvgReturn[5]
	c.Win10d, c.Avg10d = st.WinRate[10], st.AvgReturn[10]
	c.Win60d, c.Avg60d = st.WinRate[60], st.AvgReturn[60]
	for _, t := range matured {
		if !math.IsNaN(t.Return60d) {
			c.Avail60d++
		}
	}
	return c
}
