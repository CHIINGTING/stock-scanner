package entryplan_test

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// ── the plan invariant checker ────────────────────────────────────────────────────────
//
// What is under test here is the CHECKER, not ComputePlan. Every bad Plan below is built by
// hand, because production cannot build one: ComputePlan is invariant-clean over every input
// this package can be handed (TestEveryPlanComputePlanReturnsIsInvariantClean sweeps for
// exactly that, with a ledger), so a test that fed the checker only ComputePlan's output would
// be asserting over a population of clean plans and would pass with the checker's body deleted.
//
// That is the same inversion the package already uses twice and both are live guards:
// TestTheEntryPlanLayerStaysPure plants `os.WriteFile` lines that the sweep must catch, and
// TestNoNonFiniteFloatEscapes plants a fixture the walker must find 7 floats and 5 non-finite
// values in. In both cases the subject is the CHECKER and the planted input is the fixture,
// which is why neither is vacuous today.
//
// EP-4 added four invariants — INVALIDATION_BELOW_ZONE, TARGET1_ABOVE_ZONE,
// TARGET2_NOT_BELOW_TARGET1, RR_FINITE_POSITIVE — and with them the planted cases that trip
// each one alone, the BOUNDARY case for each (equality is illegal for the stop and legal for
// the target pair, and the two rows say so), and the legal complete EP-4 plan that stops the
// four from being satisfiable only by publishing nothing.

// ── fixtures ──────────────────────────────────────────────────────────────────────────

// legalZone is a zone of the shape EP-3b is expected to produce: two strictly positive
// bounds, ordered, with the optional width measurement present.
func legalZone(low, high float64) *entryplan.PriceZone {
	width := 0.62
	return &entryplan.PriceZone{
		Low: low, High: high, WidthATR: &width,
		PriceBasis: entryplan.PriceBasisRaw,
		Basis:      []string{"MA20"},
	}
}

// pricedPlan is a Plan carrying execution prices. NOTHING IN PRODUCTION BUILDS ONE — that is
// EP-3b's first job — so the shape is written out here so the checker has something to check.
//
// It is WAIT_PULLBACK with the reason DecideStatus's S17 arm actually publishes, and the reason
// is not decoration: EP-5's InvariantStatusHasReason requires every status to carry one, so a
// fixture without it would be ILLEGAL and every geometry case below would report two violations
// — the one it plants and one about itself. The pair here is a legal EP-5 plan in every respect
// the status invariants care about, which is what lets the geometry cases plant exactly one
// thing each.
//
// It carries NO EntryTrace, and that is also deliberate: the three price-bearing status
// invariants read the quote off EntryTrace.Decision, and a hand-built plan has no quote, so
// they must skip rather than fire. The cases that DO exercise them supply a trace, and
// TestTheStatusInvariantsSkipRatherThanFireWithoutAQuote holds the skip.
func pricedPlan(z *entryplan.PriceZone, chase *float64) entryplan.Plan {
	return entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09",
		Status:        entryplan.StatusWaitPullback,
		Reasons:       []entryplan.Reason{entryplan.ReasonStatusPriceAboveEntryZone},
		RuleVersion:   entryplan.RuleVersion,
		IdealEntry:    z,
		MaxChasePrice: chase,
		Confidence:    entryplan.ConfidenceMedium,
	}
}

// decided stamps a plan with the decision trace the EP-5 invariants read: the entry SHAPE the
// verdict was made about, and the QUOTE it was made against.
//
// Plan carries no price of its own — a plan is a statement about levels, not a quote — so this
// is the only way a hand-built fixture can exercise BUY_NOW_PRICE_AUTHORISED or
// TOO_EXTENDED_ABOVE_CEILING at all.
//
// EP-6D: the trace also records a CLASSIFIED, non-contradicting thesis standing, because that is
// what a decided BUY_NOW legitimately carries — without it every legal BUY_NOW fixture below
// would trip BUY_NOW_THESIS_NOT_CONTRADICTED for a reason unrelated to what it plants. The
// thesis fixtures override it with withThesis.
func decided(p entryplan.Plan, sem entryplan.EntrySemantic, cur float64) entryplan.Plan {
	p.EntryTrace = &entryplan.EntryTrace{
		RuleVersion: entryplan.RuleVersion,
		Decision: entryplan.DecisionTrace{
			Semantic: sem, CurrentPrice: f(cur), Status: p.Status,
			Thesis: entryplan.ThesisNotContradicted,
		},
	}
	return p
}

// s1Plan is what ComputePlan publishes for a CONTRADICTED thesis: NO_VALID_ENTRY at S1, no
// executable price, empty step traces. Fixtures plant exactly one thing into it.
func s1Plan() entryplan.Plan {
	p := withThesis(decided(statused(pricedPlan(nil, nil), entryplan.StatusNoValidEntry,
		entryplan.ReasonStatusThesisContradicted), entryplan.EntrySemanticPullback, 850),
		entryplan.ThesisContradicted)
	p.EntryTrace.Decision.Rule = entryplan.RuleThesisContradicted
	return p
}

// withThesis replaces the thesis standing on an already-decided plan's trace. The trace is
// copied, so the fixture it was built from is not edited.
func withThesis(p entryplan.Plan, t entryplan.ThesisStanding) entryplan.Plan {
	tr := *p.EntryTrace
	tr.Decision.Thesis = t
	p.EntryTrace = &tr
	return p
}

// statused sets a status and the single reason that defends it, together, because the checker
// requires the pair and a fixture that set only one would be planting a second violation.
func statused(p entryplan.Plan, st entryplan.EntryStatus, why entryplan.Reason) entryplan.Plan {
	p.Status, p.Reasons = st, []entryplan.Reason{why}
	return p
}

// ep1ShapedPlan is what ComputePlan actually returns today: a verdict, a census, and not one
// execution price. It must produce ZERO violations — a checker that flagged the current output
// would be unusable the moment anyone wired it in.
func ep1ShapedPlan() entryplan.Plan {
	return entryplan.Plan{
		Symbol: "2330", AsOf: "2026-09-09",
		Status:      entryplan.StatusInsufficientData,
		RuleVersion: entryplan.RuleVersion,
		Confidence:  entryplan.ConfidenceHigh,
		Reasons:     []entryplan.Reason{entryplan.ReasonStatusEntryZoneUnavailable},
		Evidence: []entryplan.Evidence{
			{Key: entryplan.EvidenceCurrentPrice, Status: entryplan.Available, Value: f(902.5),
				PriceBasis: entryplan.PriceBasisRaw},
			{Key: entryplan.EvidenceMarketRegime, Status: entryplan.Available, Text: "BULL"},
		},
	}
}

// plantedBrokenPlan trips ALL SEVEN OF EP-3a's invariants at once, and is the fixture the
// counting self-test measures the checker against.
//
// It trips NONE of EP-4's four, and that is worth knowing rather than accidental: the
// invalidation is -Inf (which really is below a zone low of 0), Target1 is 900 (above a zone
// high of -5), Target2 is NaN (and `NaN < 900` is false, exactly as `NaN <= 0` is false for a
// zone bound) and the RiskReward is a legal 10/20/2. So EP-4's codes get their own planted
// cases below rather than riding along on this one.
//
// Every number in it is deliberate:
//
//	IdealEntry.Low  = 0        a bound that is not a price
//	IdealEntry.High = -5       a bound that is not a price, and below the low
//	IdealEntry.WidthATR = NaN  a NaN behind a POINTER — the walk must dereference to see it
//	MaxChasePrice   = -6       not positive, and below the zone high
//	Invalidation.Price = -Inf  an EP-4 field: swept for finiteness, not for meaning
//	Target1.RMultiple  = +Inf  behind a pointer, inside a pointer to a struct
//	Target2.Price      = NaN   a plain float64 in a struct EP-1 never built
//	Evidence[0].Value  = NaN   behind a pointer, inside a SLICE element
//	Evidence[1].Value  = nil   a nil pointer the walk must step over without counting
func plantedBrokenPlan() entryplan.Plan {
	p := pricedPlan(legalZone(0, -5), f(-6))
	p.IdealEntry.WidthATR = f(math.NaN())
	p.Invalidation = &entryplan.Invalidation{Price: math.Inf(-1), PriceBasis: entryplan.PriceBasisRaw}
	p.Target1 = &entryplan.PriceLevel{Price: 900, RMultiple: f(math.Inf(1))}
	p.Target2 = &entryplan.PriceLevel{Price: math.NaN()}
	p.RiskReward = &entryplan.RiskReward{RiskPerShare: 10, RewardPerShare: 20, Ratio: 2,
		Target: "TARGET_1"}
	p.Evidence = []entryplan.Evidence{
		{Key: "X", Status: entryplan.Available, Value: f(math.NaN())},
		{Key: "Y", Status: entryplan.Unavailable},
	}
	return p
}

// The float and violation census of plantedBrokenPlan, stated as numbers so the counting
// self-test below is an assertion and not a restatement of whatever the checker did.
//
//	floats  3 in the zone (Low, High, WidthATR*) + 1 chase + 1 invalidation
//	        + 2 in Target1 (Price, RMultiple*) + 1 in Target2 + 3 in RiskReward
//	        + 1 evidence value = 12
//	zones   1
//	bad     3 NaN + 2 Inf + 3 zone geometry + 2 chase = 10
const (
	plantedFloats     = 12
	plantedZones      = 1
	plantedViolations = 10
)

func invariantCounts(vs []entryplan.PlanViolation) map[entryplan.PlanInvariant]int {
	got := map[entryplan.PlanInvariant]int{}
	for _, v := range vs {
		got[v.Invariant]++
	}
	return got
}

func renderViolations(vs []entryplan.PlanViolation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.String())
	}
	return out
}

// ── 1. the planted bad plans, one per invariant ───────────────────────────────────────

type invariantCase struct {
	name string
	plan entryplan.Plan
	// want is the EXACT multiset of invariants the checker must report, not "at least".
	// "At least" is how a checker acquires a second violation nobody asked for — and how a
	// check that fires on the WRONG field goes unnoticed, because something did fire.
	want map[entryplan.PlanInvariant]int
}

func invariantCases() []invariantCase {
	return []invariantCase{
		// ── legal plans. A checker that cannot say "this one is fine" is not a checker.
		{name: "an EP-1 shaped plan with every price pointer nil", plan: ep1ShapedPlan()},
		{name: "a legal zone with a chase limit above it",
			plan: pricedPlan(legalZone(840, 860), f(866))},
		{name: "a legal zone with no chase limit at all",
			plan: pricedPlan(legalZone(840, 860), nil)},
		{
			// THE BOUNDARY. Equality is legal: "buy in the zone, pay nothing above it" is the
			// tightest honest plan, and a checker that rejected it would force every producer
			// to invent a slack it has no basis for.
			name: "a chase limit exactly at the top of the zone",
			plan: pricedPlan(legalZone(840, 860), f(860)),
		},
		{name: "a chase limit with no zone to compare it against",
			plan: pricedPlan(nil, f(866))},

		// ── ZONE_LOW_POSITIVE
		{
			name: "zone low is zero",
			plan: pricedPlan(legalZone(0, 860), nil),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantZoneLowPositive: 1},
		},
		{
			name: "zone low is negative",
			plan: pricedPlan(legalZone(-840, 860), nil),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantZoneLowPositive: 1},
		},

		// ── ZONE_HIGH_POSITIVE. It cannot be tripped ALONE: a non-positive high with a
		// positive low is also an inverted zone, and a non-positive high with a
		// non-positive low trips the low bound too. The exact multiset says so out loud.
		{
			name: "zone high is zero",
			plan: pricedPlan(legalZone(840, 0), nil),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantZoneHighPositive: 1,
				entryplan.InvariantZoneOrdered:      1,
			},
		},
		{
			name: "both bounds are zero",
			plan: pricedPlan(legalZone(0, 0), nil),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantZoneLowPositive:  1,
				entryplan.InvariantZoneHighPositive: 1,
			},
		},

		// ── ZONE_LOW_NOT_ABOVE_HIGH, alone: both bounds are real prices, in the wrong order.
		{
			name: "the zone is inverted",
			plan: pricedPlan(legalZone(880, 850), nil),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantZoneOrdered: 1},
		},

		// ── FLOAT_NOT_NAN. Note what does NOT fire: NaN <= 0 is false and NaN > High is
		// false, so a NaN bound trips exactly one invariant. That is the reason NaN is its
		// own invariant rather than being folded into the positivity check — it does not
		// make the positivity check fail, it makes it PASS.
		{
			name: "a zone bound is NaN",
			plan: pricedPlan(legalZone(math.NaN(), 860), nil),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantFloatNotNaN: 1},
		},
		{
			name: "the zone width measurement is NaN behind a pointer",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), nil)
				p.IdealEntry.WidthATR = f(math.NaN())
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantFloatNotNaN: 1},
		},
		{
			name: "an evidence value is NaN inside a slice element",
			plan: func() entryplan.Plan {
				p := ep1ShapedPlan()
				p.Evidence = append(p.Evidence, entryplan.Evidence{Key: "Z", Value: f(math.NaN())})
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantFloatNotNaN: 1},
		},
		{
			// TWO CODES, and the pair is the point. The finiteness sweep reaches RiskReward
			// because it reaches every float; RR_FINITE_POSITIVE fires as well because that
			// invariant is NAMED for the conjunction "finite and positive" — unlike the zone
			// bounds, where NaN deliberately trips only the NaN code because `NaN <= 0` is
			// false. Both sentences here are true: the bits cannot be encoded, AND the ratio
			// is not a positive number. See InvariantRiskRewardFinitePositive.
			//
			// What is STILL not asserted: that 20/10 equals the Ratio. That is arithmetic
			// consistency, not geometry, and invariants.go says why it is left out.
			name: "RiskReward.Ratio is NaN",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: 10, RewardPerShare: 20,
					Ratio: math.NaN()}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantFloatNotNaN:              1,
				entryplan.InvariantRiskRewardFinitePositive: 1,
			},
		},

		// ── FLOAT_NOT_INF
		{
			name: "a zone bound is +Inf",
			plan: pricedPlan(legalZone(840, math.Inf(1)), nil),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantFloatNotInf: 1},
		},
		{
			// The other EP-4 field, same line: -Inf is caught because it is a float, NOT
			// because anything here knows an invalidation belongs below the zone.
			name: "Invalidation.Price is -Inf",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Invalidation = &entryplan.Invalidation{Price: math.Inf(-1)}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantFloatNotInf: 1},
		},

		// ── MAX_CHASE_POSITIVE
		{
			name: "the chase limit is zero",
			plan: pricedPlan(nil, f(0)),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantMaxChasePositive: 1},
		},
		{
			name: "the chase limit is negative",
			plan: pricedPlan(nil, f(-10)),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantMaxChasePositive: 1},
		},

		// ── MAX_CHASE_NOT_BELOW_ZONE_HIGH
		{
			name: "the chase limit is one tick below the top of the zone",
			plan: pricedPlan(legalZone(840, 860), f(859.5)),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantMaxChaseCoversZone: 1},
		},
		{
			name: "the chase limit is below the whole zone",
			plan: pricedPlan(legalZone(840, 860), f(700)),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantMaxChaseCoversZone: 1},
		},
		{
			name: "a zero chase limit under a legal zone breaks both chase invariants",
			plan: pricedPlan(legalZone(840, 860), f(0)),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantMaxChasePositive:   1,
				entryplan.InvariantMaxChaseCoversZone: 1,
			},
		},

		// ── EP-4's four, each planted alone. Every one of these is a plan whose numbers
		// are all finite, all positive and all on a plausible scale: the failure is in the
		// RELATIONS, which is exactly the class of bug the finiteness sweep cannot see.

		// ── INVALIDATION_BELOW_ZONE
		{
			// The stop INSIDE the entry band. The plan would buy between 840 and 860 and
			// declare the idea dead at 850, which is two contradictory instructions about the
			// same ten dollars.
			name: "the invalidation sits inside the zone",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Invalidation = &entryplan.Invalidation{Price: 850,
					PriceBasis: entryplan.PriceBasisRaw}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantInvalidationBelowZone: 1},
		},
		{
			// THE BOUNDARY, and it is the opposite call from the chase ceiling's: equality is
			// NOT legal. A stop exactly at zone.Low forbids the price it is standing on, while
			// a ceiling exactly at zone.High still permits every price in the zone.
			name: "the invalidation is exactly at the bottom of the zone",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Invalidation = &entryplan.Invalidation{Price: 840,
					PriceBasis: entryplan.PriceBasisRaw}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantInvalidationBelowZone: 1},
		},
		{
			// One tick below is LEGAL, so the invariant is not merely "any stop is wrong".
			name: "the invalidation one tick below the zone is legal",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Invalidation = &entryplan.Invalidation{Price: 839.5,
					PriceBasis: entryplan.PriceBasisRaw}
				return p
			}(),
		},
		{
			// A stop with NO ZONE to be below is not a violation. It cannot arise from
			// ComputePlan (the stop step requires a published zone) but the checker must not
			// invent a rule for it either: with no entry there is nothing for "below" to mean.
			name: "an invalidation with no zone to compare against",
			plan: func() entryplan.Plan {
				p := pricedPlan(nil, nil)
				p.Invalidation = &entryplan.Invalidation{Price: 840}
				return p
			}(),
		},

		// ── TARGET1_ABOVE_ZONE
		{
			// A first target BELOW the worst acceptable entry: the reward is negative at the
			// entry a reader must assume, so the R:R is a loss rendered as a ratio.
			name: "the first target is below the top of the zone",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Target1 = &entryplan.PriceLevel{Price: 855,
					PriceBasis: entryplan.PriceBasisRaw}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantTarget1AboveZone: 1},
		},
		{
			// THE BOUNDARY: exactly AT zone.High is a zero reward, which is not a target.
			name: "the first target is exactly at the top of the zone",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Target1 = &entryplan.PriceLevel{Price: 860,
					PriceBasis: entryplan.PriceBasisRaw}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantTarget1AboveZone: 1},
		},

		// ── TARGET2_NOT_BELOW_TARGET1
		{
			// The pair a valuation ceiling can invert. Withdrawing rather than swapping is
			// enforce.go's decision; this is the check that notices.
			name: "the second target is below the first",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Target1 = &entryplan.PriceLevel{Price: 900}
				p.Target2 = &entryplan.PriceLevel{Price: 880}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantTarget2NotBelowTarget1: 1},
		},
		{
			// THE BOUNDARY: equality is LEGAL here, unlike the invalidation's. A valuation
			// ceiling landing exactly on the first target is a real answer, and refusing it
			// would force a producer to invent a gap it has no basis for.
			name: "the two targets are equal",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Target1 = &entryplan.PriceLevel{Price: 900}
				p.Target2 = &entryplan.PriceLevel{Price: 900}
				return p
			}(),
		},
		{
			// A SECOND TARGET WITH NO FIRST is not a violation of THIS invariant — there is
			// nothing to be below. It is also unreachable from ComputePlan, where Target2 is
			// computed after Target1 exists, and the checker says so by staying quiet rather
			// than inventing an ordering rule for a pair that is not a pair.
			name: "a second target with no first target",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Target2 = &entryplan.PriceLevel{Price: 900}
				return p
			}(),
		},

		// ── RR_FINITE_POSITIVE, one field at a time, because each is a different sentence.
		{
			name: "the risk per share is zero",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: 0, RewardPerShare: 20,
					Ratio: 2}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantRiskRewardFinitePositive: 1,
			},
		},
		{
			name: "the reward per share is negative",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: 10, RewardPerShare: -20,
					Ratio: 2}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantRiskRewardFinitePositive: 1,
			},
		},
		{
			// THE SPECIFIC NUMBER types.go warns about: a Ratio of 0 reads as "this trade
			// offers no reward", which is a conclusion. "We could not compute it" is a nil
			// RiskReward.
			name: "the ratio is zero",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: 10, RewardPerShare: 20,
					Ratio: 0}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantRiskRewardFinitePositive: 1,
			},
		},
		{
			// +Inf: TWO codes, both true. Unlike the NaN row above, this one would also be
			// caught by a naive `<= 0` check — it is here because the two non-finite shapes
			// have to be seen to behave the same way under an invariant named for finiteness.
			name: "the ratio is +Inf",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: 10, RewardPerShare: 20,
					Ratio: math.Inf(1)}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantFloatNotInf:              1,
				entryplan.InvariantRiskRewardFinitePositive: 1,
			},
		},
		{
			// All three fields bad at once: THREE violations of one code, not one. The count
			// is what says the check is per-field rather than a single early return.
			name: "every risk/reward field is non-positive",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: -1, RewardPerShare: 0,
					Ratio: -3}
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantRiskRewardFinitePositive: 3,
			},
		},
		{
			// THE WHOLE EP-4 SHAPE, LEGAL. A checker that cannot say "this risk side is fine"
			// is not a checker, and this row is what stops the four additions from being
			// satisfiable only by withdrawing everything.
			name: "a complete, consistent EP-4 plan",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Invalidation = &entryplan.Invalidation{Price: 830,
					PriceBasis: entryplan.PriceBasisRaw}
				p.Target1 = &entryplan.PriceLevel{Price: 920, PriceBasis: entryplan.PriceBasisRaw}
				p.Target2 = &entryplan.PriceLevel{Price: 965, PriceBasis: entryplan.PriceBasisRaw}
				p.RiskReward = &entryplan.RiskReward{RiskPerShare: 30, RewardPerShare: 60,
					Ratio: 2, Target: "TARGET_1"}
				return p
			}(),
		},

		// ── EP-5's seven STATUS invariants, each planted alone.
		//
		// The class of failure they catch is different from everything above: not an
		// impossible NUMBER but a VERDICT that contradicts the numbers published beside it.
		// Each of these is a sentence a reader would act on, and none of them can be true.

		// ── STATUS_CANONICAL
		{
			// THE ZERO PLAN, and it moved from the legal half of this table to the broken
			// half when EP-5 shipped. Before EP-5 a Plan without a status was merely a Plan
			// nothing had decided about yet; now every plan ComputePlan returns carries one
			// of the six, so "" is a string NO RULE CAN PRODUCE — which is not a cautious
			// value, it is a case every consumer's switch falls through.
			//
			// STATUS_HAS_REASON does NOT also fire here, and that is deliberate rather than
			// an oversight: it asks whether a status is DEFENDED, and "" is not a status. One
			// defect, one code.
			name: "the zero Plan has no status at all",
			plan: entryplan.Plan{},
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantStatusCanonical: 1},
		},
		{
			name: "a status that is not one of the six",
			plan: statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.EntryStatus("MAYBE_BUY"), entryplan.ReasonStatusPriceInsideZone),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantStatusCanonical: 1},
		},

		// ── STATUS_HAS_REASON
		{
			// A VERDICT WITH NO DEFENCE. The arm that produced it, and therefore what it
			// meant, cannot be reconstructed from the archived row — which is the whole
			// failure DecisionRule and the STATUS_ vocabulary exist to prevent.
			name: "a status with no reason code",
			plan: func() entryplan.Plan {
				p := pricedPlan(legalZone(840, 860), f(866))
				p.Reasons = nil
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantStatusHasReason: 1},
		},
		{
			// INSUFFICIENT_DATA is not exempt. "We could not conclude" still has to say why:
			// a missing band and a missing risk boundary are different sentences and a reader
			// acts differently on them.
			name: "even INSUFFICIENT_DATA has to say why",
			plan: func() entryplan.Plan {
				p := ep1ShapedPlan()
				p.Reasons = nil
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantStatusHasReason: 1},
		},

		// ── BUY_NOW_HAS_ZONE
		{
			// "Buy at the market now" with no band published is an instruction whose price
			// the reader has to invent — the exact failure this package exists to prevent.
			name: "BUY_NOW with no entry band",
			plan: statused(pricedPlan(nil, nil), entryplan.StatusBuyNow,
				entryplan.ReasonStatusPriceInsideZone),
			want: map[entryplan.PlanInvariant]int{entryplan.InvariantBuyNowHasZone: 1},
		},

		// ── BUY_NOW_PRICE_AUTHORISED. The DISJUNCTION: inside the band, OR at-or-below a
		// ceiling that exists. What it forbids is the third case.
		{
			name: "BUY_NOW at a price the plan never authorised",
			plan: decided(statused(pricedPlan(legalZone(840, 860), nil),
				entryplan.StatusBuyNow, entryplan.ReasonStatusImmediateChasePermitted),
				entryplan.EntrySemanticBreakout, 900),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantBuyNowPriceAuthorised: 1,
			},
		},
		{
			// LEGAL, first half of the disjunction: the quote is inside the band, bounds
			// inclusive, and it sits exactly on the top bound.
			name: "BUY_NOW at the top of its own band is authorised",
			plan: decided(statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.StatusBuyNow, entryplan.ReasonStatusPriceInsideZone),
				entryplan.EntrySemanticPullback, 860),
		},
		{
			// LEGAL, second half: above the band and AT the ceiling. This is the chase arm's
			// output, and a checker that rejected it would forbid the arm.
			name: "BUY_NOW above the band but at the ceiling is authorised",
			plan: decided(statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.StatusBuyNow, entryplan.ReasonStatusImmediateChasePermitted),
				entryplan.EntrySemanticBreakout, 866),
		},

		// ── BUY_NOW_THESIS_NOT_CONTRADICTED (EP-6D). Each fixture is otherwise the LEGAL
		// "BUY_NOW at the top of its own band" plan above, so exactly one thing is planted.
		{
			// The scanner said SELL / REDUCE / TAKE PROFIT / STOP LOSS, and the plan is BUY_NOW
			// with prices: BOTH EP-6D invariants fire, and both sentences are true.
			name: "BUY_NOW on a stock whose primary Action contradicts the thesis",
			plan: withThesis(decided(statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.StatusBuyNow, entryplan.ReasonStatusPriceInsideZone),
				entryplan.EntrySemanticPullback, 860), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantBuyNowThesisNotContradicted:        1,
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			// MISSING ≠ ZERO: nobody classified the Action, which is not permission.
			name: "BUY_NOW with an unclassified thesis standing",
			plan: withThesis(decided(statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.StatusBuyNow, entryplan.ReasonStatusPriceInsideZone),
				entryplan.EntrySemanticPullback, 860), ""),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantBuyNowThesisNotContradicted: 1,
			},
		},
		{
			// A value nobody declared is not NOT_CONTRADICTED either.
			name: "BUY_NOW with a thesis standing nobody declared",
			plan: withThesis(decided(statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.StatusBuyNow, entryplan.ReasonStatusPriceInsideZone),
				entryplan.EntrySemanticPullback, 860), entryplan.ThesisStanding("PROBABLY_FINE")),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantBuyNowThesisNotContradicted: 1,
			},
		},
		// ── THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY (EP-6D, widened by the user)
		//
		// The STATUS half, one entering/waiting status per fixture, each with NO price, so the
		// status check is the only thing that can fire (BUY_NOW additionally trips
		// BUY_NOW_THESIS_NOT_CONTRADICTED, and both sentences are true).
		{
			name: "a contradicted thesis on a priceless WAIT_PULLBACK",
			plan: withThesis(decided(statused(pricedPlan(nil, nil),
				entryplan.StatusWaitPullback, entryplan.ReasonStatusPriceAboveEntryZone),
				entryplan.EntrySemanticPullback, 900), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			// No ceiling and no zone is also a TOO_EXTENDED fault of its own, so those two
			// EP-5 codes are expected beside the EP-6D one; the count of the EP-6D one is what
			// this row is about.
			name: "a contradicted thesis on a priceless TOO_EXTENDED",
			plan: withThesis(decided(statused(pricedPlan(nil, nil),
				entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase),
				entryplan.EntrySemanticPullback, 900), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
				entryplan.InvariantTooExtendedKeepsZone:               1,
				entryplan.InvariantTooExtendedAboveCeiling:            1,
			},
		},
		{
			name: "a contradicted thesis on a priceless BUY_NOW",
			plan: withThesis(decided(statused(pricedPlan(nil, nil),
				entryplan.StatusBuyNow, entryplan.ReasonStatusPriceInsideZone),
				entryplan.EntrySemanticPullback, 850), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
				entryplan.InvariantBuyNowThesisNotContradicted:        1,
				entryplan.InvariantBuyNowHasZone:                      1,
			},
		},
		{
			// The TRACE half: an S1 plan, NO_VALID_ENTRY, no published price, and a zone number
			// left in EntryTrace.Zone.
			name: "an S1 plan whose zone trace still carries a price",
			plan: func() entryplan.Plan {
				p := s1Plan()
				p.EntryTrace.Zone.Low = f(97)
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			name: "an S1 plan whose chase trace still carries a price",
			plan: func() entryplan.Plan {
				p := s1Plan()
				p.EntryTrace.Chase.Final = f(105)
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			name: "an S1 plan whose stop trace still carries a price",
			plan: func() entryplan.Plan {
				p := s1Plan()
				p.EntryTrace.Stop.Price = f(95)
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			name: "an S1 plan whose target trace still carries a price",
			plan: func() entryplan.Plan {
				p := s1Plan()
				p.EntryTrace.Targets.Target1 = f(111)
				return p
			}(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			// LEGAL: exactly what S1 publishes.
			name: "an S1 plan with empty step traces is legal",
			plan: s1Plan(),
		},
		{
			// A contradicted thesis shown as "wait for the pullback, zone 840-860".
			name: "a contradicted thesis on a waiting status with prices",
			plan: withThesis(decided(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.EntrySemanticPullback, 900), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			// The right STATUS is not enough: NO_VALID_ENTRY beside a zone still tells a reader
			// where to buy.
			name: "a contradicted thesis at NO_VALID_ENTRY that still carries a zone",
			plan: withThesis(decided(statused(pricedPlan(legalZone(840, 860), nil),
				entryplan.StatusNoValidEntry, entryplan.ReasonStatusThesisContradicted),
				entryplan.EntrySemanticPullback, 850), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			// No prices is not enough either: a priceless WAIT_BREAKOUT is still a wait.
			name: "a contradicted thesis on a priceless waiting status",
			plan: withThesis(decided(statused(pricedPlan(nil, nil),
				entryplan.StatusWaitBreakout, entryplan.ReasonStatusPriceBelowEntryZone),
				entryplan.EntrySemanticBreakout, 800), entryplan.ThesisContradicted),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantThesisContradictedPublishesNoEntry: 1,
			},
		},
		{
			// LEGAL: what S1 publishes.
			name: "a contradicted thesis at NO_VALID_ENTRY with no price is legal",
			plan: withThesis(decided(statused(pricedPlan(nil, nil),
				entryplan.StatusNoValidEntry, entryplan.ReasonStatusThesisContradicted),
				entryplan.EntrySemanticPullback, 850), entryplan.ThesisContradicted),
		},
		{
			// LEGAL: no resolved shape (S0) — no thesis to contradict, no price to withhold.
			name: "a contradicted standing on a priceless INSUFFICIENT_DATA plan is legal",
			plan: withThesis(decided(statused(pricedPlan(nil, nil),
				entryplan.StatusInsufficientData, entryplan.ReasonStatusSemanticUnresolved),
				entryplan.EntrySemanticUnknown, 850), entryplan.ThesisContradicted),
		},

		// ── TOO_EXTENDED_ABOVE_CEILING
		{
			// No ceiling, so "past what this plan would pay" is a statement with no number
			// behind it.
			name: "TOO_EXTENDED with no chase ceiling",
			plan: decided(statused(pricedPlan(legalZone(840, 860), nil),
				entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase),
				entryplan.EntrySemanticPullback, 900),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantTooExtendedAboveCeiling: 1,
			},
		},
		{
			// THE BOUNDARY, and it is the same call the decision makes: EQUALITY IS NOT
			// EXTENSION. The highest price the plan permits paying is a price the plan
			// permits paying, so a TOO_EXTENDED published AT the ceiling is a contradiction.
			name: "TOO_EXTENDED at exactly the chase ceiling",
			plan: decided(statused(pricedPlan(legalZone(840, 860), f(872)),
				entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase),
				entryplan.EntrySemanticPullback, 872),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantTooExtendedAboveCeiling: 1,
			},
		},
		{
			// One tick over IS extension, so the invariant is not merely "TOO_EXTENDED is
			// always wrong".
			name: "TOO_EXTENDED one tick above the ceiling is legal",
			plan: decided(statused(pricedPlan(legalZone(840, 860), f(872)),
				entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase),
				entryplan.EntrySemanticPullback, 872.5),
		},

		// ── TOO_EXTENDED_KEEPS_ZONE
		{
			// "Too expensive here" is only actionable beside "and this is where it would not
			// be". SEPARATE from the ceiling check, and this row shows why: the ceiling is
			// present and correct, and the plan is still useless.
			name: "TOO_EXTENDED that lost its entry band",
			plan: decided(statused(pricedPlan(nil, f(872)),
				entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase),
				entryplan.EntrySemanticPullback, 900),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantTooExtendedKeepsZone: 1,
			},
		},

		// ── WAIT_MATCHES_SEMANTIC, both directions, ONE code — because it is one claim.
		{
			// Telling a reader to wait for a retracement that no thesis predicts, which is
			// also this package overriding the scanner's choice of entry SHAPE.
			name: "WAIT_PULLBACK on a breakout setup",
			plan: decided(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.EntrySemanticBreakout, 900),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantWaitMatchesSemantic: 1,
			},
		},
		{
			name: "WAIT_BREAKOUT on a pullback setup",
			plan: decided(statused(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.StatusWaitBreakout, entryplan.ReasonStatusPriceBelowEntryZone),
				entryplan.EntrySemanticPullback, 800),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantWaitMatchesSemantic: 1,
			},
		},
		{
			name: "WAIT_PULLBACK on a pullback setup is legal",
			plan: decided(pricedPlan(legalZone(840, 860), f(866)),
				entryplan.EntrySemanticPullback, 900),
		},
		{
			// NO SHAPE RECORDED, no claim. A plan whose trace never said which shape the
			// verdict was about cannot be checked against one, and inventing a default here
			// would fail every hand-built plan for a reason that has nothing to do with the
			// invariant.
			name: "a waiting status with no shape recorded is not checked against one",
			plan: pricedPlan(legalZone(840, 860), f(866)),
		},

		// ── everything at once
		{
			name: "the planted plan that breaks all seven",
			plan: plantedBrokenPlan(),
			want: map[entryplan.PlanInvariant]int{
				entryplan.InvariantFloatNotNaN:        3,
				entryplan.InvariantFloatNotInf:        2,
				entryplan.InvariantZoneLowPositive:    1,
				entryplan.InvariantZoneHighPositive:   1,
				entryplan.InvariantZoneOrdered:        1,
				entryplan.InvariantMaxChasePositive:   1,
				entryplan.InvariantMaxChaseCoversZone: 1,
			},
		},
	}
}

// Each planted Plan must produce EXACTLY the violations it was built to produce, and each
// legal Plan must produce none.
//
// The ANTI-VACUITY LEDGER at the bottom is what stops this table from decaying: if a future
// edit leaves an invariant untripped by every fixture, the test fails and names it. A check
// no fixture reaches is a check nobody has ever seen run — the FU-11 D8 failure (a guard
// covering 11 of 14 codes while claiming all 14) one layer down.
func TestThePlanInvariantCheckerCatchesEveryPlantedViolation(t *testing.T) {
	ledger := map[entryplan.PlanInvariant]int{}
	var legal, broken int

	for _, c := range invariantCases() {
		got := entryplan.PlanInvariantViolations(c.plan)
		counts := invariantCounts(got)
		for inv, n := range counts {
			ledger[inv] += n
		}
		if len(c.want) == 0 {
			legal++
			if len(got) != 0 {
				t.Errorf("%s: a LEGAL plan was reported as violating %v — a checker that "+
					"flags a well-formed plan gets switched off by the next person in a hurry",
					c.name, renderViolations(got))
			}
			continue
		}
		broken++
		if !reflect.DeepEqual(counts, c.want) {
			t.Errorf("%s: checker reported %v, want exactly %v\nviolations:\n  %s",
				c.name, counts, c.want, strings.Join(renderViolations(got), "\n  "))
		}
		// Every message must name the FIELD, the VALUE and the INVARIANT, so a reader does
		// not have to open this package to understand a failure.
		for _, v := range got {
			if !entryplan.KnownPlanInvariant(v.Invariant) {
				t.Errorf("%s: violation carries %q, which is not in AllPlanInvariants",
					c.name, v.Invariant)
			}
			if !strings.HasPrefix(v.Path, "Plan") {
				t.Errorf("%s: violation path %q does not locate the field inside the Plan",
					c.name, v.Path)
			}
			if len(v.Detail) < 40 {
				t.Errorf("%s: violation detail %q is a label, not an explanation — this "+
					"repo's failures have to be readable without the source", c.name, v.Detail)
			}
			line := v.String()
			if !strings.Contains(line, v.Path) || !strings.Contains(line, string(v.Invariant)) {
				t.Errorf("%s: String() = %q, which drops the path or the invariant code",
					c.name, line)
			}
		}
	}

	// ANTI-VACUITY. Every invariant in the registry must have been tripped by something
	// above, and both halves of the table must be populated.
	for _, inv := range entryplan.AllPlanInvariants {
		if ledger[inv] == 0 {
			t.Errorf("no planted plan trips %q — the check is in the registry and no test has "+
				"ever seen it fire, so it could be deleted with this suite still green. "+
				"Ledger: %v", inv, ledger)
		}
	}
	if legal == 0 || broken == 0 {
		t.Fatalf("the table has %d legal and %d broken plans — it must exercise both "+
			"directions, or it is either a checker with nothing to catch or a checker that "+
			"catches everything", legal, broken)
	}
}

// ── 2. the checker's own census ───────────────────────────────────────────────────────

// THE MOST IMPORTANT TEST IN THIS FILE.
//
// It asserts what the checker INSPECTED, not only what it concluded, in the pattern
// TestNoNonFiniteFloatEscapes uses for its walker (`seen != 7 || bad != 5`). Without it,
// "no violations" and "never looked" are the same output — and the second one is what a
// checker degrades into when a later work item adds a field the walk cannot reach.
//
// If this fails after a Plan field was added, the question to answer is NOT "what number do
// I put here": it is whether the new field is reachable by walkPlanValue and whether it needs
// an invariant of its own.
func TestTheCheckerReportsWhatItInspected(t *testing.T) {
	a := entryplan.AuditPlanInvariants(plantedBrokenPlan())

	if len(a.FloatsSeen) != plantedFloats {
		var paths []string
		for _, fl := range a.FloatsSeen {
			paths = append(paths, fmt.Sprintf("%s=%v", fl.Path, fl.Value))
		}
		t.Errorf("the checker saw %d floats in a fixture built to contain %d — it is not "+
			"reaching through a pointer, a slice or a nested struct, and every finiteness "+
			"guarantee in this package is narrower than it claims.\nsaw: %v",
			len(a.FloatsSeen), plantedFloats, paths)
	}
	if len(a.ZonesSeen) != plantedZones {
		t.Errorf("the checker found %d PriceZones in a fixture containing %d — zone geometry "+
			"is checked per zone found, so a zone the walk misses is a zone nothing checks",
			len(a.ZonesSeen), plantedZones)
	}
	if len(a.Violations) != plantedViolations {
		t.Errorf("the checker reported %d violations in a fixture built to break %d — it "+
			"either stopped checking or acquired a check nobody declared.\nreported:\n  %s",
			len(a.Violations), plantedViolations,
			strings.Join(renderViolations(a.Violations), "\n  "))
	}

	// The nil evidence Value must have been STEPPED OVER, not counted as a zero: a walk that
	// dereferenced it would have panicked, and one that counted it would report a float that
	// does not exist.
	for _, fl := range a.FloatsSeen {
		if strings.Contains(fl.Path, "Evidence[1]") {
			t.Errorf("the walk counted %s = %v — Evidence[1].Value is nil, and a nil pointer "+
				"is an ABSENT number, not 0", fl.Path, fl.Value)
		}
	}

	// And it must have looked in all four shapes the Plan type can hide a float in.
	for _, want := range []string{
		"Plan.IdealEntry*.Low",          // a plain float64 behind a pointer to a struct
		"Plan.IdealEntry*.WidthATR*",    // a pointer INSIDE a pointer
		"Plan.MaxChasePrice*",           // a bare *float64 on the Plan
		"Plan.Evidence[0].Value*",       // a pointer inside a slice element
		"Plan.RiskReward*.RiskPerShare", // a struct EP-1 never built
	} {
		var found bool
		for _, fl := range a.FloatsSeen {
			if fl.Path == want {
				found = true
				break
			}
		}
		if !found {
			var paths []string
			for _, fl := range a.FloatsSeen {
				paths = append(paths, fl.Path)
			}
			t.Errorf("the walk never visited %s — the path notation changed, or that shape of "+
				"field is invisible to the checker.\nvisited: %v", want, paths)
		}
	}
}

// The audit is a pure function of the Plan: same input, same output, and the caller's numbers
// are not touched. A checker that mutated the thing it inspects would be worse than none.
//
// Compared through String() rather than reflect.DeepEqual because DeepEqual reports NaN != NaN,
// which would make the fixture that matters look non-deterministic.
func TestTheAuditIsDeterministicAndLeavesThePlanAlone(t *testing.T) {
	p := plantedBrokenPlan()
	first := renderViolations(entryplan.PlanInvariantViolations(p))
	second := renderViolations(entryplan.PlanInvariantViolations(p))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("the checker is not deterministic:\n%v\n%v", first, second)
	}
	if p.IdealEntry.Low != 0 || p.IdealEntry.High != -5 || *p.MaxChasePrice != -6 {
		t.Fatalf("the checker rewrote the plan it was given: zone %v-%v, chase %v",
			p.IdealEntry.Low, p.IdealEntry.High, *p.MaxChasePrice)
	}
	// A CLEAN plan must produce a nil slice, not an empty one, so `== nil` and `len() == 0`
	// agree and a caller cannot get the answer wrong either way.
	//
	// The fixture is ep1ShapedPlan() and NOT the zero Plan, and the change is EP-5's: a Plan
	// with Status "" is no longer clean, because "" is a string no rule can produce and
	// STATUS_CANONICAL says so. ep1ShapedPlan is the smallest legal plan there is — a status,
	// the reason defending it, a census, and not one price.
	if v := entryplan.PlanInvariantViolations(ep1ShapedPlan()); v != nil {
		t.Errorf("a clean plan produced %v, want nil — `== nil` and `len() == 0` must agree "+
			"so a caller cannot get the answer wrong either way", v)
	}
}

// ── 3. the checker agrees with what production actually emits ─────────────────────────

// The one half of this that CAN be asserted against production: everything ComputePlan
// returns, over EVERY input this package can be handed, is invariant-clean.
//
// UNDER EP-2 THIS WAS VACUOUS ON THE PRICE INVARIANTS, by construction — no price could be
// emitted, so ZONE_LOW_POSITIVE, ZONE_LOW_NOT_ABOVE_HIGH and both chase invariants were
// checked against a population of nil pointers. EP-3b is what makes it real, and the sweep is
// over allSnapshots() rather than matrix() for exactly that reason: matrix() carries no level
// evidence, so on its own it STILL cannot produce a zone, and running only it would have kept
// the vacuity while looking like a stronger test.
//
// The zone ledger at the bottom is what says so out loud: the sweep must have seen real zones
// and real chase limits, or the price invariants were not exercised.
func TestEveryPlanComputePlanReturnsIsInvariantClean(t *testing.T) {
	var checked, withFloats, floats, zones, chases int
	// EP-4's ledger, one counter per field it publishes. The four invariants it added are
	// only tested if the sweep SAW a stop, a first target, a second target and a risk/reward
	// — and Target2 is counted separately from Target1 precisely because it is the optional
	// one, so a rule change that silently stopped publishing it would show up here.
	var stops, target1s, target2s, rrs int
	for _, c := range allSnapshots() {
		p := entryplan.ComputePlan(c.in)
		a := entryplan.AuditPlanInvariants(p)
		if len(a.Violations) != 0 {
			t.Errorf("%s: ComputePlan returned a plan that breaks its own invariants:\n  %s",
				c.name, strings.Join(renderViolations(a.Violations), "\n  "))
		}
		checked++
		floats += len(a.FloatsSeen)
		if len(a.FloatsSeen) > 0 {
			withFloats++
		}
		if p.IdealEntry != nil {
			zones++
		}
		if p.MaxChasePrice != nil {
			chases++
		}
		if p.Invalidation != nil {
			stops++
		}
		if p.Target1 != nil {
			target1s++
		}
		if p.Target2 != nil {
			target2s++
		}
		if p.RiskReward != nil {
			rrs++
		}
	}
	if stops == 0 || target1s == 0 || target2s == 0 || rrs == 0 {
		t.Errorf("the sweep produced %d invalidations, %d first targets, %d second targets "+
			"and %d risk/rewards — EP-4's four invariants were checked against nothing but "+
			"nil pointers, which is exactly the vacuity EP-4 was supposed to end",
			stops, target1s, target2s, rrs)
	}
	if checked < 10 {
		t.Fatalf("only %d plans checked — the matrix is not being iterated", checked)
	}

	// NON-VACUITY, and it has to be stated as an aggregate rather than per plan: a malformed
	// snapshot legitimately produces a plan with NO number in it at all (SNAPSHOT_MALFORMED
	// alone, no census), and so does a snapshot whose only numeric evidence was unavailable —
	// an absent value is a nil pointer, which the walk steps over. What must NOT be true is
	// that the sweep found nothing ANYWHERE: the census carries CURRENT_PRICE and ATR as
	// numbers, and a sweep that reached neither would report every plan clean for free.
	if withFloats == 0 || floats == 0 {
		t.Errorf("the checker found no float in any of %d real plans (%d with numbers, %d "+
			"floats total) — the evidence census carries CURRENT_PRICE and ATR as numbers, so "+
			"the sweep is not reaching them and \"clean\" means nothing here", checked,
			withFloats, floats)
	}
	// THE ZONE LEDGER. The price invariants — both bounds positive, ordered, and the chase
	// limit not below the zone high — are only tested if the sweep saw a zone and a chase
	// limit. Under EP-2 both counts were zero and this test said nothing about four of the
	// seven invariants.
	if zones == 0 || chases == 0 {
		t.Errorf("the sweep produced %d zones and %d chase limits — the four PRICE invariants "+
			"were checked against nothing but nil pointers, which is exactly the vacuity "+
			"EP-3b was supposed to end", zones, chases)
	}
	if testing.Verbose() {
		t.Logf("%d plans, %d zones, %d chase limits, %d stops, %d/%d targets, %d R:Rs, "+
			"%d floats", checked, zones, chases, stops, target1s, target2s, rrs, floats)
	}
}

// ── 4. the two float walkers must agree ───────────────────────────────────────────────

// invariants.go's walkPlanValue is a SECOND reflective float walk, next to plan_test.go's
// walkFloats. That duplication is deliberate: walkFloats is the independent witness for "no
// non-finite value escapes ComputePlan", and if it were rewritten to call the production
// checker, one bug in the production walk would blind both the guard and the test that is
// supposed to notice.
//
// What makes the duplication safe is this test. Two independently written walks must produce
// the same paths and the same values on the same fixture, so a change to either one that
// stops it descending shows up here rather than in a report six months later.
func TestTheTwoFloatWalkersAgree(t *testing.T) {
	p := plantedBrokenPlan()

	fromTest := map[string]string{}
	walkFloats("Plan", reflect.ValueOf(p), func(path string, got float64) {
		fromTest[path] = fmt.Sprintf("%v", got)
	})
	fromProduction := map[string]string{}
	for _, fl := range entryplan.AuditPlanInvariants(p).FloatsSeen {
		fromProduction[fl.Path] = fmt.Sprintf("%v", fl.Value)
	}

	if len(fromTest) == 0 || len(fromProduction) == 0 {
		t.Fatalf("one of the walks found nothing: test walk %d, production walk %d",
			len(fromTest), len(fromProduction))
	}
	if !reflect.DeepEqual(fromTest, fromProduction) {
		t.Errorf("the two float walks disagree on the same plan.\ntest walk:       %v\n"+
			"production walk: %v\nOne of them has stopped descending, and the two guards "+
			"built on them no longer cover the same fields", sortedPairs(fromTest),
			sortedPairs(fromProduction))
	}
}

func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// ── 5. the Plan type graph must stay walkable ─────────────────────────────────────────

// walkPlanValue makes three assumptions about the Plan type that the compiler does not
// enforce, and this test is where each of them is held:
//
//  1. ACYCLIC. The walk has no cycle detection, so a field that pointed back to a type
//     already on the path would recurse until the stack died — inside a report render.
//  2. EXPORTED. The zone collector needs reflect.Value.CanInterface, which is false for
//     anything reached through an unexported field. Such a zone would be SILENTLY skipped.
//  3. NO FLOAT IN DISGUISE. complex128 carries two floats the walk does not check, and a
//     chan or func field is a shape a pure, encodable Plan has no business holding.
//
// Each failure below is a real gap in the checker, not a style complaint, and the fix is to
// the new field or to walkPlanValue — never to this test.
func TestThePlanTypeGraphIsWalkable(t *testing.T) {
	var floatLeaves, types int

	var visit func(ty reflect.Type, path string, stack []reflect.Type)
	visit = func(ty reflect.Type, path string, stack []reflect.Type) {
		for _, on := range stack {
			if on == ty {
				t.Errorf("%s is a %s and that type is already on the path — the Plan type "+
					"graph is CYCLIC, and walkPlanValue has no cycle detection: it would "+
					"recurse until the stack was exhausted", path, ty)
				return
			}
		}
		types++
		stack = append(stack, ty)

		switch ty.Kind() {
		case reflect.Float32, reflect.Float64:
			floatLeaves++
		case reflect.Complex64, reflect.Complex128:
			t.Errorf("%s is a %s — it carries two floats that walkPlanValue does not check, "+
				"so a non-finite one would reach JSON unnoticed", path, ty.Kind())
		case reflect.Chan, reflect.Func, reflect.UnsafePointer:
			t.Errorf("%s is a %s — a Plan is an encodable, comparable value and this field "+
				"is neither walkable nor persistable", path, ty.Kind())
		case reflect.Pointer, reflect.Slice, reflect.Array:
			visit(ty.Elem(), path+"*", stack)
		case reflect.Map:
			visit(ty.Key(), path+"{key}", stack)
			visit(ty.Elem(), path+"{val}", stack)
		case reflect.Struct:
			for n := 0; n < ty.NumField(); n++ {
				fld := ty.Field(n)
				if fld.PkgPath != "" {
					t.Errorf("%s.%s is UNEXPORTED — walkPlanValue cannot call Interface() on "+
						"a value reached through it, so a PriceZone behind it would be "+
						"skipped by the zone checks in silence", path, fld.Name)
					continue
				}
				visit(fld.Type, path+"."+fld.Name, stack)
			}
		}
	}
	visit(reflect.TypeOf(entryplan.Plan{}), "Plan", nil)

	// Non-vacuity: the walk must actually have descended.
	if floatLeaves < 10 {
		t.Errorf("only %d float fields are reachable from Plan across %d types — either the "+
			"execution prices were removed from the type or this walk is not descending",
			floatLeaves, types)
	}
}

// ── 6. the registry itself ────────────────────────────────────────────────────────────

// Same three checks AllReasons and AllPolicyNames get — uniqueness, non-empty, stable
// SCREAMING_SNAKE — because a violation code that gets logged or archived is a wire format
// too. doc.go's J-3 records that Requirement was shipped WITHOUT these; this list is not
// going to repeat it.
func TestThePlanInvariantRegistryIsWellFormed(t *testing.T) {
	seen := map[entryplan.PlanInvariant]bool{}
	for _, inv := range entryplan.AllPlanInvariants {
		if inv == "" {
			t.Error("empty invariant code in AllPlanInvariants")
			continue
		}
		if seen[inv] {
			t.Errorf("%q listed twice", inv)
		}
		seen[inv] = true
		for _, ch := range inv {
			if (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' {
				t.Errorf("%q is not a stable SCREAMING_SNAKE code", inv)
				break
			}
		}
		if !entryplan.KnownPlanInvariant(inv) {
			t.Errorf("KnownPlanInvariant rejects %q, which is in the registry", inv)
		}
	}
	// SEVEN from EP-3a, FOUR from EP-4, SEVEN from EP-5. The number is pinned so that adding
	// one is a decision somebody makes on purpose rather than a side effect of a diff — and
	// each time it moved it moved in the same change that added the checks, their planted
	// fixtures and their golden literals.
	//
	// EP-5's seven are the first that are about the VERDICT rather than about a number, and
	// they are NECESSARY CONDITIONS rather than a second copy of DecideStatus: a checker that
	// re-ran the decision table would agree with it by construction on the day it was written
	// and would be the thing that silently drifts afterwards.
	//
	// EP-6D adds TWO: BUY_NOW_THESIS_NOT_CONTRADICTED (an unclassified or contradicted standing
	// is never BUY_NOW) and THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY (a contradicted standing shows
	// no price and no entering or waiting status). Both necessary conditions, not re-derivations.
	if len(entryplan.AllPlanInvariants) != 20 {
		t.Errorf("the registry holds %d invariants, want the 7 EP-3a defines plus EP-4's 4 "+
			"plus EP-5's 7 plus EP-6D's 2 (%v) — if an invariant was added, this count, its comment, its "+
			"planted fixture and the golden literal list move together",
			len(entryplan.AllPlanInvariants), entryplan.AllPlanInvariants)
	}
	if entryplan.KnownPlanInvariant("NOT_AN_INVARIANT") {
		t.Error("KnownPlanInvariant accepts a code that is not in the registry")
	}
}

// THE REPLACEMENT FOR TestTheStopAndTargetInvariantsAreDeliberatelyNotImplementedYet.
//
// Until EP-4 that test asserted the ABSENCE of these checks, so the absence could not be
// mistaken for an oversight: a plan whose invalidation sat 200 dollars ABOVE its entry zone
// produced no violation at all, because EP-3a had never seen a producer of a stop level and a
// checker written against an imagined producer is a guess with a test around it.
//
// EP-4 is that producer, so the same absurd plan is used here in the opposite direction: every
// one of the three faults must now be reported, by its own code, on its own field. And the
// finiteness half of the old test is kept, because it is a DIFFERENT claim — the sweep reaches
// those floats whether or not any rule understands the fields.
func TestTheStopAndTargetInvariantsAreNowImplementedAndProductionClean(t *testing.T) {
	p := pricedPlan(legalZone(840, 860), f(866))
	// An invalidation 200 dollars ABOVE the entry zone, a target BELOW it, and a negative
	// risk. All finite, all absurd — the exact fixture the old test asserted was IGNORED.
	p.Invalidation = &entryplan.Invalidation{Price: 1060, PriceBasis: entryplan.PriceBasisRaw}
	p.Target1 = &entryplan.PriceLevel{Price: 700, PriceBasis: entryplan.PriceBasisRaw}
	p.RiskReward = &entryplan.RiskReward{RiskPerShare: -5, RewardPerShare: 3, Ratio: 99}

	got := invariantCounts(entryplan.PlanInvariantViolations(p))
	want := map[entryplan.PlanInvariant]int{
		entryplan.InvariantInvalidationBelowZone:    1,
		entryplan.InvariantTarget1AboveZone:         1,
		entryplan.InvariantRiskRewardFinitePositive: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checker reported %v, want exactly %v — this is the fixture EP-3a "+
			"deliberately ignored, so every code here is one EP-4 was supposed to add",
			got, want)
	}

	// The finiteness sweep still reaches the same numbers, and that is a SEPARATE claim: it
	// holds for fields no rule understands, which is why it is not folded into the four
	// geometry checks.
	a := entryplan.AuditPlanInvariants(p)
	var sawStop, sawRisk bool
	for _, fl := range a.FloatsSeen {
		switch fl.Path {
		case "Plan.Invalidation*.Price":
			sawStop = true
		case "Plan.RiskReward*.RiskPerShare":
			sawRisk = true
		}
	}
	if !sawStop || !sawRisk {
		t.Errorf("the finiteness sweep did not reach the invalidation (%v) or the risk (%v) — "+
			"then a NaN stop would escape too", sawStop, sawRisk)
	}
}

// ── EP-5: what the status invariants do when there is no quote ────────────────────────

// THE THREE PRICE-BEARING STATUS INVARIANTS SKIP, THEY DO NOT FIRE.
//
// Plan carries no current price — a plan is a statement about levels, not a quote — so
// BUY_NOW_PRICE_AUTHORISED, TOO_EXTENDED_ABOVE_CEILING's comparison and WAIT_MATCHES_SEMANTIC
// read EntryTrace.Decision instead. A plan without one records no quote and no shape, and the
// checker must stay quiet rather than fail every hand-built Plan for a reason that has nothing
// to do with the invariant.
//
// This is written as its own test because the SKIP is the risky half: a skip is indistinguishable
// from a check that never ran, so the same fixtures are asserted TWICE — quiet without the
// trace, and loud with it.
func TestTheStatusInvariantsSkipRatherThanFireWithoutAQuote(t *testing.T) {
	cases := []struct {
		name string
		// plan is illegal ONLY when a quote is present.
		plan entryplan.Plan
		// withTrace supplies the quote that makes it illegal.
		semantic entryplan.EntrySemantic
		price    float64
		want     entryplan.PlanInvariant
	}{
		{
			name: "BUY_NOW outside its own band",
			plan: statused(pricedPlan(legalZone(840, 860), nil), entryplan.StatusBuyNow,
				entryplan.ReasonStatusPriceInsideZone),
			semantic: entryplan.EntrySemanticPullback, price: 900,
			want: entryplan.InvariantBuyNowPriceAuthorised,
		},
		{
			name: "TOO_EXTENDED at its own ceiling",
			plan: statused(pricedPlan(legalZone(840, 860), f(872)),
				entryplan.StatusTooExtended, entryplan.ReasonStatusAboveMaxChase),
			semantic: entryplan.EntrySemanticPullback, price: 870,
			want: entryplan.InvariantTooExtendedAboveCeiling,
		},
		{
			name:     "WAIT_PULLBACK on a breakout",
			plan:     pricedPlan(legalZone(840, 860), f(866)),
			semantic: entryplan.EntrySemanticBreakout, price: 900,
			want: entryplan.InvariantWaitMatchesSemantic,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// QUIET without the trace.
			if v := entryplan.PlanInvariantViolations(c.plan); len(v) != 0 {
				t.Errorf("with no decision trace the checker reported %v — it is asserting a "+
					"relation against a quote that was never recorded",
					renderViolations(v))
			}
			// LOUD with it, and this is what stops the skip above from being vacuous.
			loud := entryplan.PlanInvariantViolations(decided(c.plan, c.semantic, c.price))
			counts := invariantCounts(loud)
			if counts[c.want] != 1 {
				t.Errorf("with the quote recorded the checker reported %v, want exactly one "+
					"%s — if this is empty, the check above is quiet because it never runs at "+
					"all", counts, c.want)
			}
		})
	}
}
