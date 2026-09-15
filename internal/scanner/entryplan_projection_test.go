package scanner

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ──────────────────────────────────────────────────────────────────────────────
// EP-6C — THE PROJECTION OF THE SIX FIELDS EP-6A LEFT UNAVAILABLE
//
// Four are now projected (entry semantic, regime, previous close, adjustment age) and two are
// still absent on purpose (MA60, valuation). This file tests BOTH halves, because "absent"
// is a claim that rots the moment somebody fills the field with a plausible number: the tests
// below assert that MA60 and the valuation arrive as UNAVAILABLE WITH NO VALUE, not as zero.
//
// WHAT THESE TESTS DO NOT COVER, stated rather than implied:
//   - They do not test entryplan's rules. Whether a zone is legal, whether a status is
//     BUY_NOW, what the chase ceiling is — all of that belongs to internal/entryplan's own
//     suite and is frozen in EP-6C. What is tested here is what the BRIDGE hands over.
//   - They do not prove the production binary calls the bridge. That is cmd/scanner's
//     entryplan_pipeline_test.go (behavioural, through buildWatchlist).
//   - The WatchAction sweep reads rocket.go's SOURCE for the list of actions. It therefore
//     catches a new action nobody classified; it cannot catch an action declared somewhere
//     other than rocket.go.
// ──────────────────────────────────────────────────────────────────────────────

// epSeriesDate is the last session of every candle fixture in this file, and it matches
// epTestDate — the date epEntry's StockAnalysis claims. The alignment between the two is the
// subject of TestEntryPlanSeriesMustEndOnTheSessionTheAnalysisDescribes.
var epSeriesDate = epTestDate

// epSeries builds n daily candles ending exactly on epSeriesDate.
//
// closes[i] = first + step*i, and AdjClose == Close on every bar, which means the adjustment
// ratio is flat at 1.0 and fetcher.ScanAdjustments reports NO_ADJUSTED_SERIES — i.e.
// LastAdjustmentAge has NO ANSWER for this series. That is deliberate: it is the ordinary
// case in this repo (measured at 21.6% of the cached universe per adjustment.go), and it is
// the case where a caller that dropped the ok bool would publish "adjusted today".
func epSeries(n int, first, step float64) []fetcher.Candle {
	out := make([]fetcher.Candle, n)
	for i := 0; i < n; i++ {
		c := first + step*float64(i)
		out[i] = fetcher.Candle{
			Date:     epSeriesDate.AddDate(0, 0, i-(n-1)),
			Open:     c,
			High:     c + 1,
			Low:      c - 1,
			Close:    c,
			AdjClose: c,
			Volume:   1_000_000,
		}
	}
	return out
}

// epSeriesWithAdjustment is epSeries with a corporate action landing at index eventIdx: every
// bar BEFORE it carries AdjClose = Close*0.9 (ratio 1/0.9) and every bar from eventIdx on
// carries AdjClose = Close (ratio 1.0). fetcher.ScanAdjustments sees exactly one ratio step,
// at eventIdx, so LastAdjustmentAge answers n-1-eventIdx.
func epSeriesWithAdjustment(n, eventIdx int) []fetcher.Candle {
	out := epSeries(n, 80, 0.5)
	for i := 0; i < eventIdx; i++ {
		out[i].AdjClose = out[i].Close * 0.9
	}
	return out
}

// ── 1. THE ENTRY SEMANTIC ─────────────────────────────────────────────────────

// epSemanticExpectation is the arm-by-arm contract, written out as data so the sweep below
// can check it against rocket.go's own list of actions from both directions.
type epSemanticExpectation struct {
	status   entryplan.Availability
	semantic entryplan.EntrySemantic
	why      string
}

// epExpectedSemantics is EP-2's published mapping (internal/entryplan/policy.go, the
// EntrySemantic doc) restated as assertions. It is NOT this test's invention: every row cites
// the decision it is holding in place.
var epExpectedSemantics = map[WatchAction]epSemanticExpectation{
	ActPullbackBuy: {entryplan.Available, entryplan.EntrySemanticPullback,
		"EP-2: scanner.ActPullbackBuy (PULLBACK_BUY) → PULLBACK"},
	ActBreakoutBuy: {entryplan.Available, entryplan.EntrySemanticBreakout,
		"EP-2: scanner.ActBreakoutBuy (BREAKOUT_BUY) → BREAKOUT"},
	ActPrepare: {entryplan.Available, entryplan.EntrySemanticUnknown,
		"EP-2: PREPARE_ENTRY says a setup is forming, not which side of the market the " +
			"entry sits on — AVAILABLE because the scanner DID answer"},
	ActWatchClose: {entryplan.Available, entryplan.EntrySemanticUnknown,
		"EP-2: WATCH_CLOSELY → UNKNOWN"},
	ActWait: {entryplan.Available, entryplan.EntrySemanticUnknown,
		"EP-2: WAIT → UNKNOWN"},
	ActTakeProfit: {entryplan.Unavailable, "",
		"EP-2: TAKE_PROFIT is an EXIT — no entry semantic exists to hand over, and UNKNOWN " +
			"would falsely claim the scanner was asked about an entry"},
	ActRemove: {entryplan.Unavailable, "",
		"EP-2: REMOVE_FROM_WATCHLIST is an EXIT — same reasoning as TAKE_PROFIT"},
}

// Every WatchAction the scanner declares has an EXPLICIT projection, and the two lists are
// checked against each other in BOTH directions.
//
// The action list is read off rocket.go's SOURCE rather than retyped here, for the reason
// entryplan.AllEntrySemantics is checked from both ends: a retyped list pins the test and not
// the package, so adding an eighth WatchAction would leave the mapping silently falling into
// the default arm while a hand-written table kept passing.
func TestEntrySemanticProjectionCoversEveryDeclaredWatchAction(t *testing.T) {
	declared := watchActionsDeclaredInSource(t)

	// ANTI-VACUITY. Seven actions exist today; a sweep that found none (or a handful)
	// would pass while asserting nothing about the mapping.
	if len(declared) < 7 {
		t.Fatalf("only %d WatchAction constants found in rocket.go (%v) — the source sweep "+
			"is not seeing the declarations and every assertion below would be vacuous",
			len(declared), declared)
	}

	for _, a := range declared {
		want, ok := epExpectedSemantics[a]
		if !ok {
			t.Errorf("WatchAction %q is declared in rocket.go and has NO explicit row in "+
				"epExpectedSemantics. Whoever adds an action classifies it: decide whether "+
				"it is a PULLBACK, a BREAKOUT, an answered-but-shapeless entry (AVAILABLE + "+
				"UNKNOWN) or not an entry at all (UNAVAILABLE), and say so in "+
				"entryPlanEntrySemantic — the default arm must not decide it by accident", a)
			continue
		}
		got := entryPlanEntrySemantic(a)
		if got.Status != want.status || got.Semantic != want.semantic {
			t.Errorf("%s → %+v, want {Status:%s Semantic:%q}\n  %s",
				a, got, want.status, want.semantic, want.why)
		}
	}

	// The other direction: no row here may describe an action the scanner no longer has.
	for a := range epExpectedSemantics {
		var found bool
		for _, d := range declared {
			if d == a {
				found = true
			}
		}
		if !found {
			t.Errorf("epExpectedSemantics holds a row for %q, which rocket.go no longer "+
				"declares — the table has drifted away from the package it pins", a)
		}
	}
}

// watchActionsDeclaredInSource reads every `X WatchAction = "..."` constant out of rocket.go.
func watchActionsDeclaredInSource(t *testing.T) []WatchAction {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "rocket.go", nil, 0)
	if err != nil {
		t.Fatalf("parse rocket.go: %v — repoint this sweep at wherever WatchAction moved, "+
			"rather than dropping it", err)
	}
	var out []WatchAction
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "WatchAction" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				out = append(out, WatchAction(lit.Value[1:len(lit.Value)-1]))
			}
		}
	}
	return out
}

// The two NON-action inputs are explicit decisions too, not fallthrough.
func TestEntrySemanticProjectionRefusesToGuessAboutUnnamedActions(t *testing.T) {
	// An entry the rocket pass never touched. computeRocket sets ActWait even on fewer than
	// 30 candles, so "" means the pass did not run — nobody said anything.
	if got := entryPlanEntrySemantic(""); got.Status != entryplan.Unavailable || got.Semantic != "" {
		t.Errorf("an unset WatchAction → %+v, want UNAVAILABLE with no semantic: nobody said "+
			"anything, which is not the same as looking and being unable to say", got)
	}
	// An action added after this switch was written. It must NOT become UNKNOWN: that value
	// asserts the upstream layer looked and could not decide, and about a code we have never
	// seen we know nothing at all.
	got := entryPlanEntrySemantic(WatchAction("SOME_FUTURE_ACTION"))
	if got.Status != entryplan.Unavailable {
		t.Errorf("an unrecognised WatchAction → %+v, want UNAVAILABLE", got)
	}
	if got.Semantic == entryplan.EntrySemanticUnknown {
		t.Error("an unrecognised WatchAction was mapped to UNKNOWN, which claims the scanner " +
			"looked and could not say — the truth is that this adapter cannot read the code")
	}
	if got.Semantic.Resolved() {
		t.Errorf("an unrecognised WatchAction produced a RESOLVED semantic (%q), which EP-2's "+
			"policy table would price as a real entry", got.Semantic)
	}
}

// The mapping reaches the PLAN, not just the helper: every action produces the evidence row
// the projection promised, through the exported attach.
func TestEntrySemanticReachesTheEvidenceRow(t *testing.T) {
	var available, unavailable int
	for _, a := range watchActionsDeclaredInSource(t) {
		want := epExpectedSemantics[a]
		e := epEntry("2330")
		e.WatchAction = a
		entries := []WatchlistEntry{e}
		AttachEntryPlan(entries, epNoSeries, epNoMarket, true)

		row, ok := evidenceOf(entries[0].EntryPlan, entryplan.EvidenceEntrySemantic)
		if !ok {
			t.Fatalf("%s: the plan carries no ENTRY_SEMANTIC evidence row at all", a)
		}
		switch want.status {
		case entryplan.Available:
			available++
			if row.Text != string(want.semantic) {
				t.Errorf("%s: evidence row reads %q, want %q", a, row.Text, want.semantic)
			}
			// plan.go downgrades an AVAILABLE-but-UNRESOLVED semantic to
			// INSUFFICIENT_DATA on the row while keeping the text. Both readings are
			// "the scanner answered", which is what this arm is about.
			if row.Status != entryplan.Available && row.Status != entryplan.InsufficientData {
				t.Errorf("%s: evidence row status %q — the scanner DID answer for this "+
					"action, so the row must not read UNAVAILABLE", a, row.Status)
			}
		case entryplan.Unavailable:
			unavailable++
			if row.Status != entryplan.Unavailable {
				t.Errorf("%s: evidence row status %q, want UNAVAILABLE — this is an EXIT "+
					"action and no entry semantic exists for it", a, row.Status)
			}
			if row.Text != "" {
				t.Errorf("%s: evidence row carries semantic text %q for an exit action",
					a, row.Text)
			}
		}
	}
	// ANTI-VACUITY LEDGER: both arms must actually have been exercised, or a mapping that
	// collapsed every action into one answer would pass this loop.
	if available < 5 || unavailable < 2 {
		t.Fatalf("the sweep exercised %d available-semantic actions and %d unavailable ones; "+
			"today's mapping has 5 and 2, so these counts mean the loop is not covering both "+
			"arms", available, unavailable)
	}
}

// THE OVERHEATED CASE. An overheated-but-intact stock reaches this bridge as PULLBACK_BUY
// (rocket.go's watchActionFor: "simply extended in a healthy trend → wait for the pullback to
// enter"), and the bridge must project PULLBACK and nothing else.
//
// It must NOT read RocketStage to pre-empt entryplan's TOO_EXTENDED, whose condition is
// numeric and whose doc says aliasing the two "would destroy the ability to notice they
// disagree". This test pins that by giving two entries the SAME action and DIFFERENT stages
// and requiring the same projection.
func TestOverheatedStageDoesNotChangeTheProjectedSemantic(t *testing.T) {
	overheated := epEntry("2330")
	overheated.WatchAction = ActPullbackBuy
	overheated.RocketStage = StageOverheated

	healthy := epEntry("2330")
	healthy.WatchAction = ActPullbackBuy
	healthy.RocketStage = StageMainRun

	a := entryPlanSnapshotOf(&overheated, nil, epNoMarket).EntrySemantic
	b := entryPlanSnapshotOf(&healthy, nil, epNoMarket).EntrySemantic
	if a != b {
		t.Errorf("RocketStage changed the projected entry semantic: OVERHEATED → %+v, "+
			"MAIN_RUN → %+v. The stage is already resolved into the WatchAction by rocket.go; "+
			"reading it again here would be the bridge deciding a status", a, b)
	}
	if a.Semantic != entryplan.EntrySemanticPullback {
		t.Errorf("an overheated-but-intact PULLBACK_BUY projected %q, want PULLBACK", a.Semantic)
	}
}

// ── 2. THE MARKET REGIME ──────────────────────────────────────────────────────

// The three outcomes, and the distinction between the last two is the point.
func TestEntryPlanRegimeProjection(t *testing.T) {
	// Every regime the ONE vocabulary declares travels through unchanged when a snapshot
	// covered the session — including UNKNOWN, which is "computed, and the data could not
	// support a call" and is NOT the same fact as "never computed".
	real := []model.Regime{
		model.RegimeBull, model.RegimeBullPullback, model.RegimeSideways,
		model.RegimeDistribution, model.RegimeBear, model.RegimeUnknown,
	}
	var usable int
	for _, r := range real {
		got := entryPlanRegime(EntryPlanMarket{Available: true, Regime: string(r)})
		if got.Status != entryplan.Available {
			t.Errorf("%s came from a real snapshot and was projected %q, want AVAILABLE",
				r, got.Status)
		}
		if got.Regime != r {
			t.Errorf("%s was projected as %q — the regime was rewritten in transit", r, got.Regime)
		}
		if got.Usable() {
			usable++
		}
	}
	// ANTI-VACUITY: five of the six must be usable by entryplan (UNKNOWN is not), so a
	// projection that marked everything unusable would be caught here rather than pass.
	if usable != 5 {
		t.Errorf("%d of the six regimes are usable by entryplan, want 5 (every one except "+
			"UNKNOWN)", usable)
	}
	if entryPlanRegime(EntryPlanMarket{Available: true, Regime: string(model.RegimeUnknown)}).Usable() {
		t.Error("model.RegimeUnknown was projected as a usable regime — it is the honest " +
			"answer when the data cannot support a call, not a sixth market condition")
	}

	// No snapshot covered the session. UNAVAILABLE, and NOT a neutral market: SIDEWAYS
	// would permit pullback entries that nobody granted permission for.
	for _, m := range []EntryPlanMarket{
		{},
		{Available: false, Regime: string(model.RegimeBull)},
		{Available: true, Regime: ""},
	} {
		got := entryPlanRegime(m)
		if got.Status != entryplan.Unavailable || got.Regime != "" {
			t.Errorf("%+v → %+v, want UNAVAILABLE with no regime", m, got)
		}
		if got.Usable() {
			t.Errorf("%+v produced a usable regime", m)
		}
	}

	// A string this binary cannot interpret. Refused — never defaulted, and never mapped to
	// UNKNOWN (which would assert the market engine looked and could not decide).
	for _, s := range []string{"NEUTRAL", "bull", "RANGE_BOUND", "BULLISH"} {
		got := entryPlanRegime(EntryPlanMarket{Available: true, Regime: s})
		if got.Status != entryplan.Unavailable {
			t.Errorf("regime %q is not in model.Regime's vocabulary and was projected %q; "+
				"entryplan has no policy row for it", s, got.Status)
		}
		if got.Regime == model.RegimeUnknown {
			t.Errorf("regime %q was rewritten to UNKNOWN, which claims the market engine "+
				"looked and could not decide", s)
		}
	}
}

// The bridge NEVER derives a regime. Whatever else changes on the entry — sector stage,
// rocket stage, score, action — the projected regime is a function of the market argument
// alone.
func TestTheBridgeNeverDerivesARegimeFromTheStock(t *testing.T) {
	base := epEntry("2330")
	loud := epEntry("2330")
	loud.SectorStage = HotRotation
	loud.RocketStage = StageMainRun
	loud.A.Score = 99
	loud.A.Action = ActionBuy
	loud.SectorFlowDir = FlowInflow

	if got := entryPlanSnapshotOf(&loud, nil, epNoMarket).Regime; got.Status != entryplan.Unavailable {
		t.Errorf("a strongly-trending stock in a hot sector produced regime %+v with NO "+
			"market snapshot — a sector or stock stage has been substituted for the "+
			"market's regime, which keys EP-2's policy table on the wrong axis", got)
	}
	a := entryPlanSnapshotOf(&base, nil, EntryPlanMarket{Available: true, Regime: "BEAR"}).Regime
	b := entryPlanSnapshotOf(&loud, nil, EntryPlanMarket{Available: true, Regime: "BEAR"}).Regime
	if a != b {
		t.Errorf("the projected regime depends on the STOCK: %+v vs %+v", a, b)
	}
}

// The pair of TestAttachEntryPlanPublishesNoPriceWithoutARegime. Same fixture, same code
// path, one input added — and the plan becomes executable.
//
// It proves the regime projection is LOAD-BEARING rather than decorative: without this test
// the no-regime assertion could keep passing for a bridge that projected no regime at all.
func TestEntryPlanRegimeUnlocksTheSameFixture(t *testing.T) {
	entries := []WatchlistEntry{epEntry("2330")} // WatchAction is ActBreakoutBuy
	AttachEntryPlan(entries, epNoSeries,
		EntryPlanMarket{Available: true, Regime: string(model.RegimeBull)}, true)

	p := entries[0].EntryPlan
	row, ok := evidenceOf(p, entryplan.EvidenceMarketRegime)
	if !ok || row.Status != entryplan.Available || row.Text != "BULL" {
		t.Fatalf("MARKET_REGIME row = %+v (found=%v), want an AVAILABLE BULL", row, ok)
	}
	if !p.HasExecutablePrices() {
		t.Fatalf("with a BULL regime, a BREAKOUT semantic and a pivot high of 104, EP-3's "+
			"rules had everything they need and the plan still carries no price: %+v", p)
	}
	if p.IdealEntry == nil {
		t.Fatal("no ideal entry zone")
	}
	// The zone is built AROUND THE PIVOT the scan produced, which is the level EP-6C
	// projected. 104 is epEntry's Consol.PivotHigh.
	if p.IdealEntry.Low != 104 {
		t.Errorf("breakout zone = [%v, %v], want a band starting at the scan's own pivot "+
			"high of 104", p.IdealEntry.Low, p.IdealEntry.High)
	}
	if p.Status != entryplan.StatusWaitBreakout {
		t.Errorf("status %q, want WAIT_BREAKOUT: the legal entry sits above the market at "+
			"104 while the last close is 100", p.Status)
	}
}

// ── 3. THE PREVIOUS CLOSE ─────────────────────────────────────────────────────

// It is READ off the series, never reconstructed, and a missing one is an ABSENCE.
func TestEntryPlanPreviousCloseIsReadFromTheSeries(t *testing.T) {
	series := epSeries(40, 80, 0.5) // closes 80.0 .. 99.5, so [n-2] = 99.0
	e := epEntry("2330")
	snap := entryPlanSnapshotOf(&e, series, epNoMarket)

	if !snap.PreviousClose.Usable() {
		t.Fatalf("previous close = %+v, want the series' second-to-last close", snap.PreviousClose)
	}
	if want := series[len(series)-2].Close; *snap.PreviousClose.Value != want {
		t.Errorf("previous close = %v, want %v (series[n-2].Close)",
			*snap.PreviousClose.Value, want)
	}
	if snap.PreviousClose.PriceBasis != entryplan.PriceBasisRaw {
		t.Errorf("previous close basis %q, want RAW — it is an exchange close",
			snap.PreviousClose.PriceBasis)
	}
	// IT IS NOT THE CURRENT PRICE. A daily band built from today's price moves with the
	// thing it is supposed to bound, and the two fields are never interchangeable.
	if snap.CurrentPrice.Value != nil && *snap.PreviousClose.Value == *snap.CurrentPrice.Value {
		t.Errorf("previous close equals the current price (%v) — the fixture can no longer "+
			"tell a read from a substitution", *snap.CurrentPrice.Value)
	}

	// MISSING ≠ ZERO, three ways.
	for name, s := range map[string][]fetcher.Candle{
		"no series at all": nil,
		"a single bar":     epSeries(1, 90, 0),
		"a zero previous close": func() []fetcher.Candle {
			s := epSeries(10, 90, 0.5)
			s[len(s)-2].Close = 0
			return s
		}(),
	} {
		got := entryPlanPreviousClose(entryPlanSeriesFor(&e, s))
		if got.Status != entryplan.Unavailable || got.Value != nil {
			t.Errorf("%s → %+v, want UNAVAILABLE with no value. A previous close of 0 makes "+
				"pricerule.LimitUpPrice produce a ceiling of 0, which is finite, positive-"+
				"adjacent and forbids every purchase", name, got)
		}
	}
}

// ── 4. THE ADJUSTMENT AGE ─────────────────────────────────────────────────────

// LastAdjustmentAge's answer is carried VERBATIM, and its "no answer" is a NIL pointer.
func TestEntryPlanAdjustmentAgeIsCarriedVerbatim(t *testing.T) {
	e := epEntry("2330")

	// A series with one detectable corporate action, 7 bars before the end.
	const n, eventIdx = 40, 33
	adjusted := epSeriesWithAdjustment(n, eventIdx)
	wantAge, ok := fetcher.LastAdjustmentAge(adjusted)
	if !ok {
		t.Fatalf("the fixture produced no detectable adjustment, so this test would assert " +
			"nothing about the carried value")
	}
	if wantAge != n-1-eventIdx {
		t.Fatalf("fixture sanity: LastAdjustmentAge says %d bars ago, the event was planted "+
			"at %d bars ago", wantAge, n-1-eventIdx)
	}

	got := entryPlanSnapshotOf(&e, adjusted, epNoMarket).AdjustmentAge
	if !got.Known() {
		t.Fatalf("the age was dropped: %+v", got)
	}
	if *got.BarsAgo != wantAge {
		t.Errorf("adjustment age = %d, want %d — fetcher.LastAdjustmentAge's answer must be "+
			"carried verbatim, not re-derived", *got.BarsAgo, wantAge)
	}
	// And it reaches the plan's evidence census.
	entries := []WatchlistEntry{e}
	AttachEntryPlan(entries, map[string][]fetcher.Candle{"2330": adjusted}, epNoMarket, true)
	row, found := evidenceOf(entries[0].EntryPlan, entryplan.EvidenceAdjustmentAge)
	if !found || row.Status != entryplan.Available || row.Value == nil ||
		int(*row.Value) != wantAge {
		t.Errorf("ADJUSTMENT_AGE evidence row = %+v (found=%v), want %d",
			row, found, wantAge)
	}

	// ok == false must become a NIL pointer, never 0. A flat-at-one ratio (AdjClose == Close
	// on every bar) is the repo's most common "no answer", and 0 is also the most alarming
	// real answer — "the newest bar IS the adjustment".
	flat := epSeries(40, 80, 0.5)
	if _, ok := fetcher.LastAdjustmentAge(flat); ok {
		t.Fatal("the flat fixture unexpectedly HAS an answer; the assertion below would be " +
			"testing the wrong branch")
	}
	none := entryPlanSnapshotOf(&e, flat, epNoMarket).AdjustmentAge
	if none.Known() {
		t.Errorf("a series with no detectable adjustment produced an age of %d — "+
			"LastAdjustmentAge's ok=false returns 0 alongside it, and publishing that 0 "+
			"would report 'adjusted today' for a series nobody could measure", *none.BarsAgo)
	}
	if none.BarsAgo != nil {
		t.Error("the absent age is not a nil pointer")
	}
	// No series offered at all: same absence, no panic.
	if entryPlanAdjustmentAge(nil).Known() {
		t.Error("a nil series produced an adjustment age")
	}
}

// ── 5. THE SERIES ALIGNMENT GUARD ─────────────────────────────────────────────

// A series that does not END on the session the analysis describes is refused OUTRIGHT.
//
// This is the lookahead guard. With a longer series in hand, series[n-2] is a bar from the
// plan's FUTURE and LastAdjustmentAge measures from the wrong end — both produce plausible
// numbers that no assertion on the value itself could catch.
func TestEntryPlanSeriesMustEndOnTheSessionTheAnalysisDescribes(t *testing.T) {
	e := epEntry("2330") // A.Date == epTestDate == epSeriesDate

	aligned := epSeries(30, 80, 0.5)
	if entryPlanSeriesFor(&e, aligned) == nil {
		t.Fatal("the aligned fixture was refused, so every assertion below would pass for " +
			"the wrong reason")
	}

	// One extra bar AFTER the analysis date — the shape a later fetch or an uncut replay
	// slice produces.
	future := epSeries(31, 80, 0.5)
	for i := range future {
		future[i].Date = future[i].Date.AddDate(0, 0, 1)
	}
	if got := entryPlanSeriesFor(&e, future); got != nil {
		t.Errorf("a series ending %s was accepted for an analysis dated %s — the previous "+
			"close would come from the plan's future",
			future[len(future)-1].Date.Format("2006-01-02"), e.A.Date.Format("2006-01-02"))
	}
	// A series ending BEFORE the analysis date is refused too: it is stale, and its
	// "previous close" is two or more sessions old.
	stale := epSeries(30, 80, 0.5)
	for i := range stale {
		stale[i].Date = stale[i].Date.AddDate(0, 0, -3)
	}
	if got := entryPlanSeriesFor(&e, stale); got != nil {
		t.Error("a stale series was accepted")
	}

	// And the refusal shows up as ABSENCE on the snapshot, not as a wrong number.
	snap := entryPlanSnapshotOf(&e, future, epNoMarket)
	if snap.PreviousClose.Status != entryplan.Unavailable || snap.PreviousClose.Value != nil {
		t.Errorf("a misaligned series still produced a previous close: %+v", snap.PreviousClose)
	}
	if snap.AdjustmentAge.Known() {
		t.Error("a misaligned series still produced an adjustment age")
	}

	// An entry with no date at all cannot be aligned against anything.
	undated := epEntry("2330")
	undated.A.Date = time.Time{}
	if entryPlanSeriesFor(&undated, aligned) != nil {
		t.Error("an undated entry accepted a series — there is nothing to align it to")
	}
}

// ── 6. THE TWO FIELDS THAT ARE STILL UNAVAILABLE ──────────────────────────────

// MISSING ≠ ZERO for the two gaps EP-6C did not close.
//
// They must arrive as UNAVAILABLE WITH NO VALUE. An MA60 of 0 is a pullback candidate below
// every price ever quoted; a valuation target of 0 is a ceiling that would cap every target
// at zero. Both survive every downstream finite/positive check by being refused there — which
// is exactly why the refusal must be asserted HERE, where the substitution would be made.
func TestMA60AndValuationAreUnavailableAndNotZero(t *testing.T) {
	e := epEntry("2330")
	// Handed EVERYTHING this pass can be handed: a full aligned series and a real regime.
	// If an MA60 were ever going to be computed here, this is the call that would do it.
	snap := entryPlanSnapshotOf(&e, epSeries(90, 80, 0.5),
		EntryPlanMarket{Available: true, Regime: string(model.RegimeBull)})

	if snap.MA60.Status != entryplan.Unavailable {
		t.Errorf("MA60 status %q, want UNAVAILABLE. SMA(closes, 60) is computed in four "+
			"places in this package (horizonhint.go:187 uses it as a pullback support), but "+
			"none of them publishes the LEVEL onto indicator.Result or StockAnalysis, so a "+
			"value here is one the bridge computed itself", snap.MA60.Status)
	}
	if snap.MA60.Value != nil {
		t.Errorf("MA60 carries the value %v. If a real MA60 now exists, it belongs to "+
			"internal/indicator and StockAnalysis first; if this is 0, it is a fabricated "+
			"support level that every downstream check would accept", *snap.MA60.Value)
	}
	if snap.MA60.Usable() {
		t.Error("MA60 is usable as a zone centre")
	}

	if snap.Valuation.Status != entryplan.Unavailable {
		t.Errorf("valuation status %q, want UNAVAILABLE: loadResearchViews runs after "+
			"buildWatchlist returns, so no valuation has been read when this pass runs",
			snap.Valuation.Status)
	}
	if snap.Valuation.BaseTargetPrice != nil {
		t.Errorf("valuation carries a base target of %v", *snap.Valuation.BaseTargetPrice)
	}
	if snap.Valuation.Suitability != "" {
		t.Errorf("valuation carries a suitability verdict %q that nobody computed",
			snap.Valuation.Suitability)
	}
	if snap.Valuation.Usable() {
		t.Error("the valuation is usable as a target ceiling")
	}
}

// ── 7. THE LEVELS THE BRIDGE PROJECTS ARE THE SCAN'S OWN ──────────────────────

// PivotHigh is Consolidation.PivotHigh and nothing else.
//
// The fixture gives every price on the entry a DIFFERENT value (close 100, MA20 96, base low
// 92, pivot 104), so reading the wrong field is detectable rather than a coincidence. Per
// EP-6C's decision the projected level is consolidation.go's 60-bar high — the scan's own
// 突破價 — which is documented at the field rather than silently relabelled.
func TestEntryPlanLevelsAreTheScannersOwnFields(t *testing.T) {
	e := epEntry("2330")
	snap := entryPlanSnapshotOf(&e, nil, epNoMarket)

	pairs := []struct {
		name string
		got  entryplan.PriceObservation
		want float64
	}{
		{"PivotHigh ← Consol.PivotHigh", snap.PivotHigh, e.Consol.PivotHigh},
		{"BaseLow ← Consol.BaseLow", snap.BaseLow, e.Consol.BaseLow},
		{"MA20 ← StockAnalysis.MA20", snap.MA20, e.A.MA20},
		{"CurrentPrice ← StockAnalysis.Close", snap.CurrentPrice, e.A.Close},
	}
	for _, p := range pairs {
		if !p.got.Usable() {
			t.Fatalf("%s: not projected at all (%+v)", p.name, p.got)
		}
		if *p.got.Value != p.want {
			t.Errorf("%s: projected %v, want %v — a different field of the same entry has "+
				"been read", p.name, *p.got.Value, p.want)
		}
	}

	// The pivot MOVES WITH ITS SOURCE. A projection that had been repointed at another
	// price would keep matching the table above only until the source changed.
	moved := epEntry("2330")
	moved.Consol.PivotHigh = 117.5
	got := entryPlanSnapshotOf(&moved, nil, epNoMarket).PivotHigh
	if got.Value == nil || *got.Value != 117.5 {
		t.Errorf("changing Consol.PivotHigh to 117.5 produced %+v — the projection is not "+
			"reading that field", got)
	}
	// And it is not any of the other prices on the entry.
	for name, other := range map[string]float64{
		"Close": moved.A.Close, "MA20": moved.A.MA20, "BaseLow": moved.Consol.BaseLow,
		"BreakoutPrice": moved.BreakoutPrice, "SupportPrice": moved.SupportPrice,
	} {
		if got.Value != nil && *got.Value == other && other != 117.5 {
			t.Errorf("the projected pivot high equals %s (%v)", name, other)
		}
	}
}
