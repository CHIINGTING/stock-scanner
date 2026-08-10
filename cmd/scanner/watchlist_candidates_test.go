package main

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// The watchlist keeps everything that is not exit advice. HOLD is the weakest tier kept:
// neither a buy nor a sell, so it is still being watched. REDUCE and SELL are exits and
// must stay out — a stock being exited is not a candidate to enter.
func TestQualifiesForWatchlist(t *testing.T) {
	in := []scanner.Action{
		scanner.ActionStrongBuy,
		scanner.ActionBuy,
		scanner.ActionWatch,
		scanner.ActionHold,
	}
	for _, a := range in {
		if !qualifiesForWatchlist(a) {
			t.Errorf("%s should qualify for the watchlist", a)
		}
	}

	out := []scanner.Action{
		scanner.ActionReduce,
		scanner.ActionTakeProfit,
		scanner.ActionStopLoss,
		scanner.ActionSell,
		scanner.Action(""),
		scanner.Action("UNKNOWN"),
	}
	for _, a := range out {
		if qualifiesForWatchlist(a) {
			t.Errorf("%q must not qualify for the watchlist", a)
		}
	}
}

func TestCollectWatchCandidatesKeepsHoldAndDedupes(t *testing.T) {
	results := []scanner.StockAnalysis{
		{Symbol: "1111", Name: "買進股", Action: scanner.ActionBuy},
		{Symbol: "2222", Name: "觀察股", Action: scanner.ActionWatch},
		{Symbol: "3333", Name: "持有股", Action: scanner.ActionHold},
		{Symbol: "4444", Name: "減碼股", Action: scanner.ActionReduce},
		{Symbol: "5555", Name: "賣出股", Action: scanner.ActionSell},
		{Symbol: "3333", Name: "持有股", Action: scanner.ActionHold}, // duplicate code
		{Symbol: "", Name: "無代號", Action: scanner.ActionBuy},      // no symbol
	}

	got := collectWatchCandidates(results)

	want := []string{"1111", "2222", "3333"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %+v, want %d", len(got), got, len(want))
	}
	// Order follows the scan order, which is score-descending — the strongest names first.
	for i, code := range want {
		if got[i].Code != code {
			t.Errorf("candidate %d = %s, want %s", i, got[i].Code, code)
		}
	}
}
