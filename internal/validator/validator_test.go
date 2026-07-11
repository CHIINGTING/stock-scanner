package validator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

const fixtureHTML = `<html><body>
<div id="tab-market"><table><tbody>
<tr><td class="sym">2330</td><td class="name-col">台積電</td>
  <td><span class="action-badge act-buy">STRONG BUY</span></td></tr>
<tr><td class="sym">2317</td><td class="name-col">鴻海</td>
  <td><span class="action-badge act-sell">SELL</span></td></tr>
</tbody></table></div>
<div id="tab-watchlist"><table><tbody>
<tr class="sector-row" data-score="88" data-action="3">
  <td><span class="sym">2327</span> <span class="name-col">國巨</span></td>
  <td><span class="stage-badge rk-run">主升 MAIN_RUN</span></td>
  <td><span class="act-badge act-tp">TAKE_PROFIT</span></td>
  <td><span class="risk-tag">上影/追高</span></td></tr>
</tbody></table></div>
</body></html>`

func TestParseReportFixture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report_20260601.html")
	if err := os.WriteFile(path, []byte(fixtureHTML), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ := time.Parse("20060102", "20260601")
	snaps, err := ParseReport(path, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Fatalf("want 3 signals, got %d: %+v", len(snaps), snaps)
	}
	by := map[string]SignalSnapshot{}
	for _, s := range snaps {
		by[s.Code] = s
	}
	if s := by["2330"]; s.Action != "STRONG BUY" || s.ActionGroup != BuyGroup || s.SourceTab != "market" {
		t.Errorf("2330 wrong: %+v", s)
	}
	if s := by["2317"]; s.ActionGroup != ReduceGroup {
		t.Errorf("2317 should be REDUCE_GROUP: %+v", s)
	}
	w := by["2327"]
	if w.ActionGroup != ReduceGroup || w.SourceTab != "watchlist" || w.Score != 88 {
		t.Errorf("2327 wrong: %+v", w)
	}
	if w.Stage != "主升 MAIN_RUN" {
		t.Errorf("2327 stage: %q", w.Stage)
	}
	if !contains(w.ReasonTags, "MAIN_RUN") || !contains(w.ReasonTags, "追高") {
		t.Errorf("2327 reason tags: %v", w.ReasonTags)
	}
}

func TestGroupForAction(t *testing.T) {
	cases := map[string]ActionGroup{
		"STRONG BUY": BuyGroup, "BUY": BuyGroup, "PULLBACK_BUY": BuyGroup, "加買": BuyGroup,
		"REDUCE": ReduceGroup, "SELL": ReduceGroup, "TAKE PROFIT": ReduceGroup,
		"REMOVE_FROM_WATCHLIST": ReduceGroup, "停損": ReduceGroup,
		"WAIT": WatchGroup, "HOLD": WatchGroup, "WATCH_CLOSELY": WatchGroup,
		"BANANA": UnknownGroup,
	}
	for action, want := range cases {
		if got := GroupForAction(action); got != want {
			t.Errorf("GroupForAction(%q) = %s, want %s", action, got, want)
		}
	}
}

// synthSeries builds an in-memory price series (bypassing the file cache) so the
// price/evaluator logic can be tested deterministically.
func synthSeries(code string, start time.Time, closes []float64) *PriceSeries {
	candles := make([]fetcher.Candle, len(closes))
	byDate := make(map[int]int, len(closes))
	for i, c := range closes {
		day := start.AddDate(0, 0, i)
		candles[i] = fetcher.Candle{Date: day, Open: c, High: c * 1.02, Low: c * 0.97, Close: c}
		byDate[dateKey(day)] = i
	}
	return &PriceSeries{Code: code, Candles: candles, byDate: byDate}
}

func TestOutcomeAndBuyVerdict(t *testing.T) {
	start := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)
	// signal on day 0 (close 100); entry day 1 open 101; rises steadily.
	closes := []float64{100, 101, 103, 105, 108, 110, 112, 114, 116, 118, 121, 123}
	series := synthSeries("9999", start, closes)

	out, ok := series.Outcome(start, []int{3, 5, 10})
	if !ok {
		t.Fatal("outcome not ok")
	}
	if out.SignalPrice != 100 {
		t.Errorf("signal price = %v, want 100", out.SignalPrice)
	}
	if out.EntryPrice != 101 {
		t.Errorf("entry price = %v, want 101 (next-day open)", out.EntryPrice)
	}
	if _, has := out.Returns[5]; !has {
		t.Errorf("expected T+5 return present")
	}

	// Evaluate a BUY signal against the injected series (no file cache needed).
	store := NewPriceStore("")
	store.loaded["9999"] = series
	eval := &Evaluator{Store: store, Benchmark: &Benchmark{}, Horizons: []int{3, 5, 10}}
	res := eval.Evaluate(SignalSnapshot{SignalDate: start, Code: "9999", Action: "BUY", ActionGroup: BuyGroup})
	if res.Result != ResultCorrect {
		t.Errorf("rising BUY should be CORRECT, got %s (%s)", res.Result, res.Reason)
	}
}

func TestReduceWrongOnRally(t *testing.T) {
	start := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)
	// REDUCE but the stock rallies hard -> WRONG (T+10 >= +8%).
	closes := []float64{100, 101, 104, 107, 110, 113, 116, 119, 122, 125, 128, 131}
	series := synthSeries("8888", start, closes)
	store := NewPriceStore("")
	store.loaded["8888"] = series
	eval := &Evaluator{Store: store, Benchmark: &Benchmark{}, Horizons: []int{3, 5, 10}}
	res := eval.Evaluate(SignalSnapshot{SignalDate: start, Code: "8888", Action: "REDUCE", ActionGroup: ReduceGroup})
	if res.Result != ResultWrong {
		t.Errorf("REDUCE before a rally should be WRONG, got %s (%s)", res.Result, res.Reason)
	}
}

func TestPendingWhenNoForwardData(t *testing.T) {
	start := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)
	// Only signal day + one entry day; no T+3 data.
	series := synthSeries("7777", start, []float64{100, 101})
	store := NewPriceStore("")
	store.loaded["7777"] = series
	eval := &Evaluator{Store: store, Benchmark: &Benchmark{}, Horizons: []int{3, 5, 10}}
	res := eval.Evaluate(SignalSnapshot{SignalDate: start, Code: "7777", Action: "BUY", ActionGroup: BuyGroup})
	if res.Result != ResultPending {
		t.Errorf("want PENDING, got %s", res.Result)
	}
}

func TestOverheatedSplitOutOfReduce(t *testing.T) {
	// watchlist OVERHEATED "追高" caution -> ENTRY_CAUTION_GROUP, not REDUCE.
	oh := SignalSnapshot{Action: "TAKE_PROFIT", SourceTab: "watchlist",
		Stage: "OVERHEATED", ReasonTags: []string{"OVERHEATED", "追高"}}
	if g := GroupForSnapshot(oh); g != EntryCautionGroup {
		t.Errorf("watchlist OVERHEATED should be ENTRY_CAUTION_GROUP, got %s", g)
	}
	// A real position exit (no OVERHEATED tag) stays in REDUCE.
	exit := SignalSnapshot{Action: "TAKE PROFIT", SourceTab: "positions"}
	if g := GroupForSnapshot(exit); g != ReduceGroup {
		t.Errorf("positions TAKE PROFIT should stay REDUCE_GROUP, got %s", g)
	}
	// A bare "追高" risk sub-tag on a MAIN_RUN is NOT an overheated caution.
	mainRun := SignalSnapshot{Action: "TAKE_PROFIT", SourceTab: "watchlist",
		Stage: "主升 MAIN_RUN", ReasonTags: []string{"MAIN_RUN", "追高"}}
	if g := GroupForSnapshot(mainRun); g != ReduceGroup {
		t.Errorf("MAIN_RUN with 追高 sub-tag should stay REDUCE_GROUP, got %s", g)
	}
}

func TestEntryCautionVerdicts(t *testing.T) {
	start := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)
	store := NewPriceStore("")
	eval := &Evaluator{Store: store, Benchmark: &Benchmark{}, Horizons: []int{3, 5, 10}}
	caution := SignalSnapshot{SignalDate: start, Action: "TAKE_PROFIT",
		SourceTab: "watchlist", Stage: "OVERHEATED", ReasonTags: []string{"OVERHEATED"},
		ActionGroup: EntryCautionGroup}

	// Ran away with no pullback (synthSeries low is only -3% of close) -> WRONG.
	store.loaded["1111"] = synthSeries("1111", start,
		[]float64{100, 101, 108, 115, 120, 125, 130, 135, 140, 145, 150, 155})
	c1 := caution
	c1.Code = "1111"
	if res := eval.Evaluate(c1); res.Result != ResultWrong {
		t.Errorf("overheated warning before a straight-up run should be WRONG, got %s (%s)", res.Result, res.Reason)
	}

	// A real pullback appears (drops well below entry) -> CORRECT.
	store.loaded["2222"] = synthSeries("2222", start,
		[]float64{100, 101, 95, 90, 88, 92, 96, 94, 90, 93, 97, 99})
	c2 := caution
	c2.Code = "2222"
	if res := eval.Evaluate(c2); res.Result != ResultCorrect {
		t.Errorf("overheated warning followed by a pullback should be CORRECT, got %s (%s)", res.Result, res.Reason)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
