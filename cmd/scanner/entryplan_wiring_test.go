package main

import (
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/research"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func wiringEntries() []scanner.WatchlistEntry {
	a := scanner.StockAnalysis{
		Symbol: "2330",
		Name:   "台積電",
		Date:   time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC),
		Close:  100,
		ATR:    2.5,
		MA20:   96,
	}
	b := a
	b.Symbol, b.Name = "2454", "聯發科"
	return []scanner.WatchlistEntry{{A: a}, {A: b}}
}

// The binary's post-pass must attach plans when the config says to, and nothing otherwise.
//
// SCOPE, corrected in EP-6B. This test drives attachEntryPlan — the thin wrapper that reads
// the config flag — DIRECTLY. It proves the flag is read correctly; it does NOT prove the
// binary ever calls the wrapper. (An earlier version of this comment said it went through
// "the same function run() calls"; there is no run(), the pipeline was inline in main(), and
// a direct call to a wrapper cannot say anything about its callers.)
//
// The wiring itself is proved one level up, behaviourally, in entryplan_pipeline_test.go:
// TestProductionPipelineAttachesTheEntryPlan runs buildWatchlist — the production seam main()
// calls — and fails if no plan comes back. What remains here is the flag truth-table, which
// is worth keeping separate because it is the part that has three cases rather than one.
func TestAttachEntryPlanWiringRespectsTheConfigFlag(t *testing.T) {
	on := wiringEntries()
	attachEntryPlan(on, scanner.Config{EnableEntryPlan: true}, nil, research.MarketContext{})
	for i := range on {
		if on[i].EntryPlan == nil {
			t.Fatalf("%s: enable_entry_plan=true produced no plan through the binary's "+
				"post-pass", on[i].A.Symbol)
		}
		if on[i].EntryPlan.Symbol != on[i].A.Symbol {
			t.Errorf("plan %d is about %q, entry is %q — the plans have been crossed",
				i, on[i].EntryPlan.Symbol, on[i].A.Symbol)
		}
	}

	off := wiringEntries()
	attachEntryPlan(off, scanner.Config{}, nil, research.MarketContext{})
	for i := range off {
		if off[i].EntryPlan != nil {
			t.Errorf("%s: a config with enable_entry_plan unset must leave EntryPlan nil, "+
				"got %+v", off[i].A.Symbol, off[i].EntryPlan)
		}
	}

	// show_entry_plan is a DISPLAY flag and must not be able to switch the computation on
	// by itself. Config.Validate rejects that pair at startup; this is the second half of
	// the same rule — if the check were ever removed, the pass still computes nothing.
	showOnly := wiringEntries()
	attachEntryPlan(showOnly, scanner.Config{ShowEntryPlan: true}, nil, research.MarketContext{})
	for i := range showOnly {
		if showOnly[i].EntryPlan != nil {
			t.Errorf("%s: show_entry_plan alone must not compute anything",
				showOnly[i].A.Symbol)
		}
	}
}

// The startup validator the binary already runs must reject the one incoherent pair, so an
// operator who asks for the section without the data is told at startup rather than shown an
// empty section after a full scan.
func TestEntryPlanConfigPairIsValidatedAtStartup(t *testing.T) {
	if err := (scanner.Config{ShowEntryPlan: true}).Validate(); err == nil {
		t.Error("show_entry_plan=true with enable_entry_plan=false must fail validation")
	}
	if err := (scanner.Config{EnableEntryPlan: true, ShowEntryPlan: true}).Validate(); err != nil {
		t.Errorf("a coherent EP-6 pair was rejected: %v", err)
	}
	if err := (scanner.Config{}).Validate(); err != nil {
		t.Errorf("a config that predates EP-6 was rejected: %v", err)
	}
}
