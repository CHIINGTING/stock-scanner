package research

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// ── fixtures ─────────────────────────────────────────────────────────────────────────────

const epTestDate = "2026-09-10"

// epSentinel is a number no published field in epFullPlan carries. It is planted in every
// EntryTrace price-like slot, so a persisted row holding it can only have come from the trace.
const epSentinel = 187.6543

// epFullPlan is a plan with every published field present and exact, distinct values, plus a
// trace, reasons, caveats, notes and an evidence census that must NOT be persisted.
func epFullPlan(symbol, asOf string) *entryplan.Plan {
	s := epSentinel
	return &entryplan.Plan{
		Symbol: symbol, AsOf: asOf,
		Status:        entryplan.StatusWaitPullback,
		RuleVersion:   entryplan.RuleVersion,
		IdealEntry:    &entryplan.PriceZone{Low: 98.5, High: 101, WidthATR: f64(0.8), PriceBasis: entryplan.PriceBasisRaw},
		MaxChasePrice: f64(103.5),
		Invalidation:  &entryplan.Invalidation{Price: 95.2, Note: "NOTE-SENTINEL-INVALIDATION"},
		Target1:       &entryplan.PriceLevel{Price: 108, RMultiple: f64(1.2), Note: "NOTE-SENTINEL-T1"},
		Target2:       &entryplan.PriceLevel{Price: 117.5, RMultiple: f64(2.9)},
		RiskReward:    &entryplan.RiskReward{RiskPerShare: 5.8, RewardPerShare: 7, Ratio: 1.25, Target: "TARGET_1"},
		EntryTrace: &entryplan.EntryTrace{
			RuleVersion: entryplan.RuleVersion,
			Policy:      entryplan.PolicyTrace{Name: entryplan.PolicyPullbackOnly, ChaseMaxATR: &s},
			Zone:        entryplan.ZoneTrace{RawLow: &s, RawHigh: &s, Center: &s},
			Chase:       entryplan.ChaseTrace{Raw: &s, LimitUp: &s},
			Stop:        entryplan.StopTrace{RawPrice: &s},
			Targets: entryplan.TargetTrace{Target2Raw: &s, ValuationTarget: &s, Target2Clamped: &s,
				Ratio: &s, Reward: &s},
			Decision:       entryplan.DecisionTrace{Rule: entryplan.RuleWaitPullback, CurrentPrice: &s},
			InvariantCheck: entryplan.InvariantCheckClean,
		},
		Confidence: entryplan.ConfidenceMedium,
		Reasons:    []entryplan.Reason{entryplan.ReasonStatusPriceAboveEntryZone},
		Evidence:   []entryplan.Evidence{{Key: "CURRENT_PRICE", Status: entryplan.Available, Value: &s}},
		Caveats:    []string{"PROSE-SENTINEL-CAVEAT"},
	}
}

func epSnap(symbol, date string) store.StockSnapshot {
	return store.StockSnapshot{Symbol: symbol, TradingDate: date, SourceTab: store.TabWatchlist}
}

func mustProject(t *testing.T, p *entryplan.Plan) []store.Evidence {
	t.Helper()
	items, err := projectEntryPlan(p, epSnap("2330", epTestDate))
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return items
}

func keysOf(items []store.Evidence) []string {
	out := make([]string, 0, len(items))
	for _, e := range items {
		out = append(out, e.Key)
	}
	return out
}

// epNum / epText build the exact row the projection is expected to emit. Written out per test
// rather than derived from the registry, so a registry edit cannot move the expectation with it.
func epNum(key string, v float64, unit, src string) store.Evidence {
	return store.Evidence{Category: "entry_plan", Key: key, NumValue: &v, Unit: unit, Source: src}
}

func epText(key, v, src string) store.Evidence {
	return store.Evidence{Category: "entry_plan", Key: key, TextValue: &v, Source: src}
}

// epStoredRows reads back every row of one snapshot whose category is entry_plan OR whose key
// starts with ep_, so a row written under the wrong category is still seen.
func epStoredRows(t *testing.T, st *store.Store, snapID int64) map[string]store.Evidence {
	t.Helper()
	rows, err := st.Evidence().BySnapshot(context.Background(), snapID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]store.Evidence{}
	for _, r := range rows {
		if r.Category == "entry_plan" || strings.HasPrefix(r.Key, "ep_") {
			out[r.Key] = r
		}
	}
	return out
}

func numOf(t *testing.T, rows map[string]store.Evidence, key string) float64 {
	t.Helper()
	r, ok := rows[key]
	if !ok {
		t.Fatalf("%s absent; rows %v", key, sortedKeys(rows))
	}
	if r.NumValue == nil || r.TextValue != nil {
		t.Fatalf("%s is not a numeric row: %+v", key, r)
	}
	return *r.NumValue
}

func sortedKeys(m map[string]store.Evidence) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── series fixture for plans produced by the real bridge ──────────────────────────────────

var epSeriesEnd = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

func epCandles(n int, first, step float64) []fetcher.Candle {
	out := make([]fetcher.Candle, n)
	for i := 0; i < n; i++ {
		c := first + step*float64(i)
		out[i] = fetcher.Candle{Date: epSeriesEnd.AddDate(0, 0, i-(n-1)), Open: c, High: c + 1,
			Low: c - 1, Close: c, AdjClose: c, Volume: 1_000_000}
	}
	return out
}

// epBridgeEntries runs the production bridge (scanner.AttachEntryPlan) on one BREAKOUT entry.
func epBridgeEntries(t *testing.T, symbol string, action scanner.Action, enabled bool,
	valuation map[string]scanner.EntryPlanValuation) ([]scanner.WatchlistEntry, []fetcher.Candle) {
	t.Helper()
	series := epCandles(90, 80, 0.25)
	e := bareEntry(symbol)
	e.A.Date = series[len(series)-1].Date
	e.A.Close = series[len(series)-1].Close
	e.A.ATR, e.A.MA20, e.A.AvgVolume20, e.A.Action = 2.5, 96, 12_000, action
	e.Consol = scanner.Consolidation{Bucket: scanner.SwingBase, Days: 14, BaseLow: 92, PivotHigh: 104}
	e.WatchAction = scanner.ActBreakoutBuy
	entries := []scanner.WatchlistEntry{e}
	var views []map[string]scanner.EntryPlanValuation
	if valuation != nil {
		views = append(views, valuation)
	}
	scanner.AttachEntryPlan(entries, map[string][]fetcher.Candle{symbol: series},
		scanner.EntryPlanMarket{Available: true, Regime: "BULL"}, enabled, views...)
	return entries, series
}

// ── 1. nil plan ──────────────────────────────────────────────────────────────────────────

func TestEntryPlanNilPlanProjectsNoRows(t *testing.T) {
	items, err := projectEntryPlan(nil, epSnap("2330", epTestDate))
	if err != nil || items != nil {
		t.Fatalf("nil plan → items %v, err %v; want nil, nil", items, err)
	}
	var c collector
	if err := buildEntryPlanEvidence(&c, bareEntry("2330"), epSnap("2330", epTestDate)); err != nil || len(c.items) != 0 {
		t.Fatalf("entry without plan → %d rows, err %v", len(c.items), err)
	}
}

// ── 2. complete plan: exact rows, exact order, exact units and sources ────────────────────

func TestEntryPlanCompletePlanProjectsExactRows(t *testing.T) {
	got := mustProject(t, epFullPlan("2330", epTestDate))
	want := []store.Evidence{
		epText("ep_status", "WAIT_PULLBACK", "entryplan.Plan"),
		epText("ep_rule_version", "EP6G-v1", "entryplan.Plan"),
		epText("ep_policy", "PULLBACK_ONLY", "entryplan.EntryTrace.Policy.Name"),
		epNum("ep_zone_low", 98.5, "TWD", "entryplan.Plan"),
		epNum("ep_zone_high", 101, "TWD", "entryplan.Plan"),
		epNum("ep_max_chase", 103.5, "TWD", "entryplan.Plan"),
		epNum("ep_invalidation", 95.2, "TWD", "entryplan.Plan"),
		epNum("ep_target_1", 108, "TWD", "entryplan.Plan"),
		epNum("ep_target_2", 117.5, "TWD", "entryplan.Plan"),
		epNum("ep_rr_ratio", 1.25, "x", "entryplan.Plan"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection mismatch:\n got %s\nwant %s", renderEv(got), renderEv(want))
	}
}

func renderEv(items []store.Evidence) string {
	var b strings.Builder
	for _, e := range items {
		fmt.Fprintf(&b, "\n  %s/%s num=%v text=%v unit=%q src=%q", e.Category, e.Key,
			derefF(e.NumValue), derefS(e.TextValue), e.Unit, e.Source)
	}
	return b.String()
}

func derefF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefS(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// ── 3 / 10 / 11. unavailable fields are absent, key by key ────────────────────────────────

func TestEntryPlanUnavailableFieldsLeaveTheirKeysAbsent(t *testing.T) {
	all := []string{"ep_status", "ep_rule_version", "ep_policy", "ep_zone_low", "ep_zone_high",
		"ep_max_chase", "ep_invalidation", "ep_target_1", "ep_target_2", "ep_rr_ratio"}
	without := func(drop ...string) []string {
		var out []string
		for _, k := range all {
			keep := true
			for _, d := range drop {
				if k == d {
					keep = false
				}
			}
			if keep {
				out = append(out, k)
			}
		}
		return out
	}
	cases := []struct {
		name  string
		edit  func(*entryplan.Plan)
		wantK []string
	}{
		{"complete", func(*entryplan.Plan) {}, all},
		{"Target2 nil", func(p *entryplan.Plan) { p.Target2 = nil }, without("ep_target_2")},
		{"MaxChasePrice nil", func(p *entryplan.Plan) { p.MaxChasePrice = nil }, without("ep_max_chase")},
		{"RiskReward nil", func(p *entryplan.Plan) { p.RiskReward = nil }, without("ep_rr_ratio")},
		{"IdealEntry nil", func(p *entryplan.Plan) { p.IdealEntry = nil }, without("ep_zone_low", "ep_zone_high")},
		{"Invalidation nil", func(p *entryplan.Plan) { p.Invalidation = nil }, without("ep_invalidation")},
		{"Target1 nil", func(p *entryplan.Plan) { p.Target1 = nil }, without("ep_target_1")},
		{"EntryTrace nil", func(p *entryplan.Plan) { p.EntryTrace = nil }, without("ep_policy")},
		{"policy name empty", func(p *entryplan.Plan) { p.EntryTrace.Policy.Name = "" }, without("ep_policy")},
	}
	ran := 0
	for _, c := range cases {
		p := epFullPlan("2330", epTestDate)
		c.edit(p)
		got := keysOf(mustProject(t, p))
		ran++
		if !reflect.DeepEqual(got, c.wantK) {
			t.Errorf("%s: keys %v, want %v", c.name, got, c.wantK)
		}
	}
	if ran != 9 {
		t.Fatalf("ran %d of 9 cases", ran)
	}
}

func TestEntryPlanRiskRewardRatioIsCopiedWhenPresent(t *testing.T) {
	p := epFullPlan("2330", epTestDate)
	p.RiskReward = &entryplan.RiskReward{RiskPerShare: 4, RewardPerShare: 9, Ratio: 2.375}
	found := 0
	for _, e := range mustProject(t, p) {
		if e.Key == "ep_rr_ratio" {
			found++
			if e.NumValue == nil || *e.NumValue != 2.375 || e.Unit != "x" {
				t.Errorf("ep_rr_ratio = %s, want 2.375 x", renderEv([]store.Evidence{e}))
			}
		}
	}
	if found != 1 {
		t.Fatalf("ep_rr_ratio emitted %d times, want 1", found)
	}
}

func TestEntryPlanRiskRewardRatioIsAbsentWhenUnavailable(t *testing.T) {
	p := epFullPlan("2330", epTestDate)
	p.RiskReward = nil
	items := mustProject(t, p)
	if len(items) != 9 {
		t.Fatalf("got %d rows, want the 9 non-ratio rows: %v", len(items), keysOf(items))
	}
	for _, e := range items {
		if e.Key == "ep_rr_ratio" {
			t.Fatalf("ep_rr_ratio persisted without a RiskReward: %s", renderEv([]store.Evidence{e}))
		}
	}
}

// ── 4. missing never becomes zero ─────────────────────────────────────────────────────────

func TestEntryPlanMissingNeverBecomesNumericZero(t *testing.T) {
	// A plan with no published price at all: only the status, version and policy rows.
	bare := &entryplan.Plan{Symbol: "2330", AsOf: epTestDate, Status: entryplan.StatusInsufficientData,
		RuleVersion: entryplan.RuleVersion, EntryTrace: &entryplan.EntryTrace{
			Policy: entryplan.PolicyTrace{Name: entryplan.PolicyNormal}}}
	got := mustProject(t, bare)
	want := []store.Evidence{
		epText("ep_status", "INSUFFICIENT_DATA", "entryplan.Plan"),
		epText("ep_rule_version", "EP6G-v1", "entryplan.Plan"),
		epText("ep_policy", "NORMAL", "entryplan.EntryTrace.Policy.Name"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("price-less plan:\n got %s\nwant %s", renderEv(got), renderEv(want))
	}
	numeric := 0
	for _, e := range got {
		if e.NumValue != nil {
			numeric++
		}
	}
	if numeric != 0 {
		t.Fatalf("%d numeric rows for a plan with no price", numeric)
	}

	// A PRESENT price of 0 (or NaN) is a malformed plan, never a stored zero: nothing is written.
	for name, edit := range map[string]func(*entryplan.Plan){
		"MaxChasePrice=0":    func(p *entryplan.Plan) { p.MaxChasePrice = f64(0) },
		"Target2.Price=0":    func(p *entryplan.Plan) { p.Target2.Price = 0 },
		"IdealEntry.Low=-1":  func(p *entryplan.Plan) { p.IdealEntry.Low = -1 },
		"RiskReward.Ratio=0": func(p *entryplan.Plan) { p.RiskReward.Ratio = 0 },
	} {
		p := epFullPlan("2330", epTestDate)
		edit(p)
		items, err := projectEntryPlan(p, epSnap("2330", epTestDate))
		if err == nil || items != nil {
			t.Errorf("%s: items %s, err %v; want no rows and an error", name, renderEv(items), err)
		}
	}
}

// ── 5. sell-side contradiction ────────────────────────────────────────────────────────────

func TestEntryPlanSellContradictionPersistsStatusOnlyEvenWithTraceNumbers(t *testing.T) {
	p := epFullPlan("2330", epTestDate)
	p.Status = entryplan.StatusNoValidEntry
	p.IdealEntry, p.MaxChasePrice, p.Invalidation, p.Target1, p.Target2, p.RiskReward = nil, nil, nil, nil, nil, nil
	s := epSentinel
	p.EntryTrace.Decision.Rule = entryplan.RuleThesisContradicted
	p.EntryTrace.Zone.Low, p.EntryTrace.Zone.High = &s, &s
	p.EntryTrace.Chase.Final = &s
	p.EntryTrace.Stop.Price = &s
	p.EntryTrace.Targets.Target1, p.EntryTrace.Targets.Target2 = &s, &s

	got := mustProject(t, p)
	want := []store.Evidence{
		epText("ep_status", "NO_VALID_ENTRY", "entryplan.Plan"),
		epText("ep_rule_version", "EP6G-v1", "entryplan.Plan"),
		epText("ep_policy", "PULLBACK_ONLY", "entryplan.EntryTrace.Policy.Name"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("S1 plan:\n got %s\nwant %s", renderEv(got), renderEv(want))
	}
}

// The same rule through the real bridge and the real store, for all four sell-type Actions,
// with a SUITABLE valuation planted. Control: BUY on the same fixture persists price keys.
func TestEntryPlanSellContradictionThroughTheBridgeAndStore(t *testing.T) {
	ctx := context.Background()
	val := func() map[string]scanner.EntryPlanValuation {
		v := epSentinel
		return map[string]scanner.EntryPlanValuation{"2330": {Status: "AVAILABLE", BaseTarget: &v,
			Suitability: "SUITABLE", AsOf: epTestDate}}
	}
	record := func(action scanner.Action) (map[string]store.Evidence, *entryplan.Plan) {
		rec, _ := openRecorder(t)
		entries, _ := epBridgeEntries(t, "2330", action, true, val())
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: entries})
		if err != nil {
			t.Fatal(err)
		}
		return epStoredRows(t, rec.Store(), res.WatchlistSnapshots["2330"]), entries[0].EntryPlan
	}

	control, cp := record(scanner.ActionBuy)
	if cp == nil || cp.IdealEntry == nil || cp.Target2 == nil {
		t.Fatalf("anti-vacuity: BUY control plan has no prices: %+v", cp)
	}
	for _, k := range []string{"ep_zone_low", "ep_zone_high", "ep_target_2"} {
		if _, ok := control[k]; !ok {
			t.Fatalf("anti-vacuity: BUY control did not persist %s (rows %v)", k, sortedKeys(control))
		}
	}

	actions := []scanner.Action{scanner.ActionSell, scanner.ActionReduce, scanner.ActionTakeProfit, scanner.ActionStopLoss}
	ran := 0
	for _, a := range actions {
		rows, p := record(a)
		ran++
		if p == nil || p.EntryTrace == nil || p.EntryTrace.Decision.Rule != entryplan.RuleThesisContradicted {
			t.Fatalf("%s: plan not decided at S1: %+v", a, p)
		}
		if got := sortedKeys(rows); !reflect.DeepEqual(got, []string{"ep_policy", "ep_rule_version", "ep_status"}) {
			t.Errorf("%s: persisted keys %v, want only ep_policy, ep_rule_version, ep_status", a, got)
		}
		if r := rows["ep_status"]; r.TextValue == nil || *r.TextValue != "NO_VALID_ENTRY" {
			t.Errorf("%s: ep_status = %+v", a, r)
		}
		for k, r := range rows {
			if r.NumValue != nil {
				t.Errorf("%s: numeric row %s=%v on a sell-side plan", a, k, *r.NumValue)
			}
		}
	}
	if ran != 4 {
		t.Fatalf("ran %d of 4 sell-type Actions", ran)
	}
}

// ── 6 / 7. Target2: final published value only ────────────────────────────────────────────

func TestEntryPlanValuationCappedTarget2PersistsTheFinalValue(t *testing.T) {
	p := epFullPlan("2330", epTestDate)
	raw, ceiling, clamped := 121.3, 110.4, 110.4
	p.Target2 = &entryplan.PriceLevel{Price: 110}
	p.EntryTrace.Targets.Target2Raw, p.EntryTrace.Targets.ValuationTarget = &raw, &ceiling
	p.EntryTrace.Targets.Target2Clamped = &clamped
	p.EntryTrace.Targets.ValuationCeiling = entryplan.ValuationCeilingApplied
	rows := map[string]store.Evidence{}
	for _, e := range mustProject(t, p) {
		rows[e.Key] = e
	}
	if got := numOf(t, rows, "ep_target_2"); got != 110 {
		t.Fatalf("ep_target_2 = %v, want the published 110 (raw 121.3, ceiling 110.4)", got)
	}
}

func TestEntryPlanUncappedTarget2PersistsTheStructuralValue(t *testing.T) {
	p := epFullPlan("2330", epTestDate)
	raw := 117.5
	p.EntryTrace.Targets.Target2Raw, p.EntryTrace.Targets.Target2 = &raw, &raw
	p.EntryTrace.Targets.ValuationTarget, p.EntryTrace.Targets.Target2Clamped = nil, nil
	p.EntryTrace.Targets.ValuationCeiling = entryplan.ValuationCeilingUnavailable
	rows := map[string]store.Evidence{}
	for _, e := range mustProject(t, p) {
		rows[e.Key] = e
	}
	if got := numOf(t, rows, "ep_target_2"); got != 117.5 {
		t.Fatalf("ep_target_2 = %v, want structural 117.5", got)
	}
}

// Through the real bridge: a SUITABLE valuation below the structural Target2 caps it, and the
// store holds exactly the plan's final Target2 in both runs — not the trace's raw value.
func TestEntryPlanTarget2ThroughTheBridgeIsTheFinalPublishedValue(t *testing.T) {
	ctx := context.Background()
	plain, _ := epBridgeEntries(t, "2330", scanner.ActionBuy, true, nil)
	target := 140.3 // between the fixture's Target1 (132.5) and structural Target2 (152.5)
	capped, _ := epBridgeEntries(t, "2330", scanner.ActionBuy, true, map[string]scanner.EntryPlanValuation{
		"2330": {Status: "AVAILABLE", BaseTarget: &target, Suitability: "SUITABLE", AsOf: epTestDate}})
	pp, cp := plain[0].EntryPlan, capped[0].EntryPlan
	if pp == nil || cp == nil || pp.Target2 == nil || cp.Target2 == nil {
		t.Fatalf("fixture: missing Target2 (plain %+v, capped %+v)", pp, cp)
	}
	if cp.EntryTrace.Targets.ValuationCeiling != entryplan.ValuationCeilingApplied ||
		cp.Target2.Price == pp.Target2.Price || cp.EntryTrace.Targets.Target2Raw == nil ||
		*cp.EntryTrace.Targets.Target2Raw == cp.Target2.Price {
		t.Fatalf("anti-vacuity: valuation did not cap Target2 (ceiling %q, plain %v, capped %v)",
			cp.EntryTrace.Targets.ValuationCeiling, pp.Target2.Price, cp.Target2.Price)
	}
	for name, tc := range map[string]struct {
		entries []scanner.WatchlistEntry
		want    float64
	}{"plain": {plain, pp.Target2.Price}, "capped": {capped, cp.Target2.Price}} {
		rec, _ := openRecorder(t)
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: tc.entries})
		if err != nil {
			t.Fatal(err)
		}
		rows := epStoredRows(t, rec.Store(), res.WatchlistSnapshots["2330"])
		if got := numOf(t, rows, "ep_target_2"); got != tc.want {
			t.Errorf("%s: stored ep_target_2 = %v, want the plan's final %v", name, got, tc.want)
		}
	}
}

// ── 8. RuleVersion ────────────────────────────────────────────────────────────────────────

func TestEntryPlanRuleVersionIsCopiedFromThePlan(t *testing.T) {
	p := epFullPlan("2330", epTestDate)
	p.RuleVersion = "SYNTHETIC-EP7-v9"
	found := 0
	for _, e := range mustProject(t, p) {
		if e.Key == "ep_rule_version" {
			found++
			if e.TextValue == nil || *e.TextValue != "SYNTHETIC-EP7-v9" {
				t.Errorf("ep_rule_version = %v, want SYNTHETIC-EP7-v9 (the package constant is %s)",
					derefS(e.TextValue), entryplan.RuleVersion)
			}
		}
	}
	if found != 1 {
		t.Fatalf("ep_rule_version emitted %d times", found)
	}
	p.RuleVersion = ""
	if items, err := projectEntryPlan(p, epSnap("2330", epTestDate)); err == nil || items != nil {
		t.Fatalf("a plan with no rule version was persisted: %s", renderEv(items))
	}
}

// ── 9. policy ─────────────────────────────────────────────────────────────────────────────

func TestEntryPlanPolicyIsTheTracePolicyNameAsText(t *testing.T) {
	ran := 0
	for _, name := range []entryplan.PolicyName{entryplan.PolicyNormal, entryplan.PolicyDefensive, entryplan.PolicyUnresolved} {
		p := epFullPlan("2330", epTestDate)
		p.EntryTrace.Policy.Name = name
		// The trace's other policy labels are populated, so a projection reading the wrong one
		// produces a wrong value rather than only a missing row.
		p.EntryTrace.Policy.SemanticAllowance = entryplan.AllowanceAllowed
		p.EntryTrace.Policy.ChaseAllowance = entryplan.AllowanceDisallowed
		found := 0
		for _, e := range mustProject(t, p) {
			if e.Key != "ep_policy" {
				continue
			}
			found++
			if e.TextValue == nil || *e.TextValue != string(name) || e.NumValue != nil ||
				e.Source != "entryplan.EntryTrace.Policy.Name" {
				t.Errorf("%s: ep_policy row %s", name, renderEv([]store.Evidence{e}))
			}
		}
		if found != 1 {
			t.Errorf("%s: ep_policy emitted %d times", name, found)
		}
		ran++
	}
	if ran != 3 {
		t.Fatalf("ran %d of 3", ran)
	}
}

// ── 13. the registry ──────────────────────────────────────────────────────────────────────

func TestEntryPlanKeyRegistryIsExact(t *testing.T) {
	want := []string{"ep_status", "ep_rule_version", "ep_policy", "ep_zone_low", "ep_zone_high",
		"ep_max_chase", "ep_invalidation", "ep_target_1", "ep_target_2", "ep_rr_ratio"}
	if !reflect.DeepEqual(entryPlanEvidenceKeys, want) {
		t.Fatalf("registry = %v, want %v", entryPlanEvidenceKeys, want)
	}
	seen := map[string]bool{}
	for _, k := range entryPlanEvidenceKeys {
		if !strings.HasPrefix(k, "ep_") {
			t.Errorf("%q lacks the ep_ prefix", k)
		}
		if seen[k] {
			t.Errorf("%q registered twice", k)
		}
		seen[k] = true
	}
	if store.CategoryEntryPlan != "entry_plan" {
		t.Errorf("category = %q, want entry_plan", store.CategoryEntryPlan)
	}
	// The complete plan emits every registered key, each once, in registry order, all under the
	// one category — so no key is emitted that the registry does not name.
	got := mustProject(t, epFullPlan("2330", epTestDate))
	if !reflect.DeepEqual(keysOf(got), want) {
		t.Fatalf("emitted %v, want the registry in order", keysOf(got))
	}
	for _, e := range got {
		if e.Category != "entry_plan" {
			t.Errorf("%s written under category %q", e.Key, e.Category)
		}
	}
}

// ── 14. determinism ───────────────────────────────────────────────────────────────────────

func TestEntryPlanProjectionIsDeterministic(t *testing.T) {
	a := mustProject(t, epFullPlan("2330", epTestDate))
	b := mustProject(t, epFullPlan("2330", epTestDate))
	c := mustProject(t, epFullPlan("2330", epTestDate))
	if len(a) != 10 || !reflect.DeepEqual(a, b) || !reflect.DeepEqual(b, c) {
		t.Fatalf("projections differ:\n%s\n%s\n%s", renderEv(a), renderEv(b), renderEv(c))
	}

	// And through the store: the same input recorded twice stores identical ep_* rows, in the
	// store's deterministic (category, key) order, apart from ids and timestamps.
	ctx := context.Background()
	stored := func() []string {
		rec, _ := openRecorder(t)
		e := bareEntry("2330")
		e.EntryPlan = epFullPlan("2330", epTestDate)
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: []scanner.WatchlistEntry{e}})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := rec.Store().Evidence().ByCategory(ctx, res.WatchlistSnapshots["2330"], store.CategoryEntryPlan)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s|%s|%v|%v|%s|%s", r.Category, r.Key, derefF(r.NumValue),
				derefS(r.TextValue), r.Unit, r.Source))
		}
		return out
	}
	x, y := stored(), stored()
	if len(x) != 10 || !reflect.DeepEqual(x, y) {
		t.Fatalf("stored rows differ or are short:\n%v\n%v", x, y)
	}
}

// ── 15. point-in-time and run identity ────────────────────────────────────────────────────

func TestEntryPlanRowsBelongToTheirOwnRunSnapshotAndAsOf(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)
	var logs []string
	rec.WithLogger(func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	st := rec.Store()

	type runSpec struct {
		date    string
		t2      float64
		version string
	}
	specs := []runSpec{{"2026-09-10", 117.5, "RUN-A-v1"}, {"2026-09-11", 120.5, "RUN-B-v1"}}
	var results []Result
	for _, s := range specs {
		e := bareEntry("2330")
		p := epFullPlan("2330", s.date)
		p.Target2.Price, p.RuleVersion = s.t2, s.version
		e.EntryPlan = p
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: s.date}, Input{Watchlist: []scanner.WatchlistEntry{e}})
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, res)
	}
	if results[0].RunID == results[1].RunID {
		t.Fatal("fixture: both scans landed in one run")
	}
	for i, s := range specs {
		id := results[i].WatchlistSnapshots["2330"]
		snap, err := st.Scans().Snapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if snap.ScanRunID != results[i].RunID || snap.TradingDate != s.date {
			t.Errorf("run %d: snapshot run=%d date=%s, want run=%d date=%s", i, snap.ScanRunID, snap.TradingDate, results[i].RunID, s.date)
		}
		rows := epStoredRows(t, st, id)
		if len(rows) != 10 {
			t.Errorf("run %d: %d ep rows, want 10 (%v)", i, len(rows), sortedKeys(rows))
		}
		if got := numOf(t, rows, "ep_target_2"); got != s.t2 {
			t.Errorf("run %d: ep_target_2 = %v, want %v", i, got, s.t2)
		}
		if r := rows["ep_rule_version"]; r.TextValue == nil || *r.TextValue != s.version {
			t.Errorf("run %d: ep_rule_version = %v, want %s", i, derefS(r.TextValue), s.version)
		}
	}

	// A plan for ANOTHER session, or another symbol, is not recorded against this snapshot. The
	// snapshot and its other evidence still are, and the refusal is logged.
	for _, bad := range []struct{ name, symbol, asOf string }{
		{"stale AsOf", "2330", "2026-09-11"},
		{"future AsOf", "2330", "2026-09-13"},
		{"other symbol", "2317", "2026-09-12"},
	} {
		logs = nil
		e := bareEntry("2330")
		e.EntryPlan = epFullPlan(bad.symbol, bad.asOf)
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: "2026-09-12"}, Input{Watchlist: []scanner.WatchlistEntry{e}})
		if err != nil || res.Snapshots != 1 || res.Skipped != 0 {
			t.Fatalf("%s: res %+v err %v", bad.name, res, err)
		}
		id := res.WatchlistSnapshots["2330"]
		if rows := epStoredRows(t, st, id); len(rows) != 0 {
			t.Errorf("%s: %d ep rows recorded for a plan that is not this snapshot's: %v", bad.name, len(rows), sortedKeys(rows))
		}
		if _, ok := evidenceMap(t, st, id)["close"]; !ok {
			t.Errorf("%s: the snapshot's own evidence was lost with the plan", bad.name)
		}
		if !strings.Contains(strings.Join(logs, "\n"), "2330 entry plan not recorded: entry plan identity") {
			t.Errorf("%s: refusal not logged: %v", bad.name, logs)
		}
	}
}

// ── 15b. identity match is required; there is no session gating ──────────────────────────
//
// User decision 1: ep_* rows are written only when Plan.Symbol == snapshot Symbol AND
// Plan.AsOf == snapshot TradingDate. Otherwise zero ep_* rows; the plan is not re-dated, not
// stored under its bar date, and the snapshot keeps its own identity.
// User decision 2: no session gating. A same-symbol, same-date plan is recorded whatever the
// date is; persistence has no clock. These tests use no clock of their own.

// epEvidenceTableCounts reads the WHOLE evidence table with SQL, not through a snapshot-scoped
// repository call, so a row written under any snapshot, category or key spelling is counted.
func epEvidenceTableCounts(t *testing.T, st *store.Store) (epRows, allRows int) {
	t.Helper()
	err := st.WithTx(context.Background(), func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT
			COALESCE(SUM(CASE WHEN category = 'entry_plan' OR substr(key, 1, 3) = 'ep_' THEN 1 ELSE 0 END), 0),
			COUNT(*) FROM evidence`).Scan(&epRows, &allRows)
	})
	if err != nil {
		t.Fatal(err)
	}
	return epRows, allRows
}

// epRecordOne records one watchlist entry for runDate on a fresh store and returns the store,
// the result and the log lines.
func epRecordOne(t *testing.T, runDate string, plan *entryplan.Plan) (*store.Store, Result, []string) {
	t.Helper()
	rec, _ := openRecorder(t)
	var logs []string
	rec.WithLogger(func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	e := bareEntry("2330")
	e.EntryPlan = plan
	res, err := rec.RecordScan(context.Background(), RunMeta{TradingDate: runDate},
		Input{Watchlist: []scanner.WatchlistEntry{e}})
	if err != nil || res.Snapshots != 1 || res.Skipped != 0 {
		t.Fatalf("record %s: res %+v err %v", runDate, res, err)
	}
	return rec.Store(), res, logs
}

// epAssertRefused checks the store after a plan that must NOT be recorded: zero ep_* rows in
// the evidence table, the snapshot still there under the RUN's date and symbol, its own
// evidence still written, and the refusal logged.
func epAssertRefused(t *testing.T, name, runDate string, st *store.Store, res Result, logs []string) {
	t.Helper()
	ctx := context.Background()
	epRows, allRows := epEvidenceTableCounts(t, st)
	if epRows != 0 {
		t.Errorf("%s: evidence table holds %d ep_*/entry_plan rows, want 0", name, epRows)
	}
	if allRows == 0 {
		t.Errorf("%s: evidence table is empty — the snapshot's own evidence was lost with the plan", name)
	}
	id, ok := res.WatchlistSnapshots["2330"]
	if !ok {
		t.Fatalf("%s: no watchlist snapshot recorded", name)
	}
	snap, err := st.Scans().Snapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Symbol != "2330" || snap.TradingDate != runDate {
		t.Errorf("%s: snapshot identity %s/%s, want 2330/%s", name, snap.Symbol, snap.TradingDate, runDate)
	}
	if _, ok := evidenceMap(t, st, id)["close"]; !ok {
		t.Errorf("%s: the snapshot's own evidence was lost with the plan", name)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "2330 entry plan not recorded: entry plan identity") {
		t.Errorf("%s: refusal not logged: %v", name, logs)
	}
}

func TestEntryPlanAsOfMismatchPersistsNoEntryPlanRows(t *testing.T) {
	// ANTI-VACUITY: the same plan, same symbol, with AsOf equal to the run date, IS counted by
	// the same SQL. Without this a query that never matched would pass every case below.
	{
		st, _, _ := epRecordOne(t, "2026-09-11", epFullPlan("2330", "2026-09-11"))
		if epRows, _ := epEvidenceTableCounts(t, st); epRows != 10 {
			t.Fatalf("anti-vacuity: matching plan stored %d ep_* rows, want 10", epRows)
		}
	}
	// Every case has the SAME symbol on plan and snapshot; only AsOf differs.
	for _, tc := range []struct{ name, runDate, planAsOf string }{
		{"weekend run, previous Friday's plan", "2026-09-12", "2026-09-11"},
		{"pre-open run, previous session's plan", "2026-09-14", "2026-09-11"},
		{"plan dated after the snapshot", "2026-09-10", "2026-09-11"},
		{"plan with no AsOf", "2026-09-11", ""},
	} {
		plan := epFullPlan("2330", tc.planAsOf)
		st, res, logs := epRecordOne(t, tc.runDate, plan)
		epAssertRefused(t, tc.name, tc.runDate, st, res, logs)
		// The plan was not re-dated to make it fit.
		if plan.AsOf != tc.planAsOf {
			t.Errorf("%s: Plan.AsOf rewritten to %q, want %q", tc.name, plan.AsOf, tc.planAsOf)
		}
		// And not stored under its own bar date either: no snapshot exists for that date.
		if tc.planAsOf != "" {
			n := 0
			if err := st.WithTx(context.Background(), func(tx *sql.Tx) error {
				return tx.QueryRow(`SELECT COUNT(*) FROM stock_snapshots WHERE trading_date = ?`, tc.planAsOf).Scan(&n)
			}); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s: %d snapshots recorded under the plan's date %s", tc.name, n, tc.planAsOf)
			}
		}
	}
}

func TestEntryPlanSymbolMismatchPersistsNoEntryPlanRows(t *testing.T) {
	// ANTI-VACUITY, as above.
	{
		st, _, _ := epRecordOne(t, epTestDate, epFullPlan("2330", epTestDate))
		if epRows, _ := epEvidenceTableCounts(t, st); epRows != 10 {
			t.Fatalf("anti-vacuity: matching plan stored %d ep_* rows, want 10", epRows)
		}
	}
	// Every case has the SAME AsOf on plan and snapshot; only Symbol differs.
	for _, tc := range []struct{ name, planSymbol string }{
		{"another stock's plan", "2317"},
		{"suffixed symbol", "2330.TW"},
		{"plan with no symbol", ""},
	} {
		plan := epFullPlan(tc.planSymbol, epTestDate)
		st, res, logs := epRecordOne(t, epTestDate, plan)
		epAssertRefused(t, tc.name, epTestDate, st, res, logs)
		if plan.Symbol != tc.planSymbol {
			t.Errorf("%s: Plan.Symbol rewritten to %q", tc.name, plan.Symbol)
		}
	}
}

// Decision 2. The dates are chosen so that ANY rule comparing the snapshot date with a clock or
// a trading calendar refuses at least one of them: a far-past date, a far-future date (a
// "session not yet complete" rule), and a Saturday (a "not a trading day" rule). The test itself
// reads no clock. It does NOT catch a gate on the time of day; the structural half,
// TestEntryPlanPersistenceHasNoClockOrSessionReference, is what flags that shape.
func TestEntryPlanSameIdentityPlanIsPersistedWithoutSessionGating(t *testing.T) {
	ctx := context.Background()
	for _, date := range []string{epTestDate, "1990-01-02", "2099-12-31", "2026-09-12"} {
		plan := epFullPlan("2330", date)
		st, res, logs := epRecordOne(t, date, plan)
		if epRows, _ := epEvidenceTableCounts(t, st); epRows != 10 {
			t.Errorf("%s: evidence table holds %d ep_* rows, want 10 (logs %v)", date, epRows, logs)
		}
		rows := epStoredRows(t, st, res.WatchlistSnapshots["2330"])
		if len(rows) != 10 {
			t.Errorf("%s: snapshot carries %d ep_* rows, want 10", date, len(rows))
		}
		snap, err := st.Scans().Snapshot(ctx, res.WatchlistSnapshots["2330"])
		if err != nil {
			t.Fatal(err)
		}
		if snap.TradingDate != date || plan.AsOf != date {
			t.Errorf("%s: snapshot date %s, plan AsOf %s", date, snap.TradingDate, plan.AsOf)
		}
	}
}

// ── 16. disabled, end to end ──────────────────────────────────────────────────────────────

func TestEntryPlanDisabledWritesNoEvidenceEndToEnd(t *testing.T) {
	ctx := context.Background()
	run := func(enabled bool) (int, int) {
		rec, _ := openRecorder(t)
		entries, _ := epBridgeEntries(t, "2330", scanner.ActionBuy, enabled, nil)
		if (entries[0].EntryPlan != nil) != enabled {
			t.Fatalf("enabled=%v but EntryPlan nil=%v", enabled, entries[0].EntryPlan == nil)
		}
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: entries})
		if err != nil {
			t.Fatal(err)
		}
		id := res.WatchlistSnapshots["2330"]
		byCat, err := rec.Store().Evidence().ByCategory(ctx, id, store.CategoryEntryPlan)
		if err != nil {
			t.Fatal(err)
		}
		all, err := rec.Store().Evidence().BySnapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		prefixed := 0
		for _, r := range all {
			if strings.HasPrefix(r.Key, "ep_") {
				prefixed++
			}
		}
		return len(byCat), prefixed
	}
	if cat, pre := run(true); cat < 3 || pre != cat {
		t.Fatalf("anti-vacuity: enabled run stored %d entry_plan rows / %d ep_ keys", cat, pre)
	}
	if cat, pre := run(false); cat != 0 || pre != 0 {
		t.Fatalf("disabled run stored %d entry_plan rows / %d ep_ keys, want none", cat, pre)
	}
}

// ── 17. no trace, reasons, caveats or prose ───────────────────────────────────────────────

func TestEntryPlanPersistsNoTraceReasonsOrProse(t *testing.T) {
	ctx := context.Background()
	rec, _ := openRecorder(t)
	e := bareEntry("2330")
	e.EntryPlan = epFullPlan("2330", epTestDate)
	res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: []scanner.WatchlistEntry{e}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := rec.Store().Evidence().BySnapshot(ctx, res.WatchlistSnapshots["2330"])
	if err != nil {
		t.Fatal(err)
	}
	forbiddenText := []string{"PROSE-SENTINEL", "NOTE-SENTINEL", string(entryplan.ReasonStatusPriceAboveEntryZone),
		string(entryplan.InvariantCheckClean), string(entryplan.ConfidenceMedium), "TARGET_1", "CURRENT_PRICE"}
	epRows := 0
	for _, r := range rows {
		if r.Category != store.CategoryEntryPlan {
			continue
		}
		epRows++
		if r.NumValue != nil && *r.NumValue == epSentinel {
			t.Errorf("%s carries the trace/census sentinel %v", r.Key, epSentinel)
		}
		if r.TextValue != nil {
			for _, f := range forbiddenText {
				if strings.Contains(*r.TextValue, f) {
					t.Errorf("%s text %q contains %q", r.Key, *r.TextValue, f)
				}
			}
		}
	}
	if epRows != 10 {
		t.Fatalf("stored %d entry_plan rows, want exactly the 10 registry rows", epRows)
	}
}

// ── 18 (behaviour half). persistence changes nothing the scanner decided ─────────────────

func TestEntryPlanPersistenceDoesNotAlterScannerRecordsOrInputs(t *testing.T) {
	ctx := context.Background()
	record := func(withPlan bool) ([]string, []string, []scanner.WatchlistEntry) {
		rec, _ := openRecorder(t)
		entries := []scanner.WatchlistEntry{bareEntry("2330"), bareEntry("2317")}
		entries[1].RocketScore, entries[1].A.Action = 40, scanner.ActionSell
		if withPlan {
			entries[0].EntryPlan = epFullPlan("2330", epTestDate)
			entries[1].EntryPlan = epFullPlan("2317", epTestDate)
		}
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: entries})
		if err != nil {
			t.Fatal(err)
		}
		snaps, err := rec.Store().Scans().SnapshotsByRun(ctx, res.RunID)
		if err != nil {
			t.Fatal(err)
		}
		var snapRows, evRows []string
		for _, s := range snaps {
			snapRows = append(snapRows, fmt.Sprintf("%s|%s|%s|%v|%v|%s|%s", s.Symbol, s.SourceTab,
				s.ScannerAction, derefF(s.ScannerScore), *s.RocketScore, s.RocketStage, s.WatchAction))
			decisions, err := rec.Store().Analyses().DecisionsBySnapshot(ctx, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range decisions {
				snapRows = append(snapRows, s.Symbol+"|decision|"+d.Source+"|"+d.Decision)
			}
			rows, err := rec.Store().Evidence().BySnapshot(ctx, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.Category == store.CategoryEntryPlan {
					continue
				}
				evRows = append(evRows, fmt.Sprintf("%s|%s|%s|%v|%v", s.Symbol, r.Category, r.Key, derefF(r.NumValue), derefS(r.TextValue)))
			}
		}
		return snapRows, evRows, entries
	}

	offSnaps, offEv, _ := record(false)
	onSnaps, onEv, onEntries := record(true)
	if len(offSnaps) < 4 || len(offEv) < 20 {
		t.Fatalf("anti-vacuity: %d snapshot/decision rows, %d evidence rows", len(offSnaps), len(offEv))
	}
	if !reflect.DeepEqual(offSnaps, onSnaps) {
		t.Errorf("snapshots/decisions changed with plans present:\noff %v\n on %v", offSnaps, onSnaps)
	}
	if !reflect.DeepEqual(offEv, onEv) {
		t.Errorf("non-entry_plan evidence changed with plans present")
	}
	// The recorder must not write through the plan pointers it was handed.
	if !reflect.DeepEqual(onEntries[0].EntryPlan, epFullPlan("2330", epTestDate)) ||
		!reflect.DeepEqual(onEntries[1].EntryPlan, epFullPlan("2317", epTestDate)) {
		t.Error("RecordScan mutated a plan it persisted")
	}
	if onEntries[0].A.Symbol != "2330" || onEntries[1].A.Symbol != "2317" {
		t.Error("RecordScan reordered its input")
	}
}

// ── EP-8 review: hiding a section changes nothing here ────────────────────────────────────
//
// EP-8 decided that report section ⑲ renders only for an entry whose plan publishes a BUY-SIDE
// entry semantic, so an EXIT action (TAKE_PROFIT / REMOVE_FROM_WATCHLIST) shows no section. That
// is a PRESENTATION rule and this test is the persistence half of the proof: the same entry still
// carries a plan and still writes its ep_* rows through the real RecordScan. The report package
// cannot reach this path at all — internal/research does not import internal/report.
func TestEntryPlanExitActionEntryStillPersistsItsRows(t *testing.T) {
	ctx := context.Background()
	series := epCandles(90, 80, 0.25)

	record := func(watch scanner.WatchAction) (map[string]store.Evidence, *entryplan.Plan) {
		rec, _ := openRecorder(t)
		e := bareEntry("2330")
		e.A.Date = series[len(series)-1].Date
		e.A.Close = series[len(series)-1].Close
		e.A.ATR, e.A.MA20, e.A.AvgVolume20, e.A.Action = 2.5, 96, 12_000, scanner.ActionBuy
		e.Consol = scanner.Consolidation{Bucket: scanner.SwingBase, Days: 14, BaseLow: 92, PivotHigh: 104}
		e.WatchAction = watch
		entries := []scanner.WatchlistEntry{e}
		scanner.AttachEntryPlan(entries, map[string][]fetcher.Candle{"2330": series},
			scanner.EntryPlanMarket{Available: true, Regime: "BULL"}, true)
		res, err := rec.RecordScan(ctx, RunMeta{TradingDate: epTestDate}, Input{Watchlist: entries})
		if err != nil {
			t.Fatal(err)
		}
		return epStoredRows(t, rec.Store(), res.WatchlistSnapshots["2330"]), entries[0].EntryPlan
	}

	// Anti-vacuity: the buy-side control writes price rows, so "rows were written" means something.
	control, cp := record(scanner.ActBreakoutBuy)
	if cp == nil || cp.IdealEntry == nil {
		t.Fatalf("anti-vacuity: the BREAKOUT_BUY control has no zone: %+v", cp)
	}
	for _, k := range []string{"ep_zone_low", "ep_zone_high", "ep_status"} {
		if _, ok := control[k]; !ok {
			t.Fatalf("anti-vacuity: the control did not persist %s (rows %v)", k, sortedKeys(control))
		}
	}

	for _, watch := range []scanner.WatchAction{scanner.ActTakeProfit, scanner.ActRemove} {
		rows, p := record(watch)
		if p == nil {
			t.Fatalf("%s: the bridge left EntryPlan nil — the plan must still be attached", watch)
		}
		if p.Status != entryplan.StatusInsufficientData {
			t.Errorf("%s: plan status %q, want INSUFFICIENT_DATA (no entry semantic was handed over)",
				watch, p.Status)
		}
		// The rows an exit-action plan writes: status, rule version and policy. No price row,
		// because the plan publishes no price — exactly as before EP-8.
		if got := sortedKeys(rows); !reflect.DeepEqual(got, []string{"ep_policy", "ep_rule_version", "ep_status"}) {
			t.Errorf("%s: persisted keys %v, want ep_policy, ep_rule_version, ep_status", watch, got)
		}
		if r := rows["ep_status"]; r.TextValue == nil || *r.TextValue != "INSUFFICIENT_DATA" {
			t.Errorf("%s: ep_status = %+v", watch, r)
		}
		if r := rows["ep_rule_version"]; r.TextValue == nil || *r.TextValue != entryplan.RuleVersion {
			t.Errorf("%s: ep_rule_version = %+v", watch, r)
		}
	}
}
