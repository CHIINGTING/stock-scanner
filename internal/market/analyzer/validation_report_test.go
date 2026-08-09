package analyzer

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Historical validation report. Run with:
//
//	go test ./internal/market/analyzer -run TestValidationReport -v
//
// This is VALIDATION, not optimisation. Forward returns are reported so the classification
// can be sanity-checked against what the market subsequently did; they are never fed back
// into a threshold. Classification at date i uses bars[:i+1] only — no look-ahead.
//
// Scope note: the replay is price+breadth only. Institutional evidence needs an archive that
// does not exist yet for past dates (the TAIFEX daily feed has no date parameter), so posture
// is UNKNOWN throughout and rules R4/R5 cannot fire. That is stated in the output rather than
// hidden, because it changes how the rule-usage table should be read.
func TestValidationReport(t *testing.T) {
	if os.Getenv("MARKET_VALIDATION") == "" {
		t.Skip("set MARKET_VALIDATION=1 to produce the historical validation report")
	}
	bars, panel := loadHistory(t)

	type day struct {
		date  string
		close float64
		d     model.RegimeDecision
	}
	var days []day
	for i := range bars {
		if i+1 < th.MinBenchmarkBars {
			continue
		}
		_, pv := AnalyzePrice(model.Benchmark0050, bars[:i+1], th)
		bi := indexOfDate(panel.Dates, bars[i].Date)
		if bi < 0 {
			continue
		}
		nh := pv.DDHigh <= th.NearHighDDPct
		_, bv := AnalyzeBreadth(panel, bi, nh, th)
		v := BuildStructureView(pv, bv, model.PostureUnknown, th)
		days = append(days, day{bars[i].Date, bars[i].Close, DecideRegime(v, th)})
	}
	n := len(days)
	fmt.Printf("\n════ Market Dashboard 歷史驗證 ════\n")
	fmt.Printf("benchmark 0050，%d 個已分類交易日（%s → %s）\n", n, days[0].date, days[n-1].date)
	fmt.Printf("posture 全程 UNKNOWN（法人封存尚未建立）→ R4/R5 結構上無法觸發\n\n")

	// ── regime distribution ──
	order := []model.Regime{model.RegimeBull, model.RegimeBullPullback, model.RegimeSideways,
		model.RegimeDistribution, model.RegimeBear, model.RegimeUnknown}
	cnt := map[model.Regime]int{}
	for _, d := range days {
		cnt[d.d.Regime]++
	}
	fmt.Println("── Regime 分布 ──")
	for _, r := range order {
		fmt.Printf("  %-14s %4d  %5.1f%%\n", r, cnt[r], float64(cnt[r])/float64(n)*100)
	}

	// ── transitions ──
	trans := map[string]int{}
	var changes, oneDay int
	for i := 1; i < n; i++ {
		if days[i].d.Regime == days[i-1].d.Regime {
			continue
		}
		changes++
		trans[fmt.Sprintf("%s → %s", days[i-1].d.Regime, days[i].d.Regime)]++
		if i+1 < n && days[i+1].d.Regime == days[i-1].d.Regime {
			oneDay++
		}
	}
	fmt.Printf("\n── 轉換矩陣（%d 次轉換，佔 %.1f%% 的日子）──\n", changes, float64(changes)/float64(n)*100)
	type kv struct {
		k string
		v int
	}
	var tl []kv
	for k, v := range trans {
		tl = append(tl, kv{k, v})
	}
	sort.Slice(tl, func(i, j int) bool {
		if tl[i].v != tl[j].v {
			return tl[i].v > tl[j].v
		}
		return tl[i].k < tl[j].k
	})
	for _, e := range tl {
		fmt.Printf("  %-32s %3d\n", e.k, e.v)
	}
	fmt.Printf("  一日內來回（A→B→A）：%d 次", oneDay)
	if float64(oneDay)/float64(max(changes, 1)) > 0.4 {
		fmt.Printf("  ⚠ 佔轉換的 %.0f%%，震盪偏高", float64(oneDay)/float64(changes)*100)
	}
	fmt.Println()

	// ── forward returns ──
	fmt.Println("\n── 前推報酬（benchmark，依 regime 分組）──")
	fmt.Printf("  %-14s %5s %8s %8s %8s %8s\n", "regime", "n", "1d", "5d", "10d", "20d")
	for _, r := range order {
		if cnt[r] == 0 {
			continue
		}
		var sums [4]float64
		var cnts [4]int
		for i, d := range days {
			if d.d.Regime != r {
				continue
			}
			for h, horizon := range []int{1, 5, 10, 20} {
				if i+horizon < n {
					sums[h] += (days[i+horizon].close/d.close - 1) * 100
					cnts[h]++
				}
			}
		}
		fmt.Printf("  %-14s %5d", r, cnt[r])
		for h := 0; h < 4; h++ {
			if cnts[h] == 0 {
				fmt.Printf("      n/a")
				continue
			}
			fmt.Printf(" %+7.2f%%", sums[h]/float64(cnts[h]))
		}
		fmt.Println()
	}

	// ── rule usage ──
	fmt.Println("\n── 規則使用次數 ──")
	rules := map[string]int{}
	for _, d := range days {
		rules[d.d.RuleID]++
	}
	allRules := []string{model.RuleUnknown, model.RuleBearDowntrend, model.RuleBearCollapse,
		model.RuleDistBreadth, model.RuleDistCoolingInst, model.RuleDistFailedHighs,
		model.RuleBullStrong, model.RuleBullUptrend, model.RulePullbackOrderly,
		model.RuleSidewaysRange, model.RulePullbackWeakening, model.RuleSidewaysFallback}
	for _, id := range allRules {
		note := ""
		if rules[id] == 0 {
			note = "  ← 本次未觸發"
			if id == model.RuleDistCoolingInst || id == model.RuleDistFailedHighs {
				note = "  ← 需要法人 posture，本次結構上不可能觸發"
			}
		}
		fmt.Printf("  %-4s %4d%s\n", id, rules[id], note)
	}

	// ── unknown / missing ──
	fmt.Println("\n── UNKNOWN 與缺漏 ──")
	if cnt[model.RegimeUnknown] == 0 {
		fmt.Println("  暖身期外沒有任何 UNKNOWN 日")
	}
	var uw int
	for i := range bars {
		if i+1 < th.MinBenchmarkBars {
			uw++
		}
	}
	fmt.Printf("  暖身期（歷史 < %d 根）排除 %d 日\n", th.MinBenchmarkBars, uw)
	fmt.Printf("  法人三項證據全程不可用：ForeignCash / ForeignFutures / Margin\n")
	fmt.Println(strings.Repeat("═", 40))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
