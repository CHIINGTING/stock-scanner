package r6backtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeStocks builds a small universe that RISES to a high then DROPS ~8% and
// plateaus — so Setup B (pullback from recent high) actually triggers entries.
func makeStocks(n int) *Universe {
	u := &Universe{bySym: map[string]*Stock{}}
	set := map[string]struct{}{}
	for k := 0; k < n; k++ {
		closes := make([]float64, 320)
		base := 50.0 + float64(k)
		var high float64
		for i := 0; i <= 250; i++ {
			closes[i] = base * (1 + 0.003*float64(i))
			high = closes[i]
		}
		for i := 251; i < 320; i++ {
			closes[i] = high * 0.92 // ~8% pullback plateau → Setup B 5/8 fire
		}
		s := withATR(withRSI(mkStock("X"+string(rune('A'+k)), closes)))
		u.Stocks = append(u.Stocks, s)
		u.bySym[s.Symbol] = s
		for d := range s.idxOf {
			set[d] = struct{}{}
		}
	}
	for d := range set {
		u.Axis = append(u.Axis, d)
	}
	return u
}

// 8. Benchmark reuses identical entries across policies (hold returns invariant)
//    and produces one stat row per (setup, policy).
func TestBenchmarkReusesEntries(t *testing.T) {
	u := makeStocks(6)
	rs := func() *RSPanel { // give everyone RS 85 every date
		m := map[string]map[string]float64{}
		for _, s := range u.Stocks {
			for d := range s.idxOf {
				if m[d] == nil {
					m[d] = map[string]float64{}
				}
				m[d][s.Symbol] = 85
			}
		}
		return &RSPanel{byDate: m}
	}()
	p := DefaultParams()
	p.Warmup = 120
	setups := []Setup{SetupB{Bucket: 5}}
	policies := BenchmarkStopPolicies()

	stats := RunStopBenchmark(u, rs, setups, policies, p)
	if len(stats) != len(policies) {
		t.Fatalf("want %d stat rows (1 setup × policies), got %d", len(policies), len(stats))
	}
	// sample_count must be identical across all policies (same entries).
	n0 := stats[0].SampleCount
	for _, s := range stats {
		if s.SampleCount != n0 {
			t.Errorf("policy %s sample_count %d != %d (entries must be shared)", s.StopPolicy, s.SampleCount, n0)
		}
		// hold-to-horizon stats are stop-independent → identical across policies.
		if !sameF(s.HoldAvgReturn[20], stats[0].HoldAvgReturn[20]) {
			t.Errorf("policy %s hold avg differs: %v vs %v", s.StopPolicy, s.HoldAvgReturn[20], stats[0].HoldAvgReturn[20])
		}
	}
	if n0 == 0 {
		t.Fatalf("fixture produced 0 entries — benchmark assertions are vacuous")
	}
	// NO_STOP: stop-adjusted avg == hold avg (delta ~ 0).
	for _, s := range stats {
		if s.StopPolicy == "NO_STOP" {
			if !sameF(s.AvgReturn[20], s.HoldAvgReturn[20]) {
				t.Errorf("NO_STOP stop-adjusted avg must equal hold avg")
			}
		}
	}
}

// 9. Benchmark CSV schema is fixed.
func TestBenchmarkCSVSchema(t *testing.T) {
	want := []string{
		"setup_name", "pullback_bucket", "stop_policy", "sample_count",
		"win_rate_5d", "win_rate_10d", "win_rate_20d", "win_rate_60d",
		"avg_return_5d", "avg_return_10d", "avg_return_20d", "avg_return_60d",
		"median_return_20d", "max_drawdown_avg", "max_drawdown_p90", "stop_hit_rate",
		"avg_hold_return_20d", "stop_saved_or_hurt_delta_20d", "worst_cases",
	}
	got := BenchmarkCSVHeader()
	if len(got) != len(want) {
		t.Fatalf("header len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("header[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

// 10. No forbidden order/trade tokens in benchmark CSV or markdown output.
func TestBenchmarkNoForbiddenTokens(t *testing.T) {
	u := makeStocks(4)
	rs := emptyPanel()
	stats := RunStopBenchmark(u, rs, []Setup{SetupA{Variant: "MA20"}}, BenchmarkStopPolicies(), DefaultParams())
	dir := t.TempDir()
	csv := filepath.Join(dir, "b.csv")
	md := filepath.Join(dir, "b.md")
	if err := WriteBenchmarkCSV(csv, stats); err != nil {
		t.Fatal(err)
	}
	if err := WriteBenchmarkMarkdown(md, "R6-3 Stop Policy Benchmark", []string{"universe: 4"}, stats); err != nil {
		t.Fatal(err)
	}
	for _, fp := range []string{csv, md} {
		b, _ := os.ReadFile(fp)
		up := strings.ToUpper(string(b))
		for _, tok := range forbiddenTokens {
			if strings.Contains(up, tok) {
				t.Errorf("%s contains forbidden token %q", filepath.Base(fp), tok)
			}
		}
	}
}
