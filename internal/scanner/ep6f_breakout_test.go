package scanner

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/indicator"
)

// TestEP6FCurrentBreakoutIsUnreachable pins the three mutable premises behind
// the production-path proof: consolidation includes the signal bar's High,
// a legal OHLC bar has Close <= High, and computeRocket requires Close > PivotHigh.
// The last premise is exercised through computeRocket itself, not a copied predicate.
func TestEP6FCurrentBreakoutIsUnreachable(t *testing.T) {
	for _, closeAtHigh := range []bool{false, true} {
		candles := ep6fCandles(closeAtHigh)
		ind := indicator.Calculate(candles, indicator.Config{})
		consol := analyzeConsolidation(candles, ind, false)
		last := candles[len(candles)-1]
		if last.Close > last.High {
			t.Fatalf("fixture is not a legal OHLC bar: close %.2f > high %.2f", last.Close, last.High)
		}
		if consol.PivotHigh < last.High {
			t.Fatalf("PivotHigh %.2f excludes current High %.2f", consol.PivotHigh, last.High)
		}
		out := computeRocket(rocketInput{candles: candles, ind: ind, consol: consol})
		if out.Stage == StageBreakoutStart || out.WatchAction == ActBreakoutBuy {
			t.Fatalf("current production path reached breakout: %+v", out)
		}
	}
}

// TestEP6FPreviousWindowHighIsPointInTimeSafe pins the candidate's temporal
// boundary. Appending future bars cannot change a decision already evaluated at T.
func TestEP6FPreviousWindowHighIsPointInTimeSafe(t *testing.T) {
	atT := ep6fCandles(true)
	ref := ep6fPreviousWindowHigh(atT, len(atT)-1)
	first := ep6fCandidate(atT)
	future := append(append([]fetcher.Candle(nil), atT...), fetcher.Candle{
		Date: atT[len(atT)-1].Date.AddDate(0, 0, 1), Open: 500, High: 550, Low: 490,
		Close: 540, Volume: 9_000_000,
	})
	if got := ep6fPreviousWindowHigh(future[:len(atT)], len(atT)-1); got != ref {
		t.Fatalf("future append changed reference at T: %.2f -> %.2f", ref, got)
	}
	if got := ep6fCandidate(future[:len(atT)]); got.Stage != first.Stage || got.WatchAction != first.WatchAction || got.BreakoutPrice != first.BreakoutPrice {
		t.Fatalf("future append changed decision at T: %+v -> %+v", first, got)
	}
}

func ep6fCandles(closeAtHigh bool) []fetcher.Candle {
	c := make([]fetcher.Candle, 80)
	for i := range c {
		p := 90.0 + float64(i)*0.10
		c[i] = fetcher.Candle{Open: p, High: p + 1, Low: p - 1, Close: p + .2, Volume: 1_000_000}
		c[i].Date = ep6fEpoch.AddDate(0, 0, i)
		c[i].AdjClose = c[i].Close
	}
	last := &c[len(c)-1]
	last.Open, last.High, last.Low, last.Volume = 99, 105, 98, 3_000_000
	last.Close = 104
	if closeAtHigh {
		last.Close = last.High
	}
	last.AdjClose = last.Close
	return c
}

var ep6fEpoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

type ep6fCache struct {
	Data fetcher.StockData `json:"data"`
}

type ep6fSignal struct {
	Symbol, Date, ExistingAction, ExistingWatchAction string
	PlanStatus                                        string
	Ret5, Ret10, Ret20, MFE, MAE                      float64
	FalseBreakout                                     bool
	VolumeConfirmed                                   bool
	Stage                                             RocketStage
	DistancePct                                       float64
	ExecutablePlan                                    bool
}

func ep6fPreviousWindowHigh(c []fetcher.Candle, signal int) float64 {
	start := signal - 60
	if start < 0 {
		start = 0
	}
	hi := 0.0
	for i := start; i < signal; i++ {
		if c[i].High > hi {
			hi = c[i].High
		}
	}
	return hi
}

func ep6fCandidate(c []fetcher.Candle) rocketOutput {
	ind := indicator.Calculate(c, indicator.Config{})
	consol := analyzeConsolidation(c, ind, false)
	consol.PivotHigh = ep6fPreviousWindowHigh(c, len(c)-1)
	return computeRocket(rocketInput{candles: c, ind: ind, consol: consol})
}

// TestEP6FHistoricalStudy is deterministic and uses only the repository's existing
// cache. It logs the observational sample used in docs/EP6F_BREAKOUT_REACHABILITY.md.
func TestEP6FHistoricalStudy(t *testing.T) {
	if os.Getenv("EP6F_STUDY") == "" {
		t.Skip("set EP6F_STUDY=1 for the repository-cache study")
	}
	paths, err := filepath.Glob("../../.cache/*.json")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	current, evaluated, invalidBars := 0, 0, 0
	var currentObservations []string
	var signals []ep6fSignal
	s := New(Config{})
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cached ep6fCache
		if json.Unmarshal(b, &cached) != nil || len(cached.Data.Candles) < 81 {
			continue
		}
		c := cached.Data.Candles
		for i := 60; i+20 < len(c); i++ {
			prefix := c[:i+1]
			if c[i].High < math.Max(c[i].Open, c[i].Close) || c[i].Low > math.Min(c[i].Open, c[i].Close) || c[i].Low > c[i].High {
				invalidBars++
				continue
			}
			ind := indicator.Calculate(prefix, indicator.Config{})
			consol := analyzeConsolidation(prefix, ind, false)
			base := computeRocket(rocketInput{candles: prefix, ind: ind, consol: consol})
			evaluated++
			if base.WatchAction == ActBreakoutBuy {
				current++
				currentObservations = append(currentObservations, cached.Data.Symbol+"@"+c[i].Date.Format("2006-01-02"))
			}
			ref := ep6fPreviousWindowHigh(prefix, i)
			consol.PivotHigh = ref
			candidate := computeRocket(rocketInput{candles: prefix, ind: ind, consol: consol})
			if candidate.WatchAction != ActBreakoutBuy {
				continue
			}
			a := s.analyze(fetcher.StockData{Symbol: cached.Data.Symbol, Candles: prefix}, ind)
			sig := ep6fSignal{Symbol: cached.Data.Symbol, Date: c[i].Date.Format("2006-01-02"), ExistingAction: string(a.Action), ExistingWatchAction: string(base.WatchAction), Stage: candidate.Stage, VolumeConfirmed: volRatioAt(prefix, ind, i) >= 1.3, DistancePct: (c[i].Close/ref - 1) * 100}
			entries := []WatchlistEntry{{A: a, Consol: consol, WatchAction: candidate.WatchAction}}
			AttachEntryPlan(entries, map[string][]fetcher.Candle{cached.Data.Symbol: prefix}, EntryPlanMarket{}, true)
			sig.PlanStatus = string(entries[0].EntryPlan.Status)
			sig.ExecutablePlan = entries[0].EntryPlan.HasExecutablePrices()
			sig.Ret5 = (c[i+5].Close/c[i].Close - 1) * 100
			sig.Ret10 = (c[i+10].Close/c[i].Close - 1) * 100
			sig.Ret20 = (c[i+20].Close/c[i].Close - 1) * 100
			hi, lo := c[i].Close, c[i].Close
			for j := i + 1; j <= i+20; j++ {
				hi = math.Max(hi, c[j].High)
				lo = math.Min(lo, c[j].Low)
			}
			sig.MFE = (hi/c[i].Close - 1) * 100
			sig.MAE = (lo/c[i].Close - 1) * 100
			// EP-6F research definition: within five sessions, a close below the breakout reference.
			for j := i + 1; j <= i+5; j++ {
				if c[j].Close < ref {
					sig.FalseBreakout = true
					break
				}
			}
			signals = append(signals, sig)
		}
	}
	t.Logf("evaluated=%d invalid_signal_bars_excluded=%d cache_files=%d current=%d current_observations=%v candidate=%d", evaluated, invalidBars, len(paths), current, currentObservations, len(signals))
	ep6fLogStudy(t, signals)
	if current != 0 {
		t.Fatalf("current BREAKOUT_BUY count=%d, want 0", current)
	}
}

func ep6fLogStudy(t *testing.T, s []ep6fSignal) {
	symbols, dates, actions, watchActions, plans := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	var r5, r10, r20, mfe, mae float64
	falseN, volN, executableN := 0, 0, 0
	distanceBuckets := map[string]int{}
	for _, x := range s {
		symbols[x.Symbol]++
		dates[x.Date]++
		actions[x.ExistingAction]++
		watchActions[x.ExistingWatchAction]++
		plans[x.PlanStatus]++
		r5 += x.Ret5
		r10 += x.Ret10
		r20 += x.Ret20
		mfe += x.MFE
		mae += x.MAE
		if x.FalseBreakout {
			falseN++
		}
		if x.VolumeConfirmed {
			volN++
		}
		if x.ExecutablePlan {
			executableN++
		}
		switch {
		case x.DistancePct < 1:
			distanceBuckets["0-<1%"]++
		case x.DistancePct < 3:
			distanceBuckets["1-<3%"]++
		default:
			distanceBuckets[">=3%"]++
		}
	}
	n := float64(len(s))
	if n == 0 {
		n = 1
	}
	t.Logf("symbols=%d dates=%d volume_confirmed=%d false_breakout_5d=%d", len(symbols), len(dates), volN, falseN)
	t.Logf("mean_ret_5=%.4f mean_ret_10=%.4f mean_ret_20=%.4f mean_mfe20=%.4f mean_mae20=%.4f", r5/n, r10/n, r20/n, mfe/n, mae/n)
	t.Logf("existing_action_overlap=%v existing_watch_action_overlap=%v", actions, watchActions)
	t.Logf("entryplan_market_regime_unavailable=%v executable_plans=%d", plans, executableN)
	minPerSymbol, maxPerSymbol := len(s), 0
	for _, count := range symbols {
		if count < minPerSymbol {
			minPerSymbol = count
		}
		if count > maxPerSymbol {
			maxPerSymbol = count
		}
	}
	t.Logf("signals_per_symbol: unique=%d total=%d min=%d max=%d distance_buckets=%v", len(symbols), len(s), minPerSymbol, maxPerSymbol, distanceBuckets)
}
