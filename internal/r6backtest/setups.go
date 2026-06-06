package r6backtest

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// Baseline signal configs MIRRORED from configs/config.yaml. These are calibration
// baselines for the backtest, NOT a change to any live config default — the live
// config file is untouched. Kept here so the backtest can call the scanner's
// exported Compute* functions as-of a historical bar.
func mfConfig() scanner.MomentumConfig {
	return scanner.MomentumConfig{
		Enable: true, MinHistoryDays: 30, AccelShortWindow: 3, AccelLongWindow: 20,
		AccelPosThresh: 0.0008, AccelNegThresh: -0.0008, AccelScale: 12000, KeyMA: 20,
		ReclaimLookback: 5, ZigzagReversal: 1.5, RSIDivLookback: 20, UseAdjustedClose: false,
		ShiftUpMinBelowDays: 2, ShiftUpConfirmDays: 2,
	}
}

func mtfConfig() scanner.MTFConfig {
	return scanner.MTFConfig{Enable: true, UseAdjustedClose: false, StrongDailyScore: 85, StrongWeeklyScore: 85}
}

// flowProvider / mtfProvider are the as-of signal sources. They are package vars
// so tests can stub them deterministically; production uses the real scanner
// Compute* functions below.
var (
	flowProvider = asofMomentumFlow
	mtfProvider  = asofMTFSignal
)

// asofMomentumFlow computes MomentumFlow as-of bar i (no look-ahead): RSI is
// causal so RSI14[:i+1] is valid; candles are truncated to i.
func asofMomentumFlow(s *Stock, i int) string {
	end := i + 1
	if end > len(s.RSI14) {
		return ""
	}
	volRatio := 0.0
	if s.VolMA20[i] > 0 {
		volRatio = s.Vol[i] / s.VolMA20[i]
	}
	ms := scanner.ComputeMomentum(s.Candles[:end], s.RSI14[:end], volRatio, mfConfig())
	return string(ms.Flow)
}

// asofMTFSignal computes the multi-timeframe SignalStrength as-of bar i.
func asofMTFSignal(s *Stock, i int) string {
	return scanner.ComputeMultiTimeframe(s.Candles[:i+1], mtfConfig()).SignalStrength
}

// strongPrequalified is the shared strong-stock filter for Setup A/B, evaluated
// strictly on bars <= i. Returns (rsPct, dist52, ok).
func strongPrequalified(rs *RSPanel, s *Stock, i int) (float64, float64, bool) {
	rsPct, ok := rs.At(s.Symbol, dateKey(s.Candles[i].Date))
	if !ok || rsPct < 70 { // cheapest gate first
		return 0, 0, false
	}
	hi52 := maxHigh(s, i-249, i)
	if hi52 <= 0 {
		return 0, 0, false
	}
	dist52 := (hi52 - s.Close[i]) / hi52 * 100
	if dist52 > 25 {
		return rsPct, dist52, false
	}
	return rsPct, dist52, true
}

// volumeDry: recent (last 3 bars) average volume below 0.9× the 20-day average.
func volumeDry(s *Stock, i int) bool {
	if i < 2 || s.VolMA20[i] <= 0 {
		return false
	}
	return (s.Vol[i-2]+s.Vol[i-1]+s.Vol[i])/3 < s.VolMA20[i]*0.9
}

// ── Setup A: MA20 / MA60 pullback ──────────────────────────────────────────

// SetupA is a strong-stock pullback to a moving average. variant ∈ {"MA20","MA60"}.
type SetupA struct{ Variant string }

func (a SetupA) Name() string { return "A_" + a.Variant + "_PULLBACK" }

func (a SetupA) Detect(_ *Universe, rs *RSPanel, s *Stock, i int, _ Params) *Trigger {
	_, _, ok := strongPrequalified(rs, s, i)
	if !ok {
		return nil
	}
	// trend still bullish: 5>10>20 stacked
	if !(s.MA5[i] > 0 && s.MA5[i] > s.MA10[i] && s.MA10[i] > s.MA20[i]) {
		return nil
	}
	if !volumeDry(s, i) {
		return nil
	}
	switch a.Variant {
	case "MA20":
		if !(s.MA20[i] > 0 && s.Low[i] <= s.MA20[i]*1.02 && s.Close[i] >= s.MA20[i]*0.98) {
			return nil
		}
		if !(s.MA60[i] > 0 && s.Close[i] > s.MA60[i]) { // support not broken
			return nil
		}
	case "MA60":
		if !(s.MA60[i] > 0 && s.Low[i] <= s.MA60[i]*1.02 && s.Close[i] >= s.MA60[i]*0.98) {
			return nil
		}
	default:
		return nil
	}
	flow := flowProvider(s, i)
	if flow == "STRUCTURAL_SHIFT_DOWN" {
		return nil
	}
	return &Trigger{
		Bucket:       0,
		PullbackPct:  pullbackFromHigh(s, i, 20),
		MomentumFlow: flow,
		MTFSignal:    mtfProvider(s, i),
	}
}

// ── Setup B: 52-week-high strong stock pullback-depth sweep ─────────────────

// SetupB enters when price has pulled back at least `Bucket`% from the recent
// high. Each bucket (5/8/10/15/20) is a separate Setup with its own cooldown, so
// it records one entry per pullback leg at the FIRST bar reaching that depth.
type SetupB struct{ Bucket int }

func (b SetupB) Name() string { return fmt.Sprintf("B_PULLBACK_%d", b.Bucket) }

func (b SetupB) Detect(_ *Universe, rs *RSPanel, s *Stock, i int, _ Params) *Trigger {
	_, _, ok := strongPrequalified(rs, s, i)
	if !ok {
		return nil
	}
	// "近 250 日內曾接近或創 52 週高": recent bars (last ~40) reached within 5% of the
	// 250-day high → the pullback is FROM a recent high, not a stale one.
	hi52 := maxHigh(s, i-249, i)
	if maxHigh(s, i-39, i) < hi52*0.95 {
		return nil
	}
	recentHigh := maxHigh(s, i-19, i)
	if recentHigh <= 0 {
		return nil
	}
	pb := (recentHigh - s.Close[i]) / recentHigh * 100
	if pb < float64(b.Bucket) { // cumulative: first time the leg reaches this depth
		return nil
	}
	flow := flowProvider(s, i)
	if flow == "STRUCTURAL_SHIFT_DOWN" {
		return nil
	}
	return &Trigger{
		Bucket:       b.Bucket,
		PullbackPct:  pb,
		MomentumFlow: flow,
		MTFSignal:    mtfProvider(s, i),
	}
}

// pullbackFromHigh returns the % pullback of the close from the high over the
// trailing `look` bars (0 if undefined).
func pullbackFromHigh(s *Stock, i, look int) float64 {
	h := maxHigh(s, i-look+1, i)
	if h <= 0 {
		return 0
	}
	return (h - s.Close[i]) / h * 100
}

// SetupAVariants returns the two Setup A variants.
func SetupAVariants() []Setup { return []Setup{SetupA{Variant: "MA20"}, SetupA{Variant: "MA60"}} }

// SetupBBuckets returns the Setup B pullback-depth sweep.
func SetupBBuckets() []Setup {
	var out []Setup
	for _, b := range []int{5, 8, 10, 15, 20} {
		out = append(out, SetupB{Bucket: b})
	}
	return out
}
