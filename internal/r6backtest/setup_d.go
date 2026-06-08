package r6backtest

import (
	"time"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// mtfWeeklyProvider is the as-of weekly-trend source (package var for test stubs).
var mtfWeeklyProvider = asofMTFWeekly

// asofMTFWeekly returns the weekly TrendState as-of bar i (UPTREND/DOWNTREND/RANGE…).
func asofMTFWeekly(s *Stock, i int) string {
	return scanner.ComputeMultiTimeframe(s.Candles[:i+1], mtfConfig()).Weekly.TrendState
}

// SetupD is the crash-regime "survivor" study. Entries are only taken on regime
// days (0050 20d ≤ threshold). It is a CASE STUDY — always LOW confidence.
//
//	Relaxed=false (main): RS≥70 + relative_return_vs_market_20d ≥ RelThreshold +
//	                      weekly≠DOWNTREND + flow≠SHIFT_DOWN + near-MA20 + vol dry.
//	Relaxed=true (cohort): only regime + near-MA20 + flow≠SHIFT_DOWN; RS recorded
//	                       but NOT gated (so HIGH_RS vs LOW_RS can be compared).
type SetupD struct {
	Regime       *RegimePanel
	RelThreshold float64 // pp; main mode only
	Relaxed      bool
}

func (d SetupD) Name() string {
	if d.Relaxed {
		return "D_CRASH_COHORT"
	}
	return "D_CRASH_SURVIVOR"
}

func (d SetupD) Detect(_ *Universe, rs *RSPanel, s *Stock, i int, _ Params) *Trigger {
	if i < 20 {
		return nil
	}
	dk := dateKey(s.Candles[i].Date)
	rd, ok := d.Regime.At(dk)
	if !ok || !rd.InRegime {
		return nil
	}
	rsPct, _ := rs.At(s.Symbol, dk)
	if s.Close[i-20] <= 0 {
		return nil
	}
	stockRet20 := (s.Close[i]/s.Close[i-20] - 1) * 100
	rel := stockRet20 - rd.ProxyReturn20

	// near-MA20 support (shared), flow gate (shared).
	if !(s.MA20[i] > 0 && s.Low[i] <= s.MA20[i]*1.03) {
		return nil
	}
	flow := flowProvider(s, i)
	if flow == "STRUCTURAL_SHIFT_DOWN" {
		return nil
	}

	if !d.Relaxed {
		if rsPct < 70 {
			return nil
		}
		if rel < d.RelThreshold {
			return nil
		}
		if mtfWeeklyProvider(s, i) == "DOWNTREND" {
			return nil
		}
		if !volumeDry(s, i) {
			return nil
		}
	}

	return &Trigger{
		Bucket:       0,
		PullbackPct:  pullbackFromHigh(s, i, 20),
		MomentumFlow: flow,
		MTFSignal:    mtfProvider(s, i),
		Crash: &CrashContext{
			ProxySymbol:          ProxySymbol,
			MarketProxyReturn20d: rd.ProxyReturn20,
			StockReturn20d:       stockRet20,
			RelativeReturn20d:    rel,
			BreadthBelowMA20Pct:  rd.BreadthBelow,
			RegimeEventID:        rd.EventID,
			RegimeStart:          rd.EventStart,
			RegimeEnd:            rd.EventEnd,
		},
	}
}

// RunCrashCohorts runs the relaxed crash candidate and splits the resulting trades
// into HIGH_RS (≥70) and LOW_RS (<70) cohorts to test whether high-RS names are
// more resilient in a crash. Both cohorts are forced to LOW confidence.
func RunCrashCohorts(u *Universe, rs *RSPanel, regime *RegimePanel, p Params) (high, low CrashCohort) {
	pp := p
	pp.ForceLowConfidence = true
	trades := RunSetup(u, rs, SetupD{Regime: regime, Relaxed: true}, pp)
	var hi, lo []Trade
	for _, t := range trades {
		if t.RSRankAtEntry >= 70 {
			hi = append(hi, t)
		} else {
			lo = append(lo, t)
		}
	}
	high.Stat = ComputeStats("D_CRASH_COHORT", 0, hi, p.Horizons, pp)
	high.Stat.Subgroup = "HIGH_RS"
	high.AvgRel = AvgRelativeReturn(hi)
	low.Stat = ComputeStats("D_CRASH_COHORT", 0, lo, p.Horizons, pp)
	low.Stat.Subgroup = "LOW_RS"
	low.AvgRel = AvgRelativeReturn(lo)
	return high, low
}

// DistinctCrashEvents returns the number of distinct regime events that actually
// produced trades (faithful event_count — pre-warmup events that yielded no
// entries are NOT counted) and the spanning date range of those events.
func DistinctCrashEvents(trades []Trade) (count int, dateRange string) {
	seen := map[int]bool{}
	var minStart, maxEnd time.Time
	for _, t := range trades {
		if t.Crash == nil || t.Crash.RegimeEventID == 0 {
			continue
		}
		if !seen[t.Crash.RegimeEventID] {
			seen[t.Crash.RegimeEventID] = true
			if minStart.IsZero() || t.Crash.RegimeStart.Before(minStart) {
				minStart = t.Crash.RegimeStart
			}
			if t.Crash.RegimeEnd.After(maxEnd) {
				maxEnd = t.Crash.RegimeEnd
			}
		}
	}
	count = len(seen)
	if count > 0 {
		dateRange = dateKey(minStart) + "~" + dateKey(maxEnd)
	}
	return count, dateRange
}

// AvgProxyReturn returns the mean market_proxy_return_20d over trades with crash
// context (0 when none).
func AvgProxyReturn(trades []Trade) float64 {
	var sum float64
	var n int
	for _, t := range trades {
		if t.Crash != nil {
			sum += t.Crash.MarketProxyReturn20d
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// AvgRelativeReturn returns the mean relative_return_vs_market_20d over trades
// that carry crash context (NaN-safe; 0 when none).
func AvgRelativeReturn(trades []Trade) float64 {
	var sum float64
	var n int
	for _, t := range trades {
		if t.Crash != nil {
			sum += t.Crash.RelativeReturn20d
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
