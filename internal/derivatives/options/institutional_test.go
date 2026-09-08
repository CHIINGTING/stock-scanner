package options_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

func callsPutsRows(t *testing.T) []provider.InstitutionalRow {
	t.Helper()
	decoded, err := provider.ParseInstitutionalCallsPuts(
		readFixture(t, "institutional_callsputs_20260904.json"))
	if err != nil {
		t.Fatalf("parse calls/puts fixture: %v", err)
	}
	if decoded.Report.Status() != "AVAILABLE" {
		t.Fatalf("fixture parses %s: %v", decoded.Report.Status(), decoded.Report.Rejected)
	}
	return decoded.Rows
}

func callPutReq(institution string) options.InstitutionalCallPutRequest {
	return options.InstitutionalCallPutRequest{
		TradingDate:  tradingDate,
		Session:      institutional.SessionCombined,
		Dataset:      provider.SourceInstitutionalCallsPuts,
		Product:      options.ProductTXOOptions,
		Institution:  institution,
		SourceStatus: institutional.StatusAvailable,
		SnapshotID:   7,
	}
}

// TestInstitutionalCallAndPutCarryFlowAndPosition, with the live figures.
//
// 自營商 on 2026-09-04, 臺指選擇權 CALL: TradingVolume(Net) −6,786 while OpenInterest(Net) is
// +6,652. The same session, the same institution, the same side — one number says what was
// traded and the other what is held, and they are not merely different, they have opposite
// signs. A view that showed either under "淨額" would be defensible prose and useless evidence.
func TestInstitutionalCallAndPutCarryFlowAndPosition(t *testing.T) {
	rows := callsPutsRows(t)
	got, err := options.NewInstitutionalCallPut(callPutReq(institutional.InstitutionDealer), rows)
	if err != nil {
		t.Fatal(err)
	}

	flow, ok := got.Call.Flow().Lots()
	if !ok {
		t.Fatal("no CALL flow")
	}
	if flow != -6786 {
		t.Errorf("CALL FLOW = %v, want -6786", flow)
	}
	pos, ok := got.Call.Position().Lots()
	if !ok {
		t.Fatal("no CALL position")
	}
	if pos != 6652 {
		t.Errorf("CALL POSITION = %v, want 6652", pos)
	}
	if flow == pos {
		t.Fatal("FLOW and POSITION are the same number, so one is standing in for the other")
	}
	if got.Call.Flow().Semantics != institutional.SemanticsFlow ||
		got.Call.Position().Semantics != institutional.SemanticsPosition {
		t.Error("a side's metrics do not carry their own semantics")
	}

	// The PUT side is a different measurement again, and the two sides are different
	// instrument scopes so M4 refuses to difference them against each other.
	if got.Put.Scope.Same(got.Call.Scope) {
		t.Fatal("the call and put sides share an instrument scope — one is differenceable " +
			"against the other, and a call lot is not a put lot")
	}
	if _, err := institutional.ChangeOver(got.Call, []institutional.Observation{got.Put}, 1,
		institutional.DefaultPolicy()); err == nil {
		t.Error("M4 accepted a put observation as a call series' history")
	}

	if len(got.Coverage.SidesSeen) != 2 {
		t.Errorf("sides seen = %v", got.Coverage.SidesSeen)
	}
	if !got.Coverage.FoundProduct {
		t.Error("the product was not found in a response that contains it")
	}
	if got.Coverage.RowsExcluded == 0 {
		t.Error("the ETF/股票/金融/電子選擇權 rows were not excluded; the response carries " +
			"five products and only one of them is this one")
	}
}

// TestTheThreeInstitutionsAreReadSeparately.
func TestTheThreeInstitutionsAreReadSeparately(t *testing.T) {
	rows := callsPutsRows(t)
	seen := map[string]float64{}
	for _, inst := range institutional.RequiredInstitutions {
		got, err := options.NewInstitutionalCallPut(callPutReq(inst), rows)
		if err != nil {
			t.Fatalf("%s: %v", inst, err)
		}
		v, ok := got.Call.Position().Lots()
		if !ok {
			t.Fatalf("%s has no CALL position", inst)
		}
		seen[inst] = v
	}
	if seen[institutional.InstitutionTrust] != -4117 {
		t.Errorf("投信 CALL POSITION = %v, want -4117", seen[institutional.InstitutionTrust])
	}
	if seen[institutional.InstitutionForeign] != -3398 {
		t.Errorf("外資及陸資 CALL POSITION = %v, want -3398", seen[institutional.InstitutionForeign])
	}
	if seen[institutional.InstitutionDealer] == seen[institutional.InstitutionTrust] {
		t.Error("two institutions produced the same number")
	}
}

// TestACombinedCallPutFigureIsOnlyEverAContractCount.
//
// §10.8: `call net + put net` presented as directional exposure is what must not appear. The
// defence is arithmetic rather than a label — the summary is |call| + |put|, which cannot
// express a side at all — and the case that shows it is +100 calls against −100 puts, where a
// signed sum reports 0 and a contract count reports 200.
func TestACombinedCallPutFigureIsOnlyEverAContractCount(t *testing.T) {
	rows := []provider.InstitutionalRow{
		{TradingDate: tradingDate, Product: options.ProductTXOOptions,
			Institution: institutional.InstitutionDealer, CallPut: provider.CallSide,
			Semantics: provider.SemanticsPosition, Side: provider.SideNet, Lots: fptr(100)},
		{TradingDate: tradingDate, Product: options.ProductTXOOptions,
			Institution: institutional.InstitutionDealer, CallPut: provider.PutSide,
			Semantics: provider.SemanticsPosition, Side: provider.SideNet, Lots: fptr(-100)},
		{TradingDate: tradingDate, Product: options.ProductTXOOptions,
			Institution: institutional.InstitutionDealer, CallPut: provider.CallSide,
			Semantics: provider.SemanticsFlow, Side: provider.SideNet, Lots: fptr(40)},
		{TradingDate: tradingDate, Product: options.ProductTXOOptions,
			Institution: institutional.InstitutionDealer, CallPut: provider.PutSide,
			Semantics: provider.SemanticsFlow, Side: provider.SideNet, Lots: fptr(-10)},
	}
	got, err := options.NewInstitutionalCallPut(callPutReq(institutional.InstitutionDealer), rows)
	if err != nil {
		t.Fatal(err)
	}

	v, ok := got.PositionSummary.Value()
	if !ok {
		t.Fatal("no position summary")
	}
	if v != 200 {
		t.Errorf("the combined POSITION figure = %v, want 200: |100| + |-100|. A signed sum "+
			"is 0 here, which reads as an institution with no options position at all", v)
	}
	if v == 0 {
		t.Error("the combined figure is call net + put net — the directional claim §10.8 forbids")
	}
	f, _ := got.FlowSummary.Value()
	if f != 50 {
		t.Errorf("the combined FLOW figure = %v, want 50: |40| + |-10|", f)
	}

	for _, s := range []options.ContractCountSummary{got.PositionSummary, got.FlowSummary} {
		if s.Label != options.ContractCountSummaryLabel {
			t.Errorf("the combined figure is labelled %q, want %s", s.Label,
				options.ContractCountSummaryLabel)
		}
		if s.Aggregation != options.ContractCountAggregation {
			t.Errorf("aggregation %q, want %s", s.Aggregation, options.ContractCountAggregation)
		}
		if s.Directional() {
			t.Error("the contract-count summary reports as directional")
		}
		if s.Caveat == "" {
			t.Error("the summary carries no caveat, so a renderer that prints one field " +
				"prints a bare number")
		}
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"DIRECTIONAL", "directional_exposure", "net_exposure"} {
		if strings.Contains(string(b), forbidden) {
			t.Errorf("the serialised call/put reading contains %q", forbidden)
		}
	}
}

// TestACombinedFigureNeedsBothSides: one side absent makes it absent, never the present side
// wearing a both-sides label.
func TestACombinedFigureNeedsBothSides(t *testing.T) {
	rows := []provider.InstitutionalRow{
		{TradingDate: tradingDate, Product: options.ProductTXOOptions,
			Institution: institutional.InstitutionDealer, CallPut: provider.CallSide,
			Semantics: provider.SemanticsPosition, Side: provider.SideNet, Lots: fptr(100)},
	}
	got, err := options.NewInstitutionalCallPut(callPutReq(institutional.InstitutionDealer), rows)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got.PositionSummary.Value(); ok {
		t.Fatalf("a both-sides count of %v was published from the call side alone", v)
	}
	if !contains(got.Coverage.SidesMissing, provider.PutSide) {
		t.Errorf("the missing side is not named: %+v", got.Coverage)
	}
}

// TestTheCallPutFeedHasNoSessionDimension — its policy is COMBINED, fixed by the data
// contract and never read off a clock.
func TestTheCallPutFeedHasNoSessionDimension(t *testing.T) {
	r := callPutReq(institutional.InstitutionDealer)
	r.Session = provider.SessionDay
	if _, err := options.NewInstitutionalCallPut(r, callsPutsRows(t)); err == nil {
		t.Fatal("a DAY session was accepted for a feed that carries no session column")
	}
}

// TestMFoursChangeMachineryAppliesToTheSidesUnchanged.
//
// The reuse is the point: each side IS an institutional.Observation, so CHANGE, the horizons,
// the baseline walk and the percentiles are M4's own code with M4's own rules, and nothing
// about them is reimplemented here.
func TestMFoursChangeMachineryAppliesToTheSidesUnchanged(t *testing.T) {
	rows := callsPutsRows(t)
	today, err := options.NewInstitutionalCallPut(callPutReq(institutional.InstitutionDealer), rows)
	if err != nil {
		t.Fatal(err)
	}
	prevReq := callPutReq(institutional.InstitutionDealer)
	prevReq.TradingDate = "2026-09-03"
	prevRows := make([]provider.InstitutionalRow, len(rows))
	copy(prevRows, rows)
	for i := range prevRows {
		prevRows[i].TradingDate = "2026-09-03"
		if prevRows[i].Lots != nil {
			v := *prevRows[i].Lots - 1000
			prevRows[i].Lots = &v
		}
	}
	prev, err := options.NewInstitutionalCallPut(prevReq, prevRows)
	if err != nil {
		t.Fatal(err)
	}

	hc, err := institutional.ChangeOver(today.Call, []institutional.Observation{prev.Call}, 1,
		institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := hc.Change.Lots()
	if !ok {
		t.Fatalf("no CHANGE: %s (%s)", hc.Status, hc.Change.Reason)
	}
	if v != 1000 {
		t.Errorf("CALL POSITION change = %v, want 1000", v)
	}
	if hc.Change.Semantics != institutional.SemanticsChange {
		t.Errorf("the change carries semantics %s", hc.Change.Semantics)
	}
	if hc.Label != institutional.HorizonLabel(1) {
		t.Errorf("label %q — M4's, not a second one", hc.Label)
	}
}
