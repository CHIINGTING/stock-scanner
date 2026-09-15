package entryplan_test

import (
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/pricerule"
)

// ── EP-3b: the ideal entry zone ───────────────────────────────────────────────────────

// ── an INDEPENDENT tick table ─────────────────────────────────────────────────────────
//
// Transcribed from the TWSE 股價升降單位 tiers, in this file, by hand. It is NOT
// pricerule.TickSize, and it deliberately is not: a test that checked "the published price is
// on the grid" by calling the same function production called would agree with production
// about the wrong grid. The same reasoning invariants.go gives for keeping two independent
// float walkers, and TestTheTwoTickTablesAgree cross-checks these two the way
// TestTheTwoFloatWalkersAgree cross-checks those.
//
//	[0, 10)      0.01
//	[10, 50)     0.05
//	[50, 100)    0.1
//	[100, 500)   0.5
//	[500, 1000)  1
//	[1000, ∞)    5
func tickCentsFor(price float64) (int64, bool) {
	if math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
		return 0, false
	}
	switch {
	case price < 10:
		return 1, true
	case price < 50:
		return 5, true
	case price < 100:
		return 10, true
	case price < 500:
		return 50, true
	case price < 1000:
		return 100, true
	default:
		return 500, true
	}
}

// onTickGrid reports whether a price is a legal quote: a whole number of cents, and a whole
// multiple of its tier's tick.
//
// Integer arithmetic on cents, because that is the only way to ask the question without
// re-introducing the float error the whole exercise is about. 1245.3 fails it (its tier is
// 5.00), and so does 1245.0000000000002.
func onTickGrid(price float64) bool {
	tickCents, ok := tickCentsFor(price)
	if !ok {
		return false
	}
	cents := price * 100
	rounded := math.Round(cents)
	if math.Abs(cents-rounded) > 1e-6 {
		// Not even a whole number of cents. math.Round(v*10)/10 produces exactly this.
		return false
	}
	return int64(rounded)%tickCents == 0
}

func TestTheTwoTickTablesAgree(t *testing.T) {
	// Every band boundary and a point inside every band. The boundaries are the cases worth
	// naming: a price of exactly 50 pays 0.1 and not 0.05, and exactly 1000 pays 5 and not 1.
	var checked int
	for _, price := range []float64{
		0.01, 5, 9.99, 10, 10.01, 49.95, 50, 50.1, 99.9, 100, 100.5,
		499.5, 500, 501, 999, 1000, 1005, 19795, 999999,
	} {
		want, wantOK := tickCentsFor(price)
		if !wantOK {
			t.Fatalf("the test table refused %v", price)
		}
		got, gotOK := priceRuleTickCents(price)
		if !gotOK {
			t.Errorf("pricerule refused %v while the transcribed table accepted it", price)
			continue
		}
		if got != want {
			t.Errorf("tick at %v: pricerule says %d cents, the transcribed TWSE table says %d",
				price, got, want)
		}
		checked++
	}
	if checked < 19 {
		t.Fatalf("only %d prices compared — the cross-check is not running", checked)
	}
}

// ── the case table, asserted field by field ───────────────────────────────────────────

// Every named case must produce exactly the zone, the ceiling and the reasons it was built
// for. This is the test that fails when a rule changes, and it names the case.
func TestTheZoneCasesProduceWhatTheyWereBuiltFor(t *testing.T) {
	var withZone, withoutZone, withChase, withoutChase int

	for _, c := range zoneCases() {
		t.Run(c.name, func(t *testing.T) {
			p := entryplan.ComputePlan(c.snapshot())

			switch {
			case c.wantZone != nil:
				withZone++
				if p.IdealEntry == nil {
					t.Fatalf("no zone, want %v — reasons: %v", *c.wantZone, p.Reasons)
				}
				if p.IdealEntry.Low != c.wantZone[0] || p.IdealEntry.High != c.wantZone[1] {
					t.Errorf("zone = %v → %v, want %v → %v", p.IdealEntry.Low,
						p.IdealEntry.High, c.wantZone[0], c.wantZone[1])
				}
			case c.noZone:
				withoutZone++
				if p.IdealEntry != nil {
					t.Errorf("zone = %v → %v, want NONE — an entry price was fabricated for "+
						"an input built to have none", p.IdealEntry.Low, p.IdealEntry.High)
				}
			}

			switch {
			case c.wantChase != nil:
				withChase++
				if p.MaxChasePrice == nil {
					t.Fatalf("no chase ceiling, want %v — reasons: %v", *c.wantChase, p.Reasons)
				}
				if *p.MaxChasePrice != *c.wantChase {
					t.Errorf("chase = %v, want %v", *p.MaxChasePrice, *c.wantChase)
				}
			case c.noChase:
				withoutChase++
				if p.MaxChasePrice != nil {
					t.Errorf("chase = %v, want NONE", *p.MaxChasePrice)
				}
			}

			for _, want := range c.wantReasons {
				if !hasReason(p, want) {
					t.Errorf("reason %q missing; got %v", want, p.Reasons)
				}
			}
			for _, not := range c.notReasons {
				if hasReason(p, not) {
					t.Errorf("reason %q present and must not be — the rule reported a "+
						"different failure from the one this input has; got %v", not, p.Reasons)
				}
			}
		})
	}

	// ANTI-VACUITY. All four quadrants must be populated, or one half of the switch above
	// never executed.
	if withZone == 0 || withoutZone == 0 || withChase == 0 || withoutChase == 0 {
		t.Errorf("the table covered %d zones / %d refusals and %d ceilings / %d refusals — "+
			"a quadrant with no case in it is an assertion that never ran",
			withZone, withoutZone, withChase, withoutChase)
	}
}

func hasReason(p entryplan.Plan, r entryplan.Reason) bool {
	for _, got := range p.Reasons {
		if got == r {
			return true
		}
	}
	return false
}

// Between them, the named cases must reach every EP-3b reason code that is not reserved.
//
// The registry test in reasons_test.go already sweeps for dead codes, and this is the other
// half: it says the ZONE CASES specifically are what reach them, so a code cannot end up
// covered only by an accident of the 280k-case evidence matrix, where nothing declares what
// the input was for.
func TestEveryEP3ReasonIsReachedByANamedCase(t *testing.T) {
	seen := map[entryplan.Reason]bool{}
	for _, c := range zoneCases() {
		for _, r := range entryplan.ComputePlan(c.snapshot()).Reasons {
			seen[r] = true
		}
	}
	// The codes the zone cases own. The base-evidence codes (no price, no basis, no regime,
	// no semantic) are matrix()'s, and the two reserved ones have no input at all.
	for _, r := range []entryplan.Reason{
		entryplan.ReasonZoneSemanticNotPermitted,
		entryplan.ReasonZonePolicyUnresolved,
		entryplan.ReasonPullbackLevelsUnavailable,
		entryplan.ReasonPullbackLevelsBasisMismatch,
		entryplan.ReasonPullbackLevelNotBelowCurrentPrice,
		entryplan.ReasonBreakoutLevelUnavailable,
		entryplan.ReasonBreakoutLevelBasisMismatch,
		entryplan.ReasonBreakoutLevelNotAQuote,
		entryplan.ReasonZoneATRUnavailable,
		entryplan.ReasonZoneATRBasisMismatch,
		entryplan.ReasonZoneATRPeriodMismatch,
		entryplan.ReasonZoneTickAlignmentUnavailable,
		entryplan.ReasonZoneLowNotAPrice,
		entryplan.ReasonPullbackZoneOverlapsCurrentPrice,
		entryplan.ReasonRecentPriceAdjustment,
		entryplan.ReasonChaseNotAllowed,
		entryplan.ReasonChasePolicyUnresolved,
		entryplan.ReasonPreviousCloseUnavailable,
		entryplan.ReasonPreviousCloseBasisMismatch,
		entryplan.ReasonChaseLimitRequiresRawBasis,
		entryplan.ReasonChaseLimitRuleUnavailable,
		entryplan.ReasonChaseLimitPriceUnavailable,
		entryplan.ReasonChaseCeilingBelowZone,
	} {
		if !seen[r] {
			t.Errorf("no named zone case produces %q — the code's behaviour is declared "+
				"nowhere, so nothing pins what input reaches it", r)
		}
	}
}

// ── the selection rule ────────────────────────────────────────────────────────────────

// THE RULE, spelled out on the exact numbers EP-3b was specified with: current 100, MA20 94,
// MA60 90, base low 92.
//
// The centre must be 94 — the HIGHEST eligible support, the one price is coming into first.
// min() gives 90, the mean gives 92, and the median gives 92, so this single case separates
// the required rule from all three of the plausible wrong ones.
func TestThePullbackCentreIsTheNearestSupportBelowTheMarket(t *testing.T) {
	in := zoneBase()
	in.CurrentPrice, in.MA20, in.MA60, in.BaseLow = obs(100), obs(94), obs(90), obs(92)

	p := entryplan.ComputePlan(in)
	if p.EntryTrace == nil {
		t.Fatal("no trace: the selection cannot be inspected")
	}
	tr := p.EntryTrace.Zone
	if tr.Center == nil {
		t.Fatalf("no centre was selected; rejection = %q, candidates = %v",
			tr.Rejection, renderCandidates(tr.Candidates))
	}
	if *tr.Center != 94 {
		t.Errorf("centre = %v, want 94 (the MA20, the nearest support below the market). "+
			"min() would give 90, mean 92, median 92 — every one of those returns a number "+
			"that is not a level anybody defends", *tr.Center)
	}
	if tr.CenterSource != entryplan.LevelMA20 {
		t.Errorf("centre source = %q, want %q", tr.CenterSource, entryplan.LevelMA20)
	}

	// And the losers are recorded as ELIGIBLE, not as failures: a reader must be able to see
	// that a deeper support existed and was passed over deliberately.
	want := map[entryplan.LevelSource]entryplan.LevelDisposition{
		entryplan.LevelMA20:         entryplan.DispositionSelected,
		entryplan.LevelMA60:         entryplan.DispositionEligibleNotSelected,
		entryplan.LevelBaseLow:      entryplan.DispositionEligibleNotSelected,
		entryplan.LevelBreakoutHigh: entryplan.DispositionWrongSemantic,
	}
	if got := dispositions(tr.Candidates); !reflect.DeepEqual(got, want) {
		t.Errorf("dispositions = %v, want %v", got, want)
	}
}

// The MA20 above the market must not be used, and the answer must be the next support DOWN —
// which is the specified case "MA20=102 must not be used, the centre should be 94 if the MA60
// is 94".
func TestASupportAboveTheMarketIsNotAPullbackLevel(t *testing.T) {
	in := zoneBase()
	in.CurrentPrice, in.MA20, in.MA60, in.BaseLow = obs(100), obs(102), obs(94), obs(92)

	p := entryplan.ComputePlan(in)
	tr := p.EntryTrace.Zone
	if tr.Center == nil || *tr.Center != 94 {
		t.Fatalf("centre = %v, want 94 (the MA60) — the MA20 at 102 is above the market and "+
			"is not a level price has retraced to", tr.Center)
	}
	if tr.CenterSource != entryplan.LevelMA60 {
		t.Errorf("centre source = %q, want MA60", tr.CenterSource)
	}
	// And the MA20 is on the record with the reason it lost, not merely absent.
	got := dispositions(tr.Candidates)
	if got[entryplan.LevelMA20] != entryplan.DispositionNotBelowCurrent {
		t.Errorf("MA20 disposition = %q, want %q", got[entryplan.LevelMA20],
			entryplan.DispositionNotBelowCurrent)
	}
	// The published zone follows the centre: 94 ± 2.
	if p.IdealEntry == nil || p.IdealEntry.Low != 92 || p.IdealEntry.High != 96 {
		t.Errorf("zone = %v, want 92 → 96", p.IdealEntry)
	}
}

// The three steps are separately testable, and here they are separately tested — driving
// SelectCenter directly, with no snapshot in sight, on the inputs that matter.
func TestSelectCenterTakesTheMaximumEligibleSupport(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"ascending", []float64{90, 92, 94}, 94},
		{"descending", []float64{94, 92, 90}, 94},
		{"the winner in the middle", []float64{92, 94, 90}, 94},
		{"all equal", []float64{94, 94, 94}, 94},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var screened []entryplan.CandidateLevel
			for n, src := range []entryplan.LevelSource{
				entryplan.LevelMA20, entryplan.LevelMA60, entryplan.LevelBaseLow,
			} {
				screened = append(screened, entryplan.CandidateLevel{
					Source: src, Status: entryplan.Available, Value: f(c.values[n]),
					PriceBasis:  entryplan.PriceBasisRaw,
					Disposition: entryplan.DispositionEligibleNotSelected,
				})
			}
			out, best, ok := entryplan.SelectCenter(screened, entryplan.EntrySemanticPullback)
			if !ok {
				t.Fatal("no centre selected from three eligible candidates")
			}
			if *out[best].Value != c.want {
				t.Errorf("selected %v, want %v", *out[best].Value, c.want)
			}
			if out[best].Disposition != entryplan.DispositionSelected {
				t.Errorf("winner disposition = %q", out[best].Disposition)
			}
			// Exactly one winner.
			var selected int
			for _, o := range out {
				if o.Disposition == entryplan.DispositionSelected {
					selected++
				}
			}
			if selected != 1 {
				t.Errorf("%d candidates marked SELECTED, want exactly 1", selected)
			}
		})
	}
}

// A tie resolves to the earlier source in AllLevelSources. Stated, so the answer is
// deterministic rather than dependent on iteration order.
func TestATieBetweenSupportsResolvesToTheEarlierSource(t *testing.T) {
	screened := []entryplan.CandidateLevel{
		{Source: entryplan.LevelMA60, Status: entryplan.Available, Value: f(94),
			Disposition: entryplan.DispositionEligibleNotSelected},
		{Source: entryplan.LevelMA20, Status: entryplan.Available, Value: f(94),
			Disposition: entryplan.DispositionEligibleNotSelected},
	}
	// Note the slice order is deliberately NOT registry order: the tie-break must not be
	// "whichever came first in the slice a caller happened to build".
	out, best, ok := entryplan.SelectCenter(screened, entryplan.EntrySemanticPullback)
	if !ok {
		t.Fatal("no centre selected")
	}
	if out[best].Source != entryplan.LevelMA60 {
		t.Errorf("tie went to %q; the rule is STRICTLY greater wins, so the first eligible "+
			"row in the slice keeps the centre — and the production candidate list is always "+
			"in AllLevelSources order, which is what makes that deterministic", out[best].Source)
	}
}

// A breakout centre may only ever be the pivot. This plants the input production cannot
// produce — an ELIGIBLE moving average under the BREAKOUT semantic — and requires the guard
// to refuse it, so "the canonical centre can only be PivotHigh" is a property of THIS
// function rather than a consequence of another one's behaviour.
func TestBreakoutRefusesANonPivotCentre(t *testing.T) {
	for _, c := range []struct {
		name     string
		screened []entryplan.CandidateLevel
		wantOK   bool
		wantSrc  entryplan.LevelSource
	}{
		{
			name: "an eligible MA20 under BREAKOUT is refused",
			screened: []entryplan.CandidateLevel{
				{Source: entryplan.LevelMA20, Status: entryplan.Available, Value: f(94),
					Disposition: entryplan.DispositionEligibleNotSelected},
			},
		},
		{
			name: "two eligible pivots is a contradiction, not a tie",
			screened: []entryplan.CandidateLevel{
				{Source: entryplan.LevelBreakoutHigh, Status: entryplan.Available, Value: f(105),
					Disposition: entryplan.DispositionEligibleNotSelected},
				{Source: entryplan.LevelBreakoutHigh, Status: entryplan.Available, Value: f(107),
					Disposition: entryplan.DispositionEligibleNotSelected},
			},
		},
		{
			name: "an eligible row with no number is refused, not dereferenced",
			screened: []entryplan.CandidateLevel{
				{Source: entryplan.LevelBreakoutHigh, Status: entryplan.Available,
					Disposition: entryplan.DispositionEligibleNotSelected},
			},
		},
		{
			name: "the pivot alone is selected",
			screened: []entryplan.CandidateLevel{
				{Source: entryplan.LevelMA20, Disposition: entryplan.DispositionWrongSemantic},
				{Source: entryplan.LevelBreakoutHigh, Status: entryplan.Available, Value: f(105),
					Disposition: entryplan.DispositionEligibleNotSelected},
			},
			wantOK:  true,
			wantSrc: entryplan.LevelBreakoutHigh,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, best, ok := entryplan.SelectCenter(c.screened, entryplan.EntrySemanticBreakout)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (best = %d)", ok, c.wantOK, best)
			}
			if ok && out[best].Source != c.wantSrc {
				t.Errorf("selected %q, want %q", out[best].Source, c.wantSrc)
			}
		})
	}
}

// An unresolved semantic selects nothing. Not a rejection of the levels — there is no
// question to answer.
func TestAnUnresolvedSemanticSelectsNoCentre(t *testing.T) {
	screened := []entryplan.CandidateLevel{
		{Source: entryplan.LevelMA20, Status: entryplan.Available, Value: f(94),
			Disposition: entryplan.DispositionEligibleNotSelected},
	}
	for _, sem := range []entryplan.EntrySemantic{
		entryplan.EntrySemanticUnknown, entryplan.EntrySemantic(""), entryplan.EntrySemantic("SWING"),
	} {
		if _, _, ok := entryplan.SelectCenter(screened, sem); ok {
			t.Errorf("semantic %q selected a centre — there is no entry shape to select for", sem)
		}
	}
}

// ── the width ─────────────────────────────────────────────────────────────────────────

// The half width is max(0.5 × ATR, 2 × tick), and EVERY TERM is kept on the trace. A zone
// that recorded only its answer could not be read back: "half width 2.0" does not say whether
// the ATR was 4.0 or whether the ATR was 0.02 and the floor took over.
func TestTheZoneWidthKeepsEveryTermItWasComputedFrom(t *testing.T) {
	in := zoneBase() // MA20 94, ATR 4, tick 0.1 at 94
	tr := entryplan.ComputePlan(in).EntryTrace.Zone

	for _, c := range []struct {
		name string
		got  *float64
		want float64
	}{
		{"ATR", tr.ATR, 4},
		{"ATR half width", tr.ATRHalfWidth, 2},
		{"tick size", tr.TickSize, 0.1},
		{"tick floor width", tr.TickFloorWidth, 0.2},
		{"half width", tr.HalfWidth, 2},
		{"raw low", tr.RawLow, 92},
		{"raw high", tr.RawHigh, 96},
		{"aligned low", tr.Low, 92},
		{"aligned high", tr.High, 96},
	} {
		if c.got == nil {
			t.Errorf("%s is absent from the trace", c.name)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, *c.got, c.want)
		}
	}
	if tr.HalfWidthBinding != entryplan.BindingATRHalf {
		t.Errorf("binding = %q, want %q — half of a 4.0 ATR is 2.0 and two ticks is 0.2",
			tr.HalfWidthBinding, entryplan.BindingATRHalf)
	}
	if tr.ATRPeriod != 14 {
		t.Errorf("ATR period = %d, want 14", tr.ATRPeriod)
	}

	// The tick floor case, so both branches of the max() are witnessed.
	in = zoneBase()
	in.CurrentPrice, in.MA20 = obs(8), obs(7.5)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.ATR = atr(0.01)
	tr = entryplan.ComputePlan(in).EntryTrace.Zone
	if tr.HalfWidthBinding != entryplan.BindingTickFloor {
		t.Fatalf("binding = %q, want %q — half of a 0.01 ATR is 0.005 and two ticks is 0.02",
			tr.HalfWidthBinding, entryplan.BindingTickFloor)
	}
	if tr.HalfWidth == nil || *tr.HalfWidth != 0.02 {
		t.Errorf("half width = %v, want 0.02", tr.HalfWidth)
	}
}

// AN ABSENT ATR PRODUCES NO ZONE. Not a tick-only zone, not a percentage of the last price,
// and not the legacy 2.5%.
//
// The assertion is against ALL THREE of the plausible fabrications by name, because the
// failure being designed against is not "no zone" — it is a zone computed by a DIFFERENT RULE
// published through the same field.
func TestAnAbsentATRProducesNoZoneAndNoSubstituteWidth(t *testing.T) {
	for _, c := range []struct {
		name string
		atr  entryplan.ATREvidence
	}{
		{"unavailable", entryplan.ATREvidence{Status: entryplan.Unavailable}},
		{"the zero value", entryplan.ATREvidence{}},
		{"available with no value", entryplan.ATREvidence{Status: entryplan.Available, Period: 14}},
		{"zero", atr(0)},
		{"negative", atr(-4)},
		{"NaN", atr(nan())},
		{"+Inf", atr(math.Inf(1))},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := zoneBase()
			in.ATR = c.atr
			p := entryplan.ComputePlan(in)

			if p.IdealEntry != nil {
				low, high := p.IdealEntry.Low, p.IdealEntry.High
				t.Fatalf("zone = %v → %v with no usable ATR. If the width came from the tick "+
					"floor it would be 93.8 → 94.2; from 2.5%% of the last price, 91.5 → 96.5; "+
					"from 5%%, 89.3 → 98.7. Every one of those is a different rule wearing "+
					"this field's name", low, high)
			}
			if p.MaxChasePrice != nil {
				t.Errorf("chase = %v with no zone to chase into", *p.MaxChasePrice)
			}
			if !hasReason(p, entryplan.ReasonZoneATRUnavailable) {
				t.Errorf("reasons = %v, want ZONE_ATR_UNAVAILABLE — an absent zone with no "+
					"code naming the missing input is a shrug", p.Reasons)
			}
			// And the levels must NOT be blamed: they were all fine.
			if hasReason(p, entryplan.ReasonPullbackLevelsUnavailable) {
				t.Errorf("the missing ATR was reported as a missing level: %v", p.Reasons)
			}
		})
	}
}

// Every width rejection maps to its OWN plan reason, and the fourth code is asserted to be
// unreachable rather than assumed to be.
//
// The three reachable ones are checked through the named cases, which each declare the reason
// they must produce — so a mapping that sent two rejections to one reason would fail there,
// and a rejection added later without a mapping would arrive as the ATR reason and fail the
// distinctness check below.
func TestEveryWidthRejectionMapsToAReason(t *testing.T) {
	if len(entryplan.AllWidthRejections) != 4 {
		t.Errorf("the width rejection registry has %d members; if that was deliberate, the "+
			"mapping in widthRejectionReason needs a case for the new one",
			len(entryplan.AllWidthRejections))
	}
	seen := map[entryplan.WidthRejection]bool{}
	for _, c := range zoneCases() {
		tr := entryplan.ComputePlan(c.snapshot()).EntryTrace
		if tr == nil {
			continue
		}
		if r := tr.Zone.WidthRejection; r != "" {
			if !knownWidthRejection(r) {
				t.Errorf("%s: width rejection %q is not in AllWidthRejections", c.name, r)
			}
			seen[r] = true
		}
	}
	// TICK_SIZE_UNAVAILABLE is not reachable through ComputePlan: the centre comes from a
	// screened candidate, so it is finite and positive, which is exactly when TickSize
	// succeeds. The other three must all be witnessed.
	for _, want := range []entryplan.WidthRejection{
		entryplan.WidthRejectATRUnavailable,
		entryplan.WidthRejectATRBasisMismatch,
		entryplan.WidthRejectATRPeriod,
	} {
		if !seen[want] {
			t.Errorf("no case produced width rejection %q", want)
		}
	}

	// DISTINCTNESS: each reachable rejection reaches a DIFFERENT plan reason. A mapping that
	// collapsed two of them would leave a reader unable to tell "there is no ATR" from "the
	// ATR is on the wrong series", which need different fixes.
	reasons := map[entryplan.WidthRejection]entryplan.Reason{}
	for _, c := range zoneCases() {
		p := entryplan.ComputePlan(c.snapshot())
		rej := p.EntryTrace.Zone.WidthRejection
		if rej == "" {
			continue
		}
		if got, ok := reasons[rej]; ok && got != p.EntryTrace.Zone.Rejection {
			t.Errorf("%q maps to both %q and %q", rej, got, p.EntryTrace.Zone.Rejection)
		}
		reasons[rej] = p.EntryTrace.Zone.Rejection
	}
	inverse := map[entryplan.Reason]entryplan.WidthRejection{}
	for rej, reason := range reasons {
		if other, ok := inverse[reason]; ok {
			t.Errorf("width rejections %q and %q both report plan reason %q — the two are "+
				"different failures with different fixes", rej, other, reason)
		}
		inverse[reason] = rej
		if !entryplan.KnownReason(reason) {
			t.Errorf("%q maps to %q, which is not a registered reason", rej, reason)
		}
	}
	if len(reasons) != 3 {
		t.Errorf("only %d width rejections were mapped: %v", len(reasons), reasons)
	}
	if seen[entryplan.WidthRejectTickSize] {
		t.Errorf("TICK_SIZE_UNAVAILABLE was reached from ComputePlan — that means a centre " +
			"got through selection without being a price, which is a screening bug")
	}
}

func knownWidthRejection(r entryplan.WidthRejection) bool {
	for _, k := range entryplan.AllWidthRejections {
		if k == r {
			return true
		}
	}
	return false
}

// ── the geometry, and the two clamps that must not exist ──────────────────────────────

// A PULLBACK ZONE IS NOT CLAMPED TO THE CURRENT PRICE.
//
// The zone stays centred on the support with a symmetric half width, the overlap is FLAGGED,
// and no status is decided. A `High = min(High, CurrentPrice)` mutation is caught by the
// symmetry assertion below, not merely by the value: with the clamp the low would still be
// 97 while the high became 100, and "the zone is the support ± half an ATR" — the only
// sentence that makes the number defensible — would be false.
func TestAPullbackZoneIsNeverClampedToTheCurrentPrice(t *testing.T) {
	in := zoneBase()
	in.CurrentPrice, in.MA20 = obs(100), obs(99)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil {
		t.Fatalf("no zone; reasons %v", p.Reasons)
	}
	low, high := p.IdealEntry.Low, p.IdealEntry.High
	if low != 97 || high != 101 {
		t.Fatalf("zone = %v → %v, want 97 → 101 (99 ± 2). A high of 100 means the zone was "+
			"clamped to the last price", low, high)
	}

	// THE SYMMETRY, which is what the clamp destroys: the centre must sit exactly in the
	// middle of the published band, to within the tick.
	centre := *p.EntryTrace.Zone.Center
	if lowGap, highGap := centre-low, high-centre; math.Abs(lowGap-highGap) > 0.1 {
		t.Errorf("the zone is asymmetric about its own centre: %v below, %v above. That is "+
			"what a clamp leaves behind, and it makes the plan's own description of itself "+
			"("+"support ± half an ATR"+") false", lowGap, highGap)
	}
	if high < *in.CurrentPrice.Value {
		t.Fatal("the fixture no longer overlaps the market, so this test asserts nothing")
	}
	if !hasReason(p, entryplan.ReasonPullbackZoneOverlapsCurrentPrice) {
		t.Errorf("the overlap was not flagged: %v", p.Reasons)
	}
	if !p.EntryTrace.Zone.OverlapsCurrentPrice {
		t.Error("the overlap is not on the trace either")
	}
	// AND THE STATUS IS THE ONE THE UNCLAMPED ZONE IMPLIES. EP-3b decided no status and this
	// block asserted INSUFFICIENT_DATA; EP-5 decides one, and an overlapping zone is exactly
	// the input where the clamp would have changed the answer.
	//
	// 100 is inside [97, 101], so the market is in the band and BULL permits executing there:
	// BUY_NOW. A zone clamped to the last price would have published [97, 100] — 100 is STILL
	// inside that, bounds inclusive, so the status would NOT have moved and the clamp would
	// have been invisible here. That is why the geometry above is asserted directly and this
	// is the weaker of the two claims: what it adds is that the decision reads the PUBLISHED
	// band, and that an overlap is a BUY_NOW rather than a TOO_EXTENDED.
	if p.Status != entryplan.StatusBuyNow {
		t.Errorf("status = %q, want BUY_NOW — the market at 100 is inside the published band "+
			"[%v, %v] and BULL permits buying there; TOO_EXTENDED would mean the market had "+
			"passed the chase ceiling, which at %v it has not", p.Status, low, high,
			*p.MaxChasePrice)
	}
	if !hasReason(p, entryplan.ReasonStatusPriceInsideZone) {
		t.Errorf("the status carries no price relation: %v", p.Reasons)
	}
}

// A BREAKOUT ZONE'S LOW NEVER EXTENDS BELOW THE PIVOT — after alignment, which is where the
// property could actually break.
func TestABreakoutZoneSitsOnTopOfItsPivot(t *testing.T) {
	// The specified case: pivot 100, ATR 4 → raw 100 → 102.
	in := zoneBase()
	breakout(&in)
	in.CurrentPrice, in.PivotHigh = obs(98), obs(100)
	in.PreviousClose = obs(98)

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil {
		t.Fatalf("no zone; reasons %v", p.Reasons)
	}
	if p.IdealEntry.Low != 100 || p.IdealEntry.High != 102 {
		t.Errorf("zone = %v → %v, want 100 → 102", p.IdealEntry.Low, p.IdealEntry.High)
	}
	if !onTickGrid(p.IdealEntry.Low) || !onTickGrid(p.IdealEntry.High) {
		t.Errorf("zone bounds are not legal quotes: %v → %v", p.IdealEntry.Low, p.IdealEntry.High)
	}

	// Now over every pivot that is a legal quote in every tick tier: the aligned low must
	// never be below the pivot, and the zone must never invert.
	var checked int
	for _, pivot := range []float64{
		1.23, 9.99, 10.05, 27.75, 49.95, 50.1, 71.4, 99.9, 100.5, 287.5, 499.5,
		500, 761, 999, 1000, 1245, 19795,
	} {
		for _, atrValue := range []float64{0.01, 0.37, 4, 33.33, 60} {
			in := zoneBase()
			breakout(&in)
			in.CurrentPrice = obs(pivot)
			in.PivotHigh = obs(pivot)
			in.PreviousClose = obs(pivot)
			in.ATR = atr(atrValue)

			p := entryplan.ComputePlan(in)
			if p.IdealEntry == nil {
				continue
			}
			checked++
			if p.IdealEntry.Low < pivot {
				t.Errorf("pivot %v, ATR %v: zone low %v is BELOW the pivot — the bottom of "+
					"a breakout zone would buy the level before it broke",
					pivot, atrValue, p.IdealEntry.Low)
			}
			if p.IdealEntry.Low > p.IdealEntry.High {
				t.Errorf("pivot %v, ATR %v: inverted zone %v → %v", pivot, atrValue,
					p.IdealEntry.Low, p.IdealEntry.High)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d breakout zones were produced across the sweep — the fixture is not "+
			"reaching the rule", checked)
	}
}

// ── tick alignment, and the direction each bound is aligned in ────────────────────────

// The two directions must be DOWN for the low and UP for the high: the zone is the band of
// prices the entry ACCEPTS, so alignment covers the computed band rather than shaving it.
//
// The fixture is chosen so the two directions give DIFFERENT answers — an ATR of 4.03 puts
// both raw bounds off the 0.10 grid. With the directions swapped the zone would be 92.0 →
// 96.0, which is inside the raw band instead of covering it.
func TestThePullbackBoundsAreAlignedOutward(t *testing.T) {
	in := zoneBase()
	in.MA20, in.MA60, in.BaseLow = obs(94), entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.ATR = atr(4.03) // half width 2.015 → raw 91.985 → 96.015

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil {
		t.Fatalf("no zone; reasons %v", p.Reasons)
	}
	if p.IdealEntry.Low != 91.9 {
		t.Errorf("zone low = %v, want 91.9: the raw low is 91.985 and it is aligned DOWN, so "+
			"the whole acceptable band is covered. 92.0 means it was aligned up and the "+
			"bottom of the rule's own band is no longer buyable", p.IdealEntry.Low)
	}
	if p.IdealEntry.High != 96.1 {
		t.Errorf("zone high = %v, want 96.1: the raw high is 96.015 and it is aligned UP. "+
			"96.0 means it was aligned down and the top of the band was shaved off",
			p.IdealEntry.High)
	}
	dirs := p.EntryTrace.Zone.TickDirections
	if dirs.Low != entryplan.TickDown || dirs.High != entryplan.TickUp {
		t.Errorf("recorded directions = %+v, want low DOWN / high UP", dirs)
	}
}

// The breakout low is aligned UP, because the low IS the level: aligning it down would place
// the bottom of a breakout zone below the price that defines the breakout.
func TestTheBreakoutLowIsAlignedUp(t *testing.T) {
	in := zoneBase()
	breakout(&in)
	// A pivot off the 0.50 grid, which is legal input (a pivot high of 100.30 exists on a
	// stock whose tick is 0.10 — but at 100.30 the tier is 0.50, so the level itself needs
	// aligning).
	in.CurrentPrice, in.PivotHigh, in.PreviousClose = obs(101), obs(100.3), obs(101)
	in.ATR = atr(4)

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil {
		t.Fatalf("no zone; reasons %v", p.Reasons)
	}
	if p.IdealEntry.Low != 100.5 {
		t.Errorf("zone low = %v, want 100.5: the pivot is 100.3, the tier is 0.50, and the "+
			"low is aligned UP so it cannot sit below the level. 100.0 means it was aligned "+
			"down, which buys the level before it broke", p.IdealEntry.Low)
	}
	if p.IdealEntry.High != 102.5 {
		t.Errorf("zone high = %v, want 102.5 (100.3 + 2.0 = 102.3, aligned up)", p.IdealEntry.High)
	}
	dirs := p.EntryTrace.Zone.TickDirections
	if dirs.Low != entryplan.TickUp || dirs.High != entryplan.TickUp {
		t.Errorf("recorded directions = %+v, want low UP / high UP", dirs)
	}
}

// ── every executable price is a legal quote ───────────────────────────────────────────

// THE SWEEP. Over every input this package can be handed — the 280k-case evidence matrix and
// every named zone case — each non-nil execution price must be finite, strictly positive and
// ON THE TICK GRID, and each zone must be ordered with its ceiling above it.
//
// Checked against the INDEPENDENT tick table at the top of this file, so "it went through
// pricerule" is not asserted by asking pricerule.
func TestEveryExecutablePriceIsALegalQuote(t *testing.T) {
	// The ledger: how many of each field was actually inspected. Without it a sweep over
	// plans that carry no price at all reports success for free.
	seen := map[string]int{}

	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		for _, ep := range executablePrices(p) {
			seen[ep.field]++
			if math.IsNaN(ep.value) || math.IsInf(ep.value, 0) {
				t.Fatalf("%s: %s = %v is not finite", c.name, ep.field, ep.value)
			}
			if ep.value <= 0 {
				t.Fatalf("%s: %s = %v is not a price. 0 is a real quote on no exchange, and "+
					"absence is spelled nil", c.name, ep.field, ep.value)
			}
			if !onTickGrid(ep.value) {
				tickCents, _ := tickCentsFor(ep.value)
				t.Fatalf("%s: %s = %v is not on the tick grid (the tier here is %d cents). "+
					"This is the number the exchange rejects, and math.Round(v*10)/10 is how "+
					"it gets produced", c.name, ep.field, ep.value, tickCents)
			}
		}
		if z := p.IdealEntry; z != nil {
			if z.Low > z.High {
				t.Fatalf("%s: inverted zone %v → %v", c.name, z.Low, z.High)
			}
			if p.MaxChasePrice != nil && *p.MaxChasePrice < z.High {
				t.Fatalf("%s: chase %v is below the top of its own zone %v", c.name,
					*p.MaxChasePrice, z.High)
			}
		}
	}

	// THE LEDGER, and EP-4 turned four of its rows from "must be zero" into "must not be".
	// Until EP-4 this block asserted that Invalidation, Target1, Target2 and RiskReward were
	// published ZERO times, which was the scope boundary then; now every one of them is an
	// executable price and has to be swept like the zone bounds.
	for _, field := range []string{
		"IdealEntry.Low", "IdealEntry.High", "MaxChasePrice",
		"Invalidation.Price", "Target1.Price", "Target2.Price",
	} {
		if seen[field] == 0 {
			t.Errorf("%s was never published anywhere in the union, so every assertion above "+
				"passed vacuously for it. Inspected: %v", field, seen)
		}
	}
	if testing.Verbose() {
		t.Logf("executable prices inspected: %v", seen)
	}
}

// ── the numbers that are DISTANCES rather than quotes ─────────────────────────────────

// RiskReward's three fields are swept for finiteness and positivity and NOT for the tick grid,
// and the distinction is the point of this test existing separately.
//
// A risk per share is the DISTANCE between two prices; a reward per share is another; a ratio is
// dimensionless. The tick grid constrains what the exchange will accept as a PRICE, and nothing
// about it constrains a span: entry 99.90 (a legal quote on the 0.10 grid) minus a stop of 49.85
// (a legal quote on the 0.05 grid) is 50.05, which is not a multiple of the 0.10 tick that
// applies at 50.05 — and there is nothing wrong with either input or with the answer.
//
// Until EP-4, RiskReward.RiskPerShare was listed in executablePrices() as part of the "EP-4
// publishes none of these" boundary, so it was tick-checked by a sweep that never saw one. It
// would have passed today by luck (real risks land in the 0.01 tier, where every whole cent is
// on the grid); it is moved here because passing by luck on a claim that is not true is exactly
// how a wrong assertion survives until the fixture that breaks it.
func TestTheRiskRewardFieldsAreSpansAndAreNotTickChecked(t *testing.T) {
	var seen int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		rr := p.RiskReward
		if rr == nil {
			continue
		}
		seen++
		for _, part := range []struct {
			name  string
			value float64
		}{
			{"RiskPerShare", rr.RiskPerShare},
			{"RewardPerShare", rr.RewardPerShare},
			{"Ratio", rr.Ratio},
		} {
			if math.IsNaN(part.value) || math.IsInf(part.value, 0) {
				t.Fatalf("%s: RiskReward.%s = %v is not finite", c.name, part.name, part.value)
			}
			if part.value <= 0 {
				t.Fatalf("%s: RiskReward.%s = %v — types.go's defence of a plain float64 here "+
					"is that the struct exists ONLY when the risk is strictly positive, and a "+
					"ratio of 0 reads as \"this trade offers no reward\"", c.name, part.name,
					part.value)
			}
		}
		// And the ratio must be the quotient of the other two, which is the ONE arithmetic
		// consistency claim the invariant checker deliberately does not make (see
		// invariants.go): it is checked here, against production's own output, rather than
		// added to the registry as a geometry invariant.
		if want := rr.RewardPerShare / rr.RiskPerShare; !closeEnough(rr.Ratio, want) {
			t.Errorf("%s: ratio = %v, want reward/risk = %v/%v = %v — the published ratio is "+
				"not the quotient of the published terms, so the R:R cannot be checked by hand",
				c.name, rr.Ratio, rr.RewardPerShare, rr.RiskPerShare, want)
		}
		if rr.Target != "TARGET_1" {
			t.Errorf("%s: RiskReward.Target = %q, want the literal \"TARGET_1\" — an R:R that "+
				"does not name the target it was measured to is unfalsifiable", c.name, rr.Target)
		}
	}
	if seen == 0 {
		t.Fatal("no plan in the union carried a risk/reward — every assertion above is vacuous")
	}
}

// closeEnough compares two floats at a tolerance that is loose against float64 rounding and
// tight against a wrong entry, a wrong target or a wrong multiple.
func closeEnough(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= 1e-9*(1+math.Abs(want))
}

// 1245.3 IS THE SPECIFIC NUMBER. A stock above 1000 元 trades on a 5.00 grid, so a level of
// 1245.3 is a legal INPUT and can never be a published price.
func TestAPriceAboveOneThousandCannotCarryADecimal(t *testing.T) {
	in := zoneBase()
	in.CurrentPrice, in.MA20 = obs(1300), obs(1245.3)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.ATR, in.PreviousClose = atr(60), obs(1300)

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil || p.MaxChasePrice == nil {
		t.Fatalf("no prices; reasons %v", p.Reasons)
	}
	for _, ep := range executablePrices(p) {
		if ep.value == 1245.3 {
			t.Errorf("%s = 1245.3 — the tier above 1000 元 is 5.00, so no order can be placed "+
				"at it", ep.field)
		}
		if !onTickGrid(ep.value) {
			t.Errorf("%s = %v is not a multiple of 5.00", ep.field, ep.value)
		}
	}
	// The centre, which is NOT an executable price, must still be the raw observation: the
	// trace records what was measured, and rounding it there would lose the input.
	if c := p.EntryTrace.Zone.Center; c == nil || *c != 1245.3 {
		t.Errorf("trace centre = %v, want the raw 1245.3 — the trace is the record of the "+
			"arithmetic, not a second copy of the order ticket", c)
	}
}

// ── PriceBasis compatibility ──────────────────────────────────────────────────────────

// The measured repo problem, as a behaviour: internal/indicator computes MA20 on the RAW
// close (calculator.go:33) while relstrength.go and vcp.go go through fetcher.PriceForCalc.
// A level on one series and a quote on the other must not be combined, and must not be
// converted.
func TestAMismatchedPriceBasisExcludesTheLevelAndIsNeverConverted(t *testing.T) {
	in := zoneBase()
	in.MA20 = obsOn(94, entryplan.PriceBasisAdjusted) // the highest support, and unusable
	in.MA60, in.BaseLow = obs(90), entryplan.PriceObservation{}

	p := entryplan.ComputePlan(in)
	tr := p.EntryTrace.Zone
	if tr.CenterSource != entryplan.LevelMA60 {
		t.Fatalf("centre source = %q, want MA60 — the MA20 is on the adjusted series and "+
			"must not be used, even though it is the nearest number", tr.CenterSource)
	}
	if got := dispositions(tr.Candidates)[entryplan.LevelMA20]; got != entryplan.DispositionBasisMismatch {
		t.Errorf("MA20 disposition = %q, want %q", got, entryplan.DispositionBasisMismatch)
	}
	// The row still carries the OBSERVED value and its OWN basis: refusing to use a number
	// is not a reason to hide it.
	var found bool
	for _, e := range p.Evidence {
		if e.Key != entryplan.EvidenceLevelMA20 {
			continue
		}
		found = true
		if e.Value == nil || *e.Value != 94 {
			t.Errorf("MA20 evidence value = %v, want the observed 94", e.Value)
		}
		if e.PriceBasis != entryplan.PriceBasisAdjusted {
			t.Errorf("MA20 evidence basis = %q, want ADJUSTED — the row must keep the basis "+
				"it was measured on, or the mismatch is unreadable afterwards", e.PriceBasis)
		}
	}
	if !found {
		t.Errorf("the MA20 evidence row disappeared: census %v", evidenceKeysOf(p))
	}

	// THE ONLY candidate mismatched → no zone, with the basis code and not the "unavailable"
	// one. This is the case that separates "we have no level" from "we have a level we may
	// not use".
	in = zoneBase()
	in.MA20 = obsOn(94, entryplan.PriceBasisAdjusted)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
	p = entryplan.ComputePlan(in)
	if p.IdealEntry != nil {
		t.Fatalf("zone %v → %v was built from a level on another price series",
			p.IdealEntry.Low, p.IdealEntry.High)
	}
	if !hasReason(p, entryplan.ReasonPullbackLevelsBasisMismatch) {
		t.Errorf("reasons = %v, want PULLBACK_LEVELS_PRICE_BASIS_MISMATCH", p.Reasons)
	}
	if hasReason(p, entryplan.ReasonPullbackLevelsUnavailable) {
		t.Errorf("a level that exists was reported as missing: %v", p.Reasons)
	}
}

// The zone names the basis it was built on, and it is never rewritten.
func TestTheZoneCarriesItsOwnPriceBasis(t *testing.T) {
	for _, basis := range []entryplan.PriceBasis{entryplan.PriceBasisRaw, entryplan.PriceBasisAdjusted} {
		in := zoneBase()
		in.PriceBasis = basis
		in.ATR = entryplan.ATREvidence{Status: entryplan.Available, Value: f(4), Period: 14,
			PriceBasis: basis}
		p := entryplan.ComputePlan(in)
		if p.IdealEntry == nil {
			t.Fatalf("basis %q: no zone; reasons %v", basis, p.Reasons)
		}
		if p.IdealEntry.PriceBasis != basis {
			t.Errorf("basis %q: the zone says %q", basis, p.IdealEntry.PriceBasis)
		}
		if p.EntryTrace.Zone.PriceBasis != basis {
			t.Errorf("basis %q: the trace says %q", basis, p.EntryTrace.Zone.PriceBasis)
		}
		// An ADJUSTED zone is published and its ceiling is not: see
		// ReasonChaseLimitRequiresRawBasis.
		if basis == entryplan.PriceBasisAdjusted && p.MaxChasePrice != nil {
			t.Errorf("an adjusted-basis plan published a chase ceiling of %v — ±10%% is "+
				"defined on the exchange's raw reference price", *p.MaxChasePrice)
		}
	}
}

// The zone names the LEVEL it was built from, as a registry code — so an archived zone can be
// read back without the trace.
func TestTheZoneNamesTheLevelItWasBuiltFrom(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*entryplan.Snapshot)
		want string
	}{
		{"MA20", func(s *entryplan.Snapshot) {}, string(entryplan.LevelMA20)},
		{"MA60", func(s *entryplan.Snapshot) {
			s.MA20 = entryplan.PriceObservation{}
			s.BaseLow = entryplan.PriceObservation{}
		}, string(entryplan.LevelMA60)},
		{"base low", func(s *entryplan.Snapshot) {
			s.MA20 = entryplan.PriceObservation{}
			s.MA60 = entryplan.PriceObservation{}
		}, string(entryplan.LevelBaseLow)},
		{"pivot", breakout, string(entryplan.LevelBreakoutHigh)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := zoneBase()
			c.mut(&in)
			p := entryplan.ComputePlan(in)
			if p.IdealEntry == nil {
				t.Fatalf("no zone; reasons %v", p.Reasons)
			}
			if got := p.IdealEntry.Basis; len(got) != 1 || got[0] != c.want {
				t.Errorf("zone basis = %v, want [%s]", got, c.want)
			}
			if !entryplan.KnownLevelSource(entryplan.LevelSource(p.IdealEntry.Basis[0])) {
				t.Errorf("zone basis %q is not a registered LevelSource — a free string here "+
					"is a code that joins back to nothing", p.IdealEntry.Basis[0])
			}
		})
	}
}

// WidthATR measures the PUBLISHED zone, not the raw one, because it is what a reader compares
// across stocks and the raw band is not what anyone can buy.
func TestTheZoneWidthIsMeasuredInATRs(t *testing.T) {
	in := zoneBase() // zone 92 → 96, ATR 4
	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil || p.IdealEntry.WidthATR == nil {
		t.Fatalf("no zone or no width measurement: %+v", p.IdealEntry)
	}
	if *p.IdealEntry.WidthATR != 1 {
		t.Errorf("width = %v ATRs, want 1.0 — the published band is 4.0 wide and the ATR is "+
			"4.0", *p.IdealEntry.WidthATR)
	}
}

// The regime decides whether a zone exists AT ALL, and the two refusals are different facts.
func TestTheRegimePolicyDecidesWhetherAZoneExists(t *testing.T) {
	cases := []struct {
		regime     model.Regime
		semantic   entryplan.EntrySemantic
		wantZone   bool
		wantReason entryplan.Reason
		notReason  entryplan.Reason
	}{
		{model.RegimeBull, entryplan.EntrySemanticPullback, true, "", ""},
		{model.RegimeBull, entryplan.EntrySemanticBreakout, true, "", ""},
		{model.RegimeBullPullback, entryplan.EntrySemanticPullback, true, "", ""},
		{model.RegimeBullPullback, entryplan.EntrySemanticBreakout, false,
			entryplan.ReasonZoneSemanticNotPermitted, entryplan.ReasonZonePolicyUnresolved},
		{model.RegimeSideways, entryplan.EntrySemanticPullback, true, "", ""},
		{model.RegimeSideways, entryplan.EntrySemanticBreakout, false,
			entryplan.ReasonZoneSemanticNotPermitted, entryplan.ReasonZonePolicyUnresolved},
		{model.RegimeDistribution, entryplan.EntrySemanticPullback, true, "", ""},
		{model.RegimeDistribution, entryplan.EntrySemanticBreakout, false,
			entryplan.ReasonZoneSemanticNotPermitted, entryplan.ReasonZonePolicyUnresolved},
		{model.RegimeBear, entryplan.EntrySemanticPullback, false,
			entryplan.ReasonZoneSemanticNotPermitted, entryplan.ReasonZonePolicyUnresolved},
		{model.RegimeBear, entryplan.EntrySemanticBreakout, false,
			entryplan.ReasonZoneSemanticNotPermitted, entryplan.ReasonZonePolicyUnresolved},
		// UNKNOWN is NOT a prohibition. Different code, and the zone is absent for a
		// different reason.
		{model.RegimeUnknown, entryplan.EntrySemanticPullback, false,
			entryplan.ReasonZonePolicyUnresolved, entryplan.ReasonZoneSemanticNotPermitted},
		{model.Regime("MELT_UP"), entryplan.EntrySemanticPullback, false,
			entryplan.ReasonZonePolicyUnresolved, entryplan.ReasonZoneSemanticNotPermitted},
	}
	for _, c := range cases {
		name := string(c.regime) + "/" + string(c.semantic)
		t.Run(name, func(t *testing.T) {
			in := zoneBase()
			in.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: c.regime}
			in.EntrySemantic = entryplan.EntrySemanticEvidence{Status: entryplan.Available,
				Semantic: c.semantic}
			p := entryplan.ComputePlan(in)

			if got := p.IdealEntry != nil; got != c.wantZone {
				t.Errorf("zone present = %v, want %v; reasons %v", got, c.wantZone, p.Reasons)
			}
			if c.wantReason != "" && !hasReason(p, c.wantReason) {
				t.Errorf("reasons = %v, want %q", p.Reasons, c.wantReason)
			}
			if c.notReason != "" && hasReason(p, c.notReason) {
				t.Errorf("reasons = %v must not contain %q — a refusal and the absence of a "+
					"decision are different facts", p.Reasons, c.notReason)
			}
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────────────

type executablePrice struct {
	field string
	value float64
}

// executablePrices enumerates the numbers a reader could place an order from.
//
// ENUMERATED, not reflected, and that is deliberate here: "may a reader act on this field" is
// a judgement, and each future price-bearing field's own work item has to make it. The
// reflective counterpart is TestTheGateWithdrawsExactlyTheAffectedFields, which holds this list
// against Plan's actual type so a new field cannot be silently omitted.
//
// The trace is NOT in here. It holds raw bounds, unaligned centres and refused arithmetic on
// purpose — see EntryTrace — and treating those as executable would make the sweep assert the
// opposite of what the type says.
func executablePrices(p entryplan.Plan) []executablePrice {
	var out []executablePrice
	if z := p.IdealEntry; z != nil {
		out = append(out,
			executablePrice{"IdealEntry.Low", z.Low},
			executablePrice{"IdealEntry.High", z.High})
	}
	if p.MaxChasePrice != nil {
		out = append(out, executablePrice{"MaxChasePrice", *p.MaxChasePrice})
	}
	if p.Invalidation != nil {
		out = append(out, executablePrice{"Invalidation.Price", p.Invalidation.Price})
	}
	if p.Target1 != nil {
		out = append(out, executablePrice{"Target1.Price", p.Target1.Price})
	}
	if p.Target2 != nil {
		out = append(out, executablePrice{"Target2.Price", p.Target2.Price})
	}
	return out
}

func dispositions(cands []entryplan.CandidateLevel) map[entryplan.LevelSource]entryplan.LevelDisposition {
	out := map[entryplan.LevelSource]entryplan.LevelDisposition{}
	for _, c := range cands {
		out[c.Source] = c.Disposition
	}
	return out
}

// priceRuleTickCents asks PRODUCTION for the tick, in cents, so the two tables can be
// compared as integers. It is the only place a test in this file calls pricerule.
func priceRuleTickCents(price float64) (int64, bool) {
	tick, ok := pricerule.TickSize(price)
	if !ok {
		return 0, false
	}
	return int64(math.Round(tick * 100)), true
}

// formatFloat renders a float for a failure message without pulling in a formatting rule that
// could round a value the message is about.
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func renderCandidates(cands []entryplan.CandidateLevel) string {
	var s string
	for _, c := range cands {
		s += string(c.Source) + "="
		if c.Value == nil {
			s += "nil"
		} else {
			s += formatFloat(*c.Value)
		}
		s += "(" + string(c.Disposition) + ") "
	}
	return s
}
