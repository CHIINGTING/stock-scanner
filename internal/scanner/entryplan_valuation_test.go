package scanner

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/valuation"
)

func TestEntryPlanValuationProjectionIsShadowOnlyAndOptional(t *testing.T) {
	series := epSeries(90, 80, 0.25)
	baseEntry := epEntry("2330")
	baseEntry.A.Date = series[len(series)-1].Date
	baseEntry.A.Close = series[len(series)-1].Close
	candles := map[string][]fetcher.Candle{"2330": series}
	market := EntryPlanMarket{Available: true, Regime: "BULL"}

	plain := []WatchlistEntry{baseEntry}
	AttachEntryPlan(plain, candles, market, true)
	unsuitable := []WatchlistEntry{baseEntry}
	target := 101.0
	AttachEntryPlan(unsuitable, candles, market, true, map[string]EntryPlanValuation{
		"2330": {Status: string(valuation.Available), BaseTarget: &target, Suitability: string(valuation.SuitabilityUnsuitable), AsOf: plain[0].EntryPlan.AsOf},
	})
	if !reflect.DeepEqual(plain[0].EntryPlan.Target2, unsuitable[0].EntryPlan.Target2) || plain[0].EntryPlan.Status != unsuitable[0].EntryPlan.Status {
		t.Fatalf("unsuitable valuation changed plan: plain=%+v got=%+v", plain[0].EntryPlan, unsuitable[0].EntryPlan)
	}

	before := baseEntry
	suitable := []WatchlistEntry{baseEntry}
	target = 110
	AttachEntryPlan(suitable, candles, market, true, map[string]EntryPlanValuation{
		"2330": {Status: string(valuation.Available), BaseTarget: &target, Suitability: string(valuation.SuitabilitySuitable), AsOf: plain[0].EntryPlan.AsOf},
	})
	after := suitable[0]
	after.EntryPlan = nil
	if !reflect.DeepEqual(before, after) {
		t.Fatal("valuation projection changed scanner evidence outside EntryPlan")
	}
	if suitable[0].EntryPlan.Status != plain[0].EntryPlan.Status {
		t.Fatal("valuation changed EntryStatus")
	}
	if reflect.DeepEqual(suitable[0].EntryPlan.Target2, plain[0].EntryPlan.Target2) {
		t.Fatal("suitable binding valuation did not change Target2")
	}
	if !reflect.DeepEqual(suitable[0].EntryPlan.Target1, plain[0].EntryPlan.Target1) || !reflect.DeepEqual(suitable[0].EntryPlan.RiskReward, plain[0].EntryPlan.RiskReward) {
		t.Fatal("valuation changed Target1 or R:R")
	}
}

func TestUnavailableValuationCannotFabricateACeiling(t *testing.T) {
	series := epSeries(90, 80, 0.25)
	e := epEntry("2330")
	e.A.Date = series[len(series)-1].Date
	e.A.Close = series[len(series)-1].Close
	plain, unavailable := []WatchlistEntry{e}, []WatchlistEntry{e}
	candles := map[string][]fetcher.Candle{"2330": series}
	market := EntryPlanMarket{Available: true, Regime: "BULL"}
	AttachEntryPlan(plain, candles, market, true)
	zero := 110.0
	AttachEntryPlan(unavailable, candles, market, true, map[string]EntryPlanValuation{"2330": {
		Status: string(valuation.Unavailable), BaseTarget: &zero, Suitability: string(valuation.SuitabilitySuitable), AsOf: e.A.Date.Format("2006-01-02")}})
	if !reflect.DeepEqual(plain[0].EntryPlan.Target2, unavailable[0].EntryPlan.Target2) || plain[0].EntryPlan.Status != unavailable[0].EntryPlan.Status {
		t.Fatal("unavailable valuation changed the plan")
	}
	for _, evidence := range unavailable[0].EntryPlan.Evidence {
		if evidence.Key == entryplan.EvidenceValuationBaseTarget {
			if evidence.Status != entryplan.Unavailable || evidence.Value != nil {
				t.Fatalf("unavailable valuation fabricated target evidence: %+v", evidence)
			}
			return
		}
	}
	t.Fatal("valuation evidence row missing")
}

func TestEntryPlanValuationCannotEscapeSellContradiction(t *testing.T) {
	e := epEntry("2330")
	e.A.Action = ActionSell
	series := epSeries(90, 80, 0.25)
	e.A.Date = series[len(series)-1].Date
	e.A.Close = series[len(series)-1].Close
	target := 123.0
	entries := []WatchlistEntry{e}
	AttachEntryPlan(entries, map[string][]fetcher.Candle{"2330": series}, EntryPlanMarket{Available: true, Regime: "BULL"}, true,
		map[string]EntryPlanValuation{"2330": {Status: string(valuation.Available), BaseTarget: &target, Suitability: string(valuation.SuitabilitySuitable), AsOf: e.A.Date.Format("2006-01-02")}})
	p := entries[0].EntryPlan
	if p.Status != entryplan.StatusNoValidEntry || p.HasExecutablePrices() {
		t.Fatalf("sell contradiction escaped: %+v", p)
	}
	b, _ := json.Marshal(p.EntryTrace)
	if string(b) != "null" && containsJSONNumber(b, "123") {
		t.Fatalf("valuation target leaked through trace: %s", b)
	}
}

func containsJSONNumber(b []byte, n string) bool {
	return string(b) != "" && bytes.Contains(b, []byte(n))
}

// F-1 (EP-6G fix round 1). The WHOLE serialized plan, not only EntryTrace: a sell-type Action
// makes the plan S1, and the planted valuation target must appear nowhere in its JSON — not in
// Target2, not in another price field, not in EntryTrace and not on the VALUATION_BASE_TARGET
// evidence row. Each Action runs independently. The anti-vacuity control is the SAME fixture
// with a NOT_CONTRADICTED Action (BUY), whose JSON must contain the planted value, so a
// number that never reached the plan in the first place cannot make this test pass.
//
// 187.6543 is chosen to collide with nothing: the fixture's prices sit near 80–102, and a
// tick-aligned price never carries four decimals.
func TestSellSidePlanJSONCarriesNoValuationTarget(t *testing.T) {
	const planted = "187.6543"
	target := 187.6543
	series := epSeries(90, 80, 0.25)
	build := func(action Action) *entryplan.Plan {
		e := epEntry("2330")
		e.A.Action = action
		e.A.Date = series[len(series)-1].Date
		e.A.Close = series[len(series)-1].Close
		entries := []WatchlistEntry{e}
		v := target
		AttachEntryPlan(entries, map[string][]fetcher.Candle{"2330": series}, EntryPlanMarket{Available: true, Regime: "BULL"}, true,
			map[string]EntryPlanValuation{"2330": {Status: string(valuation.Available), BaseTarget: &v,
				Suitability: string(valuation.SuitabilitySuitable), AsOf: e.A.Date.Format("2006-01-02")}})
		if entries[0].EntryPlan == nil {
			t.Fatalf("%s: no plan attached", action)
		}
		return entries[0].EntryPlan
	}

	control := build(ActionBuy)
	cb, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(cb, []byte(planted)) {
		t.Fatalf("anti-vacuity: the NOT_CONTRADICTED control does not carry %s, so the sell-side "+
			"assertions below could pass without the value ever reaching a plan: %s", planted, cb)
	}

	actions := []Action{ActionSell, ActionReduce, ActionTakeProfit, ActionStopLoss}
	ran := 0
	for _, action := range actions {
		p := build(action)
		ran++
		if p.Status != entryplan.StatusNoValidEntry || p.EntryTrace == nil ||
			p.EntryTrace.Decision.Rule != entryplan.RuleThesisContradicted {
			t.Fatalf("%s: plan %q is not decided at S1", action, p.Status)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(planted)) {
			t.Errorf("%s: valuation target %s leaked into the serialized S1 plan: %s", action, planted, b)
		}
		found := false
		for _, ev := range p.Evidence {
			if ev.Key != entryplan.EvidenceValuationBaseTarget {
				continue
			}
			found = true
			if ev.Status == entryplan.Available || ev.Value != nil || ev.Text != string(entryplan.DispositionNotScreened) {
				t.Errorf("%s: S1 valuation row = %+v, want non-AVAILABLE, no value, NOT_SCREENED", action, ev)
			}
		}
		if !found {
			t.Errorf("%s: VALUATION_BASE_TARGET row missing from the S1 plan", action)
		}
	}
	if ran != len(actions) || ran != 4 {
		t.Fatalf("ran %d of 4 sell-type Actions", ran)
	}
}

// F-2 / R8d. The bridge itself must refuse valuation evidence stamped for a different session
// than the plan's AsOf. cmd/scanner only ever passes the entry date, so this can only be pinned
// here, by handing AttachEntryPlan a mismatched AsOf directly. Control: the same evidence with
// the matching AsOf DOES cap Target2, so the mismatch assertion is not vacuous.
func TestAttachEntryPlanIgnoresValuationForAnotherSession(t *testing.T) {
	series := epSeries(90, 80, 0.25)
	e := epEntry("2330")
	e.A.Date = series[len(series)-1].Date
	e.A.Close = series[len(series)-1].Close
	candles := map[string][]fetcher.Candle{"2330": series}
	market := EntryPlanMarket{Available: true, Regime: "BULL"}
	asOf := e.A.Date.Format("2006-01-02")
	otherDay := e.A.Date.AddDate(0, 0, -1).Format("2006-01-02")

	plain := []WatchlistEntry{e}
	AttachEntryPlan(plain, candles, market, true)
	if plain[0].EntryPlan == nil || plain[0].EntryPlan.Target2 == nil {
		t.Fatalf("fixture: plain plan has no structural Target2: %+v", plain[0].EntryPlan)
	}

	attach := func(stamp string) *entryplan.Plan {
		target := 110.0
		out := []WatchlistEntry{e}
		AttachEntryPlan(out, candles, market, true, map[string]EntryPlanValuation{"2330": {
			Status: string(valuation.Available), BaseTarget: &target,
			Suitability: string(valuation.SuitabilitySuitable), AsOf: stamp}})
		return out[0].EntryPlan
	}

	matched := attach(asOf)
	if reflect.DeepEqual(matched.Target2, plain[0].EntryPlan.Target2) ||
		matched.EntryTrace.Targets.ValuationCeiling != entryplan.ValuationCeilingApplied {
		t.Fatalf("anti-vacuity: matching-AsOf valuation did not cap Target2 (ceiling %q)",
			matched.EntryTrace.Targets.ValuationCeiling)
	}

	mismatched := attach(otherDay)
	if !reflect.DeepEqual(mismatched.Target2, plain[0].EntryPlan.Target2) {
		t.Errorf("valuation stamped %s capped the %s plan: Target2 %+v, structural %+v",
			otherDay, asOf, mismatched.Target2, plain[0].EntryPlan.Target2)
	}
	if got := mismatched.EntryTrace.Targets.ValuationCeiling; got != entryplan.ValuationCeilingUnavailable {
		t.Errorf("ceiling outcome = %q, want VALUATION_UNAVAILABLE for another session's valuation", got)
	}
	found := false
	for _, ev := range mismatched.Evidence {
		if ev.Key == entryplan.EvidenceValuationBaseTarget {
			found = true
			if ev.Status != entryplan.Unavailable || ev.Value != nil {
				t.Errorf("another session's valuation reached the evidence row: %+v", ev)
			}
		}
	}
	if !found {
		t.Error("VALUATION_BASE_TARGET row missing")
	}
}
