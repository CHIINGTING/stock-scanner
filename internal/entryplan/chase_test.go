package entryplan_test

import (
	"math"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/pricerule"
)

// ── EP-3b: the chase ceiling ──────────────────────────────────────────────────────────

// ── an INDEPENDENT limit-up ───────────────────────────────────────────────────────────
//
// The day's highest legal price, computed here in integer TENTHS OF A CENT and aligned with
// this file's own transcribed tick table. It does not call pricerule, for the reason
// zone_test.go keeps a second tick table: a clamp checked by the function that computed it
// agrees with itself.
//
// Tenths of a cent, because that is the exact grain of ±10% of a whole-cent price — 95909
// cents × 1.1 is 105499.9 cents exactly — and pricerule's own doc records that a coarser
// grain here produced 316 illegal ceilings in a sweep.
func independentLimitUp(prevClose float64) (float64, bool) {
	cents := math.Round(prevClose * 100)
	if math.Abs(prevClose*100-cents) > 1e-6 || cents <= 0 {
		return 0, false
	}
	// Exact +10%, in tenths of a cent.
	tenths := int64(cents) * 11
	price := float64(tenths) / 1000
	tickCents, ok := tickCentsFor(price)
	if !ok {
		return 0, false
	}
	// Align DOWN onto the tick grid, staying inside the band.
	units := tenths / (10 * tickCents)
	limitCents := units * tickCents
	if limitCents <= 0 {
		return 0, false
	}
	return float64(limitCents) / 100, true
}

func TestTheTwoLimitUpComputationsAgree(t *testing.T) {
	var checked int
	for _, prevClose := range []float64{
		0.5, 1, 9.99, 10, 33.5, 36.85, 49.95, 50, 98, 100, 500, 959.09, 990, 1000, 1245, 19795,
	} {
		want, wantOK := independentLimitUp(prevClose)
		got, gotOK := priceRuleLimitUp(prevClose)
		if wantOK != gotOK {
			t.Errorf("prevClose %v: availability differs — transcribed rule %v, pricerule %v",
				prevClose, wantOK, gotOK)
			continue
		}
		if !wantOK {
			continue
		}
		if got != want {
			t.Errorf("prevClose %v: pricerule says the ceiling is %v, the transcribed ±10%% "+
				"rule says %v", prevClose, got, want)
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("only %d ceilings compared", checked)
	}
}

// ── the three nils EP-2 refused to merge ──────────────────────────────────────────────

// DISALLOWED, UNRESOLVED and "the previous close is missing" produce three different codes,
// and each is asserted to EXCLUDE the other two.
//
// This is the test that fails if the two policy nils are ever folded together. ChasePolicy
// split the decision from the number for exactly this, and a consumer that merged them would
// report an unreadable market ("we could not tell what the regime is") as a bearish one ("the
// regime forbids chasing").
func TestTheThreeChaseNilsAreDistinct(t *testing.T) {
	cases := []struct {
		name       string
		mut        func(*entryplan.Snapshot)
		want       entryplan.Reason
		wantZone   bool
		wantOthers []entryplan.Reason
	}{
		{
			name: "DISALLOWED — a decision, on sufficient evidence (BEAR)",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeBear}
			},
			want: entryplan.ReasonChaseNotAllowed,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChasePolicyUnresolved,
				entryplan.ReasonPreviousCloseUnavailable,
			},
		},
		{
			name: "DISALLOWED with the zone still allowed (SIDEWAYS)",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeSideways}
			},
			want:     entryplan.ReasonChaseNotAllowed,
			wantZone: true,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChasePolicyUnresolved,
				entryplan.ReasonPreviousCloseUnavailable,
			},
		},
		{
			name: "UNRESOLVED — no decision was made (UNKNOWN regime)",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.RegimeUnknown}
			},
			want: entryplan.ReasonChasePolicyUnresolved,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChaseNotAllowed,
				entryplan.ReasonPreviousCloseUnavailable,
			},
		},
		{
			name: "UNRESOLVED — the regime string is not a regime at all",
			mut: func(s *entryplan.Snapshot) {
				s.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: model.Regime("MELT_UP")}
			},
			want: entryplan.ReasonChasePolicyUnresolved,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChaseNotAllowed,
				entryplan.ReasonPreviousCloseUnavailable,
			},
		},
		{
			name: "ALLOWED, and the previous close is missing — a missing INPUT",
			mut: func(s *entryplan.Snapshot) {
				s.PreviousClose = entryplan.PriceObservation{}
			},
			want:     entryplan.ReasonPreviousCloseUnavailable,
			wantZone: true,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChaseNotAllowed,
				entryplan.ReasonChasePolicyUnresolved,
			},
		},
		{
			name: "ALLOWED, and the previous close is labelled available with no number",
			mut: func(s *entryplan.Snapshot) {
				s.PreviousClose = entryplan.PriceObservation{Status: entryplan.Available}
			},
			want:     entryplan.ReasonPreviousCloseUnavailable,
			wantZone: true,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChaseNotAllowed,
				entryplan.ReasonChasePolicyUnresolved,
			},
		},
		{
			name: "ALLOWED, and the previous close is 0 — MISSING ≠ ZERO",
			mut: func(s *entryplan.Snapshot) {
				s.PreviousClose = obs(0)
			},
			want:     entryplan.ReasonPreviousCloseUnavailable,
			wantZone: true,
			wantOthers: []entryplan.Reason{
				entryplan.ReasonChaseNotAllowed,
				entryplan.ReasonChasePolicyUnresolved,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := zoneBase()
			c.mut(&in)
			p := entryplan.ComputePlan(in)

			if p.MaxChasePrice != nil {
				t.Fatalf("a chase ceiling of %v was published", *p.MaxChasePrice)
			}
			if !hasReason(p, c.want) {
				t.Errorf("reasons = %v, want %q", p.Reasons, c.want)
			}
			for _, other := range c.wantOthers {
				if hasReason(p, other) {
					t.Errorf("reasons = %v also carries %q — the three nils are being "+
						"merged, and that is the one distinction EP-2 exists to protect",
						p.Reasons, other)
				}
			}
			if got := p.IdealEntry != nil; got != c.wantZone {
				t.Errorf("zone present = %v, want %v — the zone's availability and the "+
					"ceiling's are independent", got, c.wantZone)
			}
			if p.EntryTrace.Chase.Rejection != c.want {
				t.Errorf("trace rejection = %q, want %q", p.EntryTrace.Chase.Rejection, c.want)
			}
		})
	}
}

// A policy that permits chasing without naming a tolerance has RESOLVED NO TOLERANCE.
//
// ChasePolicy's invariant (MaxATR != nil ⟺ ALLOWED) makes this unreachable through
// PolicyFor, so the policy is built BY HAND — the same inversion the invariant checker uses.
// It maps to UNRESOLVED and not to NOT_ALLOWED, because "no number was resolved" is not
// "chasing is forbidden".
func TestAnAllowedChaseWithNoMultiplierIsUnresolved(t *testing.T) {
	in := zoneBase()
	zoneRes := entryplan.ComputeIdealEntryZone(in,
		entryplan.PolicyFor(model.RegimeBull, entryplan.EntrySemanticPullback))
	if zoneRes.Zone == nil {
		t.Fatalf("the fixture produced no zone: %q", zoneRes.Trace.Rejection)
	}

	broken := entryplan.PolicyResult{
		Regime:   model.RegimeBull,
		Semantic: entryplan.EntrySemanticPullback,
		Policy: &entryplan.EntryPolicy{
			Name:   entryplan.PolicyNormal,
			Regime: model.RegimeBull,
			Chase:  entryplan.ChasePolicy{Allowance: entryplan.AllowanceAllowed}, // no MaxATR
		},
		Allowance: entryplan.AllowanceAllowed,
	}
	got := entryplan.ComputeMaxChase(in, zoneRes.Zone, broken)
	if got.MaxChasePrice != nil {
		t.Errorf("a ceiling of %v was computed from a policy with no multiplier — the "+
			"multiplier would have had to be invented", *got.MaxChasePrice)
	}
	if got.Trace.Rejection != entryplan.ReasonChasePolicyUnresolved {
		t.Errorf("rejection = %q, want CHASE_POLICY_UNRESOLVED", got.Trace.Rejection)
	}

	// The control: the same call with the real policy DOES produce a ceiling, so the
	// assertion above is about the multiplier and not about the fixture.
	ok := entryplan.ComputeMaxChase(in, zoneRes.Zone,
		entryplan.PolicyFor(model.RegimeBull, entryplan.EntrySemanticPullback))
	if ok.MaxChasePrice == nil {
		t.Fatalf("the control produced no ceiling either (%q) — the test above proves "+
			"nothing", ok.Trace.Rejection)
	}
}

// ── the clamp, and the order it happens in ────────────────────────────────────────────

// THE TWO CEILINGS THE PUBLISHED PRICE MUST RESPECT, over a sweep:
//
//	MaxChase <= limitUp   the daily price limit, checked against the INDEPENDENT ±10%
//	MaxChase <= rawChase  its own arithmetic — this is the one that fails if the alignment
//	                      direction is flipped to UP
//
// plus MaxChase >= IdealEntry.High, which is InvariantMaxChaseCoversZone.
//
// This is the assertion that pins the ORDER (clamp, then align) as well as the direction: an
// alignment applied before the clamp would leave a value the clamp then has to re-check, and
// the sweep would catch a result above the band.
func TestTheChaseCeilingRespectsBothLimits(t *testing.T) {
	var checked, clamped, unclamped int

	for _, prevClose := range []float64{9.9, 33.5, 98, 100, 250, 760, 999, 1245} {
		for _, level := range []float64{8.93, 27.77, 71.43, 287.77, 761.11, 1245.3} {
			for _, atrValue := range []float64{0.01, 0.37, 4, 33.33, 60} {
				for _, regime := range []model.Regime{model.RegimeBull, model.RegimeBullPullback} {
					in := zoneBase()
					in.CurrentPrice = obs(level * 1.05)
					in.MA20 = obs(level)
					in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
					in.ATR = atr(atrValue)
					in.PreviousClose = obs(prevClose)
					in.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: regime}

					p := entryplan.ComputePlan(in)
					if p.MaxChasePrice == nil {
						continue
					}
					checked++
					chase := *p.MaxChasePrice
					tr := p.EntryTrace.Chase

					limitUp, ok := independentLimitUp(prevClose)
					if !ok {
						t.Fatalf("the independent ±10%% refused prevClose %v while production "+
							"published a ceiling of %v", prevClose, chase)
					}
					if chase > limitUp {
						t.Errorf("prevClose %v, level %v, ATR %v: chase %v is ABOVE the day's "+
							"highest legal price %v — the exchange rejects the order",
							prevClose, level, atrValue, chase, limitUp)
					}
					if tr.Raw == nil {
						t.Fatalf("no raw chase on the trace")
					}
					// snapPriceTol, pricerule's stated and tested cost: RoundToTick may
					// return a value up to 0.0001 元 above its input when the input sits a
					// hair BELOW a grid point, which is the price of not losing a whole tick
					// to one ULP of a 60-term mean. It is 100× smaller than the finest tick
					// the exchange quotes, so an alignment flipped to UP still fails this
					// assertion by a whole tick — that is what makes the tolerance safe here
					// rather than a hole in it.
					const snapTol = 1e-4
					if chase > *tr.Raw+snapTol {
						t.Errorf("prevClose %v, level %v, ATR %v: chase %v is ABOVE its own "+
							"raw limit %v — a ceiling aligned UP permits paying more than the "+
							"rule computed", prevClose, level, atrValue, chase, *tr.Raw)
					}
					if p.IdealEntry != nil && chase < p.IdealEntry.High {
						t.Errorf("chase %v is below the top of its own zone %v", chase,
							p.IdealEntry.High)
					}
					switch tr.Clamp {
					case entryplan.ClampToLimitUp:
						clamped++
						if chase != limitUp {
							t.Errorf("prevClose %v: the clamp fired and the answer is %v, not "+
								"the ceiling %v", prevClose, chase, limitUp)
						}
					case entryplan.ClampNotApplied:
						unclamped++
					default:
						t.Errorf("clamp outcome = %q", tr.Clamp)
					}
				}
			}
		}
	}

	if checked < 100 {
		t.Fatalf("only %d ceilings were produced by the sweep", checked)
	}
	// ANTI-VACUITY: both branches of the clamp must have fired somewhere, or half of this
	// test never ran.
	if clamped == 0 || unclamped == 0 {
		t.Errorf("the sweep produced %d clamped and %d unclamped ceilings — a branch with no "+
			"observations is an assertion that never ran", clamped, unclamped)
	}
}

// THE BOUNDARY: the raw chase lands exactly ON the legal ceiling.
//
// centre 80, ATR 20 → half width 10 → zone 70 → 90, raw chase 90 + 1.0 × 20 = 110, and the
// +10% ceiling on a previous close of 100 is exactly 110.00. The answer must be 110 — not
// 109.5 (a tick shaved off by an over-eager clamp) and not 110.5 — and the clamp must report
// NOT_CLAMPED, because `raw > limitUp` is false at equality.
func TestTheBoundaryWhereTheRawChaseEqualsTheLegalCeiling(t *testing.T) {
	in := zoneBase()
	in.CurrentPrice, in.MA20 = obs(100), obs(80)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.ATR, in.PreviousClose = atr(20), obs(100)

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil || p.IdealEntry.Low != 70 || p.IdealEntry.High != 90 {
		t.Fatalf("zone = %+v, want 70 → 90", p.IdealEntry)
	}
	if p.MaxChasePrice == nil {
		t.Fatalf("no ceiling; reasons %v", p.Reasons)
	}
	if *p.MaxChasePrice != 110 {
		t.Errorf("chase = %v, want exactly 110 — the raw limit and the legal ceiling are the "+
			"same number here, so any difference is an off-by-one-tick", *p.MaxChasePrice)
	}
	tr := p.EntryTrace.Chase
	if tr.Clamp != entryplan.ClampNotApplied {
		t.Errorf("clamp = %q, want NOT_CLAMPED: at equality the raw limit is already legal, "+
			"and reporting a clamp would say the band moved the price when it did not", tr.Clamp)
	}
	if tr.Raw == nil || *tr.Raw != 110 || tr.LimitUp == nil || *tr.LimitUp != 110 {
		t.Errorf("trace raw = %v, limit up = %v, want both 110", tr.Raw, tr.LimitUp)
	}
}

// THE ALIGNMENT DIRECTION. A ceiling is aligned DOWN.
//
// zone high 96.4 + 1.0 × 4.8 = 101.2, and the tier at 101.2 is 0.50: DOWN gives 101.00, UP
// would give 101.50 — a legal price ABOVE the limit the rule just computed. The daily band
// (107.50 here) does not catch that, which is the whole point of asserting the direction
// separately from the clamp.
func TestTheChaseCeilingIsAlignedDown(t *testing.T) {
	in := zoneBase()
	in.MA20, in.MA60, in.BaseLow = obs(94), entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.ATR = atr(4.8) // half width 2.4 → zone 91.6 → 96.4

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil || p.IdealEntry.High != 96.4 {
		t.Fatalf("zone = %+v, want a high of 96.4", p.IdealEntry)
	}
	if p.MaxChasePrice == nil {
		t.Fatalf("no ceiling; reasons %v", p.Reasons)
	}
	if *p.MaxChasePrice != 101 {
		t.Errorf("chase = %v, want 101.0: the raw limit is 101.2 and the tier is 0.50, so "+
			"aligning DOWN gives 101.0. 101.5 means it was aligned UP, and the plan would "+
			"then permit paying 0.30 more than its own arithmetic allows", *p.MaxChasePrice)
	}
	if p.EntryTrace.Chase.TickDirection != entryplan.TickDown {
		t.Errorf("recorded direction = %q, want DOWN", p.EntryTrace.Chase.TickDirection)
	}
}

// THE PREVIOUS CLOSE IS NOT THE CURRENT PRICE, and the fixture is built so the two answers
// differ by a tick that matters.
//
// previous close 100 → ceiling 110.0, and the raw chase is 111.0, so the clamp binds and the
// answer is 110.0. The current price is 120: banding around IT would give a ceiling of 132.0,
// no clamp at all, and a published 111.0.
func TestTheDailyBandIsComputedFromThePreviousCloseAndNotTheCurrentPrice(t *testing.T) {
	in := zoneBase()
	in.CurrentPrice, in.MA20 = obs(120), obs(105)
	in.MA60, in.BaseLow = entryplan.PriceObservation{}, entryplan.PriceObservation{}
	in.ATR, in.PreviousClose = atr(4), obs(100)

	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil || p.IdealEntry.High != 107 {
		t.Fatalf("zone = %+v, want a high of 107", p.IdealEntry)
	}
	if p.MaxChasePrice == nil {
		t.Fatalf("no ceiling; reasons %v", p.Reasons)
	}
	if *p.MaxChasePrice != 110 {
		t.Errorf("chase = %v, want 110.0 — the +10%% band on the PREVIOUS close of 100. "+
			"111.0 means the band was computed from the current price of 120, which gives a "+
			"ceiling that moves with the very thing it is supposed to bound", *p.MaxChasePrice)
	}
	tr := p.EntryTrace.Chase
	if tr.PreviousClose == nil || *tr.PreviousClose != 100 {
		t.Errorf("trace previous close = %v, want 100", tr.PreviousClose)
	}
	if tr.Clamp != entryplan.ClampToLimitUp {
		t.Errorf("clamp = %q, want CLAMPED_TO_LIMIT_UP", tr.Clamp)
	}
}

// The zone survives every chase refusal, and a zone refusal leaves the chase with NO CODE OF
// ITS OWN — the zone's reason is the answer, and inventing a second one would read as two
// independent failures.
func TestChaseAndZoneAvailabilityAreIndependent(t *testing.T) {
	// Chase gone, zone kept.
	in := zoneBase()
	in.PreviousClose = entryplan.PriceObservation{}
	p := entryplan.ComputePlan(in)
	if p.IdealEntry == nil {
		t.Errorf("the zone was withdrawn because the previous close was missing; reasons %v",
			p.Reasons)
	}
	if p.MaxChasePrice != nil {
		t.Errorf("a ceiling of %v was published with no previous close", *p.MaxChasePrice)
	}

	// Zone gone, chase permitted: no chase, and no chase-specific code.
	in = zoneBase()
	in.MA20, in.MA60, in.BaseLow = entryplan.PriceObservation{},
		entryplan.PriceObservation{}, entryplan.PriceObservation{}
	p = entryplan.ComputePlan(in)
	if p.IdealEntry != nil || p.MaxChasePrice != nil {
		t.Fatalf("expected neither a zone nor a ceiling: %+v / %v", p.IdealEntry, p.MaxChasePrice)
	}
	if got := p.EntryTrace.Chase.Rejection; got != "" {
		t.Errorf("chase rejection = %q, want empty — there was no zone to chase into, and "+
			"the zone's own rejection (%q) is the answer", got, p.EntryTrace.Zone.Rejection)
	}
	if p.EntryTrace.Zone.Rejection == "" {
		t.Error("neither step gave a reason for having no prices — that is a shrug")
	}
	// The policy WAS read, even with no zone: the chase allowance is a fact about the regime.
	if p.EntryTrace.Policy.ChaseAllowance != entryplan.AllowanceAllowed {
		t.Errorf("chase allowance = %q, want ALLOWED (BULL) — withholding the policy until a "+
			"zone exists makes the answer depend on an unrelated input",
			p.EntryTrace.Policy.ChaseAllowance)
	}
}

// The chase trace must hold every term, so the published ceiling can be recomputed by hand.
func TestTheChaseTraceRecordsEveryTerm(t *testing.T) {
	p := entryplan.ComputePlan(zoneBase()) // zone 92 → 96, ATR 4, BULL (1.0), prevClose 98
	tr := p.EntryTrace.Chase

	for _, c := range []struct {
		name string
		got  *float64
		want float64
	}{
		{"zone high", tr.ZoneHigh, 96},
		{"max ATR multiple", tr.MaxATR, 1.0},
		{"ATR", tr.ATR, 4},
		{"raw ceiling", tr.Raw, 100},
		{"previous close", tr.PreviousClose, 98},
		{"limit up", tr.LimitUp, 107.5},
		{"final", tr.Final, 100},
	} {
		if c.got == nil {
			t.Errorf("%s is absent from the trace", c.name)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, *c.got, c.want)
		}
	}
	if tr.LimitRule != "PCT_10" {
		t.Errorf("limit rule = %q, want PCT_10 for a four-digit non-00 code", tr.LimitRule)
	}
	if tr.TickDirection != entryplan.TickDown {
		t.Errorf("tick direction = %q, want DOWN", tr.TickDirection)
	}
	if tr.LimitAssumption == "" {
		t.Error("no limit assumption recorded — the ±10% band is computed from the exchange's " +
			"adjusted reference price on an ex-dividend day, and this package cannot check " +
			"which day it is")
	}
	// And the published number is the trace's number.
	if p.MaxChasePrice == nil || *p.MaxChasePrice != *tr.Final {
		t.Errorf("the plan says %v and the trace says %v", p.MaxChasePrice, tr.Final)
	}
}

// The caller-contract disclosure travels with any clamp, in both machine-readable and human
// form — and is ABSENT when no ceiling was computed, so it cannot be read as a blanket
// disclaimer.
func TestTheLimitAssumptionTravelsWithTheCeilingAndOnlyWithIt(t *testing.T) {
	const wantCode = "ASSUMES_ORDINARY_SESSION_AND_UNADJUSTED_REFERENCE_PRICE"

	p := entryplan.ComputePlan(zoneBase())
	if p.EntryTrace.Chase.LimitAssumption != wantCode {
		t.Errorf("assumption code = %q, want the literal %q — it is persisted, so a rename "+
			"has to be a decision", p.EntryTrace.Chase.LimitAssumption, wantCode)
	}
	var human bool
	for _, c := range p.Caveats {
		if containsAll(c, "漲停", "除權息", "五個交易日") {
			human = true
		}
	}
	if !human {
		t.Errorf("no caveat states the two things pricerule cannot verify (an ordinary "+
			"session, and not the first five days of a listing): %v", p.Caveats)
	}

	// No ceiling → no assumption. An assumption on a plan with no clamp in it would be a
	// disclaimer attached to nothing.
	in := zoneBase()
	in.PreviousClose = entryplan.PriceObservation{}
	p = entryplan.ComputePlan(in)
	if p.EntryTrace.Chase.LimitAssumption != "" {
		t.Errorf("assumption %q recorded on a plan with no ceiling",
			p.EntryTrace.Chase.LimitAssumption)
	}
}

// The regime's tolerance is what scales the headroom, and BULL_PULLBACK tolerates strictly
// less than BULL — the ORDERING EP-2 pinned, now observable in a price.
func TestTheRegimeTolerancesScaleTheHeadroom(t *testing.T) {
	headroom := func(regime model.Regime) float64 {
		in := zoneBase()
		in.Regime = entryplan.RegimeEvidence{Status: entryplan.Available, Regime: regime}
		p := entryplan.ComputePlan(in)
		if p.MaxChasePrice == nil || p.IdealEntry == nil {
			t.Fatalf("%s: no ceiling; reasons %v", regime, p.Reasons)
		}
		return *p.MaxChasePrice - p.IdealEntry.High
	}
	bull, pullback := headroom(model.RegimeBull), headroom(model.RegimeBullPullback)
	if !(pullback < bull) {
		t.Errorf("BULL_PULLBACK headroom %v is not less than BULL's %v — the whole premise of "+
			"the regime is that price is coming back to you", pullback, bull)
	}
	// Compared with a tolerance because the headroom is a DIFFERENCE of two published
	// prices, and 97.6 - 96 is 1.5999999999999943 in binary floating point. The prices
	// themselves are exact multiples of their tick (asserted elsewhere); the subtraction is
	// the test's own arithmetic.
	if math.Abs(bull-4) > 1e-9 || math.Abs(pullback-1.6) > 1e-9 {
		t.Errorf("headroom: BULL %v (want 1.0 × ATR = 4), BULL_PULLBACK %v (want 0.4 × ATR "+
			"= 1.6)", bull, pullback)
	}
}

// The reserved chase code must NOT appear on any plan.
//
// It is the `ok == false` branch on the final alignment, and it is UNREACHABLE: the clamped
// value is at most limitUp, and limitUp is itself a value pricerule returned from RoundToTick,
// so it is inside the alignable domain. The branch exists so that an ok == false is never
// discarded — the EP-0 review's finding was that `v, _ :=` fails on 0.6% of real inputs, which
// is exactly the rate at which nobody notices.
//
// This test does not claim the branch is exercised. It claims the opposite, and it is here so
// that the day the code DOES appear, somebody has to explain why.
func TestTheReservedChaseAlignmentCodeIsUnreachable(t *testing.T) {
	var checked int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		checked++
		if hasReason(p, entryplan.ReasonChaseTickAlignmentUnavailable) {
			t.Fatalf("%s produced CHASE_TICK_ALIGNMENT_UNAVAILABLE — the clamp is supposed to "+
				"bound its input inside pricerule's alignable domain, so either the clamp or "+
				"pricerule's domain has changed. Reasons: %v", c.name, p.Reasons)
		}
	}
	if checked == 0 {
		t.Fatal("no plans checked")
	}
	if !entryplan.ReservedReasons[entryplan.ReasonChaseTickAlignmentUnavailable] {
		t.Error("the code is not marked reserved, so the registry test would now demand an " +
			"input that produces it — and there is none")
	}
}

// priceRuleLimitUp asks PRODUCTION's rule for the ceiling. The only pricerule call in this
// file.
func priceRuleLimitUp(prevClose float64) (float64, bool) {
	return pricerule.LimitUpPrice(prevClose, pricerule.ClassifyLimitRule("2330"))
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── the tick-direction labels ─────────────────────────────────────────────────────────

// The persisted label must MEAN the direction it names.
//
// TickDirection is a stable code on the trace, and a mislabelled one is worse than no label:
// it says the ceiling was rounded down while the number went up. So each label is checked
// against pricerule's OWN String() for the same constant, and against the observable
// behaviour of that constant on an off-grid price.
func TestTickDirectionLabelsMatchPriceRule(t *testing.T) {
	cases := []struct {
		dir   pricerule.RoundDir
		label entryplan.TickDirection
		// want is RoundToTick(101.2) under this direction, where the tier is 0.50.
		want float64
	}{
		{pricerule.RoundDown, entryplan.TickDown, 101.0},
		{pricerule.RoundUp, entryplan.TickUp, 101.5},
		{pricerule.RoundNearest, entryplan.TickNearest, 101.0},
	}
	for _, c := range cases {
		if got := entryplan.TickDirection(c.dir.String()); got != c.label {
			t.Errorf("pricerule calls %v %q and the trace label is %q — a persisted label that "+
				"disagrees with the direction it names is worse than no label", c.dir,
				c.dir.String(), c.label)
		}
		got, ok := pricerule.RoundToTick(101.2, c.dir)
		if !ok {
			t.Fatalf("pricerule refused 101.2 under %v", c.dir)
		}
		if got != c.want {
			t.Errorf("%q gives %v for 101.2, want %v — the label and the behaviour have "+
				"parted company", c.label, got, c.want)
		}
	}
	// The registry covers all three, so a fourth direction added to pricerule cannot land on
	// an empty label.
	if len(entryplan.AllTickDirections) != 3 {
		t.Errorf("%d tick direction labels for pricerule's 3 directions",
			len(entryplan.AllTickDirections))
	}
}
