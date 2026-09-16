package study

import (
	"math"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────────────────
// EP-9 §34 — EXECUTION, twelve behavioural items over the fill model.
// ──────────────────────────────────────────────────────────────────────────────────────────

// E1 — a resting limit fills at the LIMIT when the market comes down to it intraday.
func TestE1AnIntradayTouchFillsAtTheLimit(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 99, 100, 94, 98}, // opens above the limit, trades through it
	)
	f := SimulateLimit(bars, s, "2026-09-10", 95, 3)
	if !f.Filled() || f.Price != 95 {
		t.Fatalf("fill %+v, want price 95 — the order rests and the market comes to it", f)
	}
}

// E2 — a GAP DOWN fills at the OPEN, which is better than the limit.
func TestE2AGapDownFillsAtTheOpenNotAtTheLimit(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 90, 92, 88, 91}, // gaps below the limit
	)
	f := SimulateLimit(bars, s, "2026-09-10", 95, 3)
	if !f.Filled() {
		t.Fatal("no fill on a gap through the limit")
	}
	if f.Price != 90 {
		t.Fatalf("filled at %v, want the open 90 — a resting buy limit executes at the open "+
			"when the market gaps below it, and 95 would understate the position's advantage",
			f.Price)
	}
}

// E3 — an exact touch at the limit fills; one tick above does not.
func TestE3TheTouchIsInclusiveAndOneTickAboveIsNot(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 99, 100, 95, 98},
	)
	if f := SimulateLimit(bars, s, "2026-09-10", 95, 3); !f.Filled() {
		t.Fatal("Low exactly at the limit did not fill")
	}
	if f := SimulateLimit(bars, s, "2026-09-10", 94.9, 3); f.Filled() {
		t.Fatalf("filled at a limit of 94.9 when the Low was 95: %+v", f)
	}
}

// E4 — FIRST ELIGIBLE FILL WINS; a better price later in the window is not taken.
func TestE4TheFirstEligibleSessionWinsAndALaterBetterPriceIsNotTaken(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 99, 100, 95, 98}, // fills at 95
		row{"2026-09-14", 80, 81, 70, 75},  // much better, but the order is already done
	)
	f := SimulateLimit(bars, s, "2026-09-10", 95, 5)
	if f.BarIndex != 1 {
		t.Fatalf("filled on bar %d, want bar 1 — taking bar 2's better price would be a "+
			"choice made with knowledge of a bar the order could not have seen", f.BarIndex)
	}
	if f.Price != 95 {
		t.Fatalf("price %v, want 95", f.Price)
	}
}

// E5 — the wait window bounds the search: W=1 sees only T+1.
func TestE5TheWaitWindowBoundsTheSearch(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 99, 100}, // does not reach
		row{"2026-09-14", 100, 101, 90, 100}, // reaches, but only within W>=2
	)
	if f := SimulateLimit(bars, s, "2026-09-10", 95, 1); f.Outcome != FillMissed {
		t.Fatalf("W=1 gave %+v, want MISSED", f)
	}
	f := SimulateLimit(bars, s, "2026-09-10", 95, 2)
	if !f.Filled() || f.SessionsWaited != 2 {
		t.Fatalf("W=2 gave %+v, want a fill after 2 sessions", f)
	}
}

// E6 — the window is SESSIONS, so a weekend is stepped over rather than counted.
func TestE6TheWindowCountsSessionsNotCalendarDays(t *testing.T) {
	// 2026-09-11 is a Friday and 2026-09-14 the following Monday; there is no weekend bar.
	bars, s := series(
		row{"2026-09-11", 100, 101, 99, 100}, // Friday, the signal
		row{"2026-09-14", 100, 101, 90, 100}, // Monday
	)
	f := SimulateLimit(bars, s, "2026-09-11", 95, 1)
	if !f.Filled() {
		t.Fatal("a Friday signal did not fill on the following Monday with W=1 — the window " +
			"is counting calendar days")
	}
	if f.Date != "2026-09-14" {
		t.Fatalf("filled on %s, want 2026-09-14", f.Date)
	}
}

// E7 — no zone means NOT_APPLICABLE, not MISSED.
func TestE7NoPublishedPriceIsNotApplicableRatherThanMissed(t *testing.T) {
	bars, s := flatSeries(30, 100)
	o := obsFor(bars, s, s.dates[0], nil, nil, nil, nil, nil)
	if r := Evaluate(o, ArmZoneLimit, 5); r.Fill.Outcome != FillNotApplicable {
		t.Fatalf("no zone gave %q, want NOT_APPLICABLE — there was never an order to miss",
			r.Fill.Outcome)
	}
	if r := Evaluate(o, ArmChase, 5); r.Fill.Outcome != FillNotApplicable {
		t.Fatalf("no chase ceiling gave %q, want NOT_APPLICABLE — the SIDEWAYS policy REFUSES "+
			"to chase, and a refusal is not a failure to fill", r.Fill.Outcome)
	}
}

// E8 — NOT_APPLICABLE is outside the fill-rate denominator; MISSED is inside it.
func TestE8NotApplicableIsOutsideTheDenominatorAndMissedIsInside(t *testing.T) {
	bars, s := flatSeries(30, 100)
	na := Evaluate(obsFor(bars, s, s.dates[0], nil, nil, nil, nil, nil), ArmZoneLimit, 5)
	miss := Evaluate(obsFor(bars, s, s.dates[0], ptr(50.0), nil, nil, nil, nil), ArmZoneLimit, 5)
	hit := Evaluate(obsFor(bars, s, s.dates[0], ptr(100.0), nil, nil, nil, nil), ArmZoneLimit, 5)
	m := Aggregate(ArmZoneLimit, 5, "", []Row{na, miss, hit})
	if m.NCandidate != 2 {
		t.Fatalf("NCandidate %d, want 2 — NOT_APPLICABLE must not inflate the denominator",
			m.NCandidate)
	}
	if m.NNotApplicable != 1 {
		t.Fatalf("NNotApplicable %d, want 1", m.NNotApplicable)
	}
	if m.FillRate.Denominator != 2 || m.FillRate.Hits != 1 || m.FillRate.Pct != 50 {
		t.Fatalf("fill rate %+v, want 1/2 = 50%%", m.FillRate)
	}
	if m.MissedRate.Hits != 1 || m.MissedRate.Denominator != 2 {
		t.Fatalf("missed rate %+v, want 1/2", m.MissedRate)
	}
}

// E9 — arm C fills a SUPERSET of arm B, at the same price or worse.
func TestE9TheChaseArmFillsASupersetOfTheZoneArmAtNoBetterPrice(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 100, 101, 97, 99},
		row{"2026-09-14", 100, 101, 94, 96},
	)
	o := obsFor(bars, s, "2026-09-10", ptr(95.0), ptr(98.0), nil, nil, nil)
	b := Evaluate(o, ArmZoneLimit, 5)
	c := Evaluate(o, ArmChase, 5)
	if !c.Fill.Filled() {
		t.Fatal("the chase arm did not fill at a ceiling of 98 with a Low of 97")
	}
	if c.Fill.BarIndex > b.Fill.BarIndex && b.Fill.Filled() {
		t.Fatal("the chase arm filled LATER than the zone arm")
	}
	if c.Fill.Price < b.Fill.Price && b.Fill.Filled() {
		t.Fatalf("the chase arm filled at %v, better than the zone arm's %v",
			c.Fill.Price, b.Fill.Price)
	}
	if !b.Fill.Filled() && c.Fill.Filled() && c.Fill.Price <= 95 {
		t.Fatalf("a chase-only fill at %v is inside the zone, which the zone arm should have "+
			"taken", c.Fill.Price)
	}
}

// E10 — an unusable bar stops the scan as UNAVAILABLE rather than being read as "did not reach".
func TestE10AnUnusableBarIsUnavailableNotNoFill(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-11", 0, 0, 0, 0},
		row{"2026-09-14", 100, 101, 90, 100},
	)
	f := SimulateLimit(bars, s, "2026-09-10", 95, 5)
	if f.Outcome != FillUnavailable {
		t.Fatalf("outcome %q, want UNAVAILABLE — a bar with no quote cannot be shown not to "+
			"have reached the limit", f.Outcome)
	}
	m := Aggregate(ArmZoneLimit, 5, "", []Row{{Obs: &Observation{}, Fill: f, ExecutableStatus: true}})
	if m.NUnavailable != 1 || m.NMissed != 0 {
		t.Fatalf("aggregate counted %d unavailable / %d missed, want 1/0", m.NUnavailable, m.NMissed)
	}
}

// E11 — a session the symbol never traded is UNAVAILABLE, never snapped to a neighbour.
func TestE11ASessionTheSymbolNeverTradedIsUnavailable(t *testing.T) {
	bars, s := series(
		row{"2026-09-10", 100, 101, 99, 100},
		row{"2026-09-14", 100, 101, 90, 100},
	)
	if f := SimulateLimit(bars, s, "2026-09-11", 95, 5); f.Outcome != FillUnavailable {
		t.Fatalf("outcome %q for a date with no bar, want UNAVAILABLE", f.Outcome)
	}
}

// E12 — the fill-vs-signal-close measure is signed the way a buyer reads it, with
// HAND-COMPUTED values, and the convention string says the same thing the arithmetic does.
//
// This is EP-9 §28 Q4's number. A reader who takes the sign backwards reads the study's main
// execution result backwards, so both halves are pinned: the arithmetic AND the sentence that
// is printed beside it.
func TestE12FillVsSignalCloseIsSignedForABuyer(t *testing.T) {
	cases := []struct {
		name             string
		fill, close, bps float64
	}{
		// (fill/close - 1) * 10000, computed by hand.
		{"1% above the close is +100 bps", 101, 100, 100},
		{"1% below the close is -100 bps", 99, 100, -100},
		{"3.61% below the close is -361 bps (the study's arm B figure)", 96.39, 100, -361},
		{"filled exactly at the close is 0 bps", 100, 100, 0},
		{"half a percent above on a 200 stock", 201, 200, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := FillVsSignalCloseBps(c.fill, c.close)
			if !ok {
				t.Fatalf("fill %v close %v was refused", c.fill, c.close)
			}
			if math.Abs(got-c.bps) > 1e-6 {
				t.Fatalf("fill %v vs close %v = %v bps, want %v", c.fill, c.close, got, c.bps)
			}
		})
	}
	// The DIRECTION, stated as the property a reader depends on.
	cheap, _ := FillVsSignalCloseBps(90, 100)
	dear, _ := FillVsSignalCloseBps(110, 100)
	if cheap >= 0 {
		t.Fatalf("a CHEAPER fill (90 vs 100) gave %v bps; it must be NEGATIVE", cheap)
	}
	if dear <= 0 {
		t.Fatalf("a DEARER fill (110 vs 100) gave %v bps; it must be POSITIVE", dear)
	}
	if _, ok := FillVsSignalCloseBps(100, 0); ok {
		t.Fatal("a zero signal close produced a basis-point figure")
	}

	// And the printed sentence must agree with the arithmetic above. A convention that drifts
	// from the code is worse than no convention, because it is believed.
	c := FillVsSignalCloseBpsConvention
	for _, want := range []string{"NEGATIVE", "BELOW", "BETTER FOR A BUYER", "-361", "3.61%",
		"POSITIVE", "ABOVE", "PRICE, not return"} {
		if !strings.Contains(c, want) {
			t.Fatalf("the printed convention never says %q:\n%s", want, c)
		}
	}
}

// ── integrity, driven rather than described ───────────────────────────────────────────────

func TestTheIntegrityCheckerCatchesAPlantedDefectOfEachKind(t *testing.T) {
	bars, s := flatSeries(30, 100)
	cases := []struct {
		name string
		mut  func(*Observation)
		want IntegrityCode
	}{
		{"inverted zone", func(o *Observation) { lo := 200.0; o.ZoneLow = &lo }, IntegrityZoneInverted},
		{"chase below zone", func(o *Observation) { c := 50.0; o.MaxChase = &c }, IntegrityChaseBelowZone},
		{"stop above entry", func(o *Observation) { v := 200.0; o.Invalidation = &v }, IntegrityStopAboveEntry},
		{"target below entry", func(o *Observation) { v := 50.0; o.Target1 = &v }, IntegrityTargetBelowEntry},
		{"impossible price", func(o *Observation) { v := -1.0; o.Target2 = &v }, IntegrityImpossiblePrice},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := obsFor(bars, s, s.dates[0], ptr(100.0), nil, nil, nil, nil)
			c.mut(o)
			rep := NewIntegrityReport(10)
			row := Evaluate(o, ArmZoneLimit, 5)
			rep.Check(&row, entryplanbacktest.NewObservationSet(), o.RegimeProvenance)
			if rep.ByCode[c.want] == 0 {
				t.Fatalf("planted %s was not caught; report %+v", c.want, rep.ByCode)
			}
		})
	}
}

func TestADuplicateObservationIsADefectAndIsRejected(t *testing.T) {
	bars, s := flatSeries(30, 100)
	o := obsFor(bars, s, s.dates[0], ptr(100.0), nil, nil, nil, nil)
	rep := NewIntegrityReport(10)
	ids := entryplanbacktest.NewObservationSet()
	row := Evaluate(o, ArmZoneLimit, 5)
	rep.Check(&row, ids, o.RegimeProvenance)
	rep.Check(&row, ids, o.RegimeProvenance)
	if rep.ByCode[IntegrityDuplicateIdentity] != 1 {
		t.Fatalf("duplicate count %d, want 1", rep.ByCode[IntegrityDuplicateIdentity])
	}
	if ids.Len() != 1 {
		t.Fatalf("the registry holds %d identities after a duplicate, want 1", ids.Len())
	}
}

func TestMixingRegimeProvenanceIsADefect(t *testing.T) {
	bars, s := flatSeries(30, 100)
	o := obsFor(bars, s, s.dates[0], ptr(100.0), nil, nil, nil, nil)
	o.RegimeProvenance = entryplanbacktest.RegimeUnavailable
	rep := NewIntegrityReport(10)
	row := Evaluate(o, ArmZoneLimit, 5)
	rep.Check(&row, entryplanbacktest.NewObservationSet(), entryplanbacktest.RegimeReplayedPIT)
	if rep.ByCode[IntegrityArmMixing] != 1 {
		t.Fatalf("a regime-blind row inside a REPLAYED_PIT pass was not caught: %+v", rep.ByCode)
	}
}

func TestEveryIntegrityCodeIsPresentAtZeroOnACleanRun(t *testing.T) {
	rep := NewIntegrityReport(10)
	for _, c := range AllIntegrityCodes {
		if _, ok := rep.ByCode[c]; !ok {
			t.Fatalf("%s is absent from a fresh report — a check that never ran and a check "+
				"that found nothing would look the same", c)
		}
	}
	if rep.Total() != 0 {
		t.Fatalf("a fresh report totals %d", rep.Total())
	}
}

var _ = fetcher.Candle{}

// TestTheMissedArmPublishesNoOutcome is the regression for a presentation defect the first
// full-scale run produced: arm D was built by relabelling arm B's rows and aggregating them,
// so the MISSED cell reported arm B's FILLED returns, excursions and hit rates under the
// label MISSED — a table of filled-trade outcomes titled "missed".
func TestTheMissedArmPublishesNoOutcome(t *testing.T) {
	bars, s := flatSeries(40, 100)
	hit := Evaluate(obsFor(bars, s, s.dates[0], ptr(100.0), nil, ptr(90.0), ptr(110.0), nil), ArmZoneLimit, 5)
	miss := Evaluate(obsFor(bars, s, s.dates[0], ptr(50.0), nil, nil, nil, nil), ArmZoneLimit, 5)
	if !hit.Fill.Filled() || miss.Fill.Outcome != FillMissed {
		t.Fatalf("fixture: hit=%+v miss=%+v", hit.Fill, miss.Fill)
	}
	// What arm B legitimately reports for the same rows.
	b := Aggregate(ArmZoneLimit, 5, "", []Row{hit, miss})
	if b.Returns[5].N == 0 {
		t.Fatal("arm B produced no return sample; the test would be vacuous")
	}

	d := NewAcc()
	for _, r := range []Row{hit, miss} {
		r.Arm = ArmMissed
		d.AddCandidateOnly(&r)
	}
	m := d.Finish(ArmMissed, 5, "")

	if m.NCandidate != 2 || m.NMissed != 1 {
		t.Fatalf("missed cell counts NCandidate=%d NMissed=%d, want 2/1", m.NCandidate, m.NMissed)
	}
	if m.MissedRate.Hits != 1 || m.MissedRate.Denominator != 2 {
		t.Fatalf("missed rate %+v, want 1/2", m.MissedRate)
	}
	for _, h := range Horizons {
		if m.Returns[h].N != 0 {
			t.Fatalf("the MISSED cell reports %d Return@%d samples — a fabricated outcome for "+
				"a trade that did not happen", m.Returns[h].N, h)
		}
	}
	if m.MFE20.N != 0 || m.MAE20.N != 0 || m.FillVsSignalCloseBps.N != 0 {
		t.Fatalf("the MISSED cell reports excursions or fill quality: MFE N=%d MAE N=%d bps N=%d",
			m.MFE20.N, m.MAE20.N, m.FillVsSignalCloseBps.N)
	}
	if m.InvalidationHitRate.Denominator != 0 || m.Target1HitRate.Denominator != 0 {
		t.Fatalf("the MISSED cell reports level hits: inval den=%d t1 den=%d",
			m.InvalidationHitRate.Denominator, m.Target1HitRate.Denominator)
	}
	if m.Returns[5].N == b.Returns[5].N && b.Returns[5].N != 0 {
		t.Fatal("the MISSED cell is echoing arm B's sample")
	}
}

// TestTheOpportunityCostSplitUsesTheBenchmarkAndFabricatesNoFill pins EP-9 §28 Q5.
//
// The whole risk in answering "what did missing cost us" is that somebody answers it with a
// fabricated entry price. This checks the opposite: the missed side carries a BENCHMARK
// outcome (what the stock did from the signal close, which is real and unexecutable) while
// arm D's own cell stays empty.
func TestTheOpportunityCostSplitUsesTheBenchmarkAndFabricatesNoFill(t *testing.T) {
	bars, s := flatSeries(40, 100)
	// The stock rises after the signal, so the benchmark has a real, non-zero outcome.
	for i := 2; i < 40; i++ {
		p := 100.0 + float64(i)
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = p, p, p, p
	}
	missed := obsFor(bars, s, s.dates[0], ptr(50.0), nil, nil, nil, nil) // zone far below: never fills
	filled := obsFor(bars, s, s.dates[0], ptr(100.0), nil, nil, nil, nil)

	zoneMissed := Evaluate(missed, ArmZoneLimit, 5)
	zoneFilled := Evaluate(filled, ArmZoneLimit, 5)
	if zoneMissed.Fill.Outcome != FillMissed || !zoneFilled.Fill.Filled() {
		t.Fatalf("fixture: missed=%q filled=%v", zoneMissed.Fill.Outcome, zoneFilled.Fill.Filled())
	}

	// The MISSED side's opportunity cost is the BENCHMARK row for that same observation.
	bench := Evaluate(missed, ArmSignalCloseBaseline, 5)
	if !bench.Fill.Filled() {
		t.Fatal("the benchmark did not fill on a missed observation; Q5 would be unanswerable")
	}
	if bench.Fill.Price != missed.SignalClose {
		t.Fatalf("the benchmark filled at %v, want the signal close %v — anything else is a "+
			"fabricated entry price", bench.Fill.Price, missed.SignalClose)
	}
	acc := NewAcc()
	acc.Add(&bench)
	m := acc.Finish(ArmSignalCloseBaseline, 5, "benchmark | zone arm MISSED")
	if m.Returns[5].N != 1 {
		t.Fatalf("the missed side's benchmark has %d Return@5 samples, want 1", m.Returns[5].N)
	}
	if m.Executable() {
		t.Fatal("the opportunity-cost row reports itself as executable")
	}

	// Arm D itself stays empty, so the two can never be confused.
	d := NewAcc()
	r := zoneMissed
	r.Arm = ArmMissed
	d.AddCandidateOnly(&r)
	dm := d.Finish(ArmMissed, 5, "")
	if dm.Returns[5].N != 0 || dm.MFE20.N != 0 {
		t.Fatalf("arm D gained an outcome from the opportunity-cost measurement: %+v", dm.Returns[5])
	}
	if dm.MissedRate.Hits != 1 || dm.MissedRate.Denominator != 1 {
		t.Fatalf("arm D missed rate %+v, want 1/1", dm.MissedRate)
	}

	// And the note printed with the table says what the rows are.
	for _, want := range []string{"NOT AN ENTRYPLAN FILL", "unexecutable", "N=0",
		"No fill price is fabricated"} {
		if !strings.Contains(OpportunityNote, want) {
			t.Fatalf("the opportunity-cost note never says %q:\n%s", want, OpportunityNote)
		}
	}
}
