package entryplan

import "github.com/deep-huang/stock-scanner/internal/pricerule"

// ── EP-4: what the trade is worth if it works ──────────────────────────────────────────
//
// Three numbers, in one place because they are one piece of arithmetic:
//
//	risk     = IdealEntry.High − Invalidation.Price
//	Target1  = min(IdealEntry.High + 2.0 × risk, nearest resistance above the entry)
//	Target2  = min(IdealEntry.High + 3.5 × risk, valuation base target)     (ceiling optional)
//	R:R      = (Target1 − IdealEntry.High) / risk
//
// # THE RISK IS MEASURED FROM zone.High, NOT FROM THE MIDPOINT
//
// zone.High is the WORST price this plan calls an acceptable entry, so it is the entry a reader
// must assume they got: a zone is a band precisely because the fill is not knowable in advance,
// and the honest reading of "I will buy between 92 and 96" is "I paid 96".
//
// The midpoint would be indefensible in a specific direction. It shrinks the risk denominator
// and lengthens the reward numerator at the same time, so it inflates R:R TWICE — and R:R is
// the single number a reader uses to decide whether to take the trade at all. On a number that
// goes onto an order ticket, systematic optimism is the worst possible bias, because it is
// invisible: every individual plan looks reasonable and the population is wrong. This is the
// same argument ComputeMaxChase makes for measuring the chase ceiling from zone.High, and the
// same one ComputeInvalidation makes for rounding DOWN.
//
// # STRUCTURAL RESISTANCE IS A CEILING ON TARGET 1, NOT A FLOOR — the opposite of the legacy
//
// internal/scanner/scorer.go:747-750 does this:
//
//	if bbUpper > t1 { t1 = bbUpper }      // the target is pushed FURTHER AWAY
//
// which is backwards. Resistance is where the move is likely to STOP, so the first level price
// has to get through is the first place a target can realistically be filled. Taking the max
// produces a target BEYOND a level the stock has not yet cleared, and the R:R computed from it
// is a reward the plan has no evidence for. So EP-4 takes the min: the nearer of "twice the
// risk" and "the next thing in the way".
//
// # Target 2 is measured from the ORIGINAL risk, never from Target 1
//
// If Target1 was clamped to resistance, Target2 = Target1 + something would inherit the clamp
// and compound it, and the R-multiple stamped on the row would stop being true. 3.5R is 3.5R
// from the entry, whatever happened to Target1.

// The two R multiples, named so they are reviewable without reading the arithmetic.
//
// HEURISTIC — NOT BACKTEST-FITTED, the same disclosure RuleVersion, the zone width and the
// chase multipliers carry. Neither is claimed to be optimal and there is no outcome study
// behind either number in this repo. What IS deliberate:
//
//   - target1RMultiple is 2.0 because a first target below 2R makes the plan's own R:R worse
//     than 2:1 before any resistance is considered, and this package publishes R:R as a
//     decision input. It is the CEILING on Target1, so structure can only bring it nearer.
//   - target2RMultiple is 3.5 and is deliberately NOT a whole multiple of the first: a second
//     target at exactly 2 × Target1 reads as a rule ("double it") rather than as a level, and
//     the two would move together under any edit to the first.
const (
	target1RMultiple = 2.0
	target2RMultiple = 3.5
)

// riskRewardTarget1 is the value RiskReward.Target carries: the R:R this package publishes is
// measured to TARGET 1 and never to Target 2.
//
// PERSISTED, and pinned as a literal by golden_test.go. Target1 is the one a plan is judged on
// because it is the one a trade is expected to reach; an R:R quoted to a 3.5R second target
// would be an unfalsifiable number attached to a plan most positions never hold for.
const riskRewardTarget1 = "TARGET_1"

// Target1Binding says which term of the min() decided Target1. A stable code on the trace, so a
// reader can tell a volatility-scaled target from a structural one without recomputing.
type Target1Binding string

const (
	// Target1FromRMultiple — 2R was the nearer term: there is no usable resistance above the
	// entry, or the resistance sits further away than 2R.
	Target1FromRMultiple Target1Binding = "R_MULTIPLE"
	// Target1FromResistance — the nearest resistance above the entry was nearer than 2R and
	// clamped the target down to it.
	Target1FromResistance Target1Binding = "RESISTANCE"
)

// AllTarget1Bindings is the registry, checked from both ends by the target tests.
var AllTarget1Bindings = []Target1Binding{Target1FromRMultiple, Target1FromResistance}

// ValuationCeilingOutcome is what the valuation sanity bound did to Target2.
//
// FIVE outcomes and not a boolean, because "there was no valuation", "P/E does not apply to
// this company", "the target is on another price series" and "the target was above ours anyway"
// are four different reasons Target2 is unclamped, and only the last one says anything about
// the stock.
type ValuationCeilingOutcome string

const (
	// ValuationCeilingUnavailable — no usable base-scenario target price was handed to us.
	// Target2 is published UNCLAMPED; see ComputeTargets on why this is not a dependency.
	ValuationCeilingUnavailable ValuationCeilingOutcome = "VALUATION_UNAVAILABLE"
	// ValuationCeilingModelUnsuitable — the valuation layer said P/E is not a meaningful
	// instrument for this company (UNSUITABLE), or the verdict was not one this package can
	// read. The number is NOT used to clamp anything; see ValuationSuitability.PermitsCeiling.
	ValuationCeilingModelUnsuitable ValuationCeilingOutcome = "MODEL_UNSUITABLE"
	// ValuationCeilingBasisMismatch — the target price is on a different price series from
	// the plan. Never converted; see PriceBasis.
	ValuationCeilingBasisMismatch ValuationCeilingOutcome = "PRICE_BASIS_MISMATCH"
	// ValuationCeilingNotBinding — the ceiling applied and sat ABOVE the 3.5R target, so it
	// changed nothing. A real answer, and the one that says the plan and the model agree.
	ValuationCeilingNotBinding ValuationCeilingOutcome = "NOT_BINDING"
	// ValuationCeilingApplied — the ceiling was below the 3.5R target and lowered it.
	ValuationCeilingApplied ValuationCeilingOutcome = "APPLIED"
)

// AllValuationCeilingOutcomes is the registry, checked from both ends by the target tests.
var AllValuationCeilingOutcomes = []ValuationCeilingOutcome{
	ValuationCeilingUnavailable,
	ValuationCeilingModelUnsuitable,
	ValuationCeilingBasisMismatch,
	ValuationCeilingNotBinding,
	ValuationCeilingApplied,
}

// basisValuationCeiling marks a Target2 that a valuation ceiling actually lowered. Persisted on
// PriceLevel.Basis, so the number carries the fact that it is not this package's own arithmetic.
const basisValuationCeiling = "VALUATION_CEILING_APPLIED"

// basisTarget1RMultipleOnly marks a Target1 that rests on the R multiple ALONE — no resistance
// was usable above the entry.
//
// Persisted, because the two Target1s are read the same way otherwise and they are not equally
// well evidenced: one is "the next level in the way", the other is "twice the distance to the
// invalidation". See ComputeTargets on why the second is still published.
const basisTarget1RMultipleOnly = "TARGET_1_R_MULTIPLE_ONLY"

// basisTarget1Resistance / basisTarget2RMultiple name the other two constructions.
const (
	basisTarget1Resistance = "TARGET_1_RESISTANCE_CLAMPED"
	basisTarget2RMultiple  = "TARGET_2_R_MULTIPLE"
)

// RiskRejection is why no risk denominator could be established. Persisted on the trace.
//
// Separate from Reason for the reason WidthRejection is: these are the SUB-REASONS of one
// plan-level reason, and they are the part a reader needs in order to know what to fix.
type RiskRejection string

const (
	// RiskRejectNotPositive — IdealEntry.High − Invalidation.Price is zero or negative, so
	// there is no risk to measure a reward against. UNREACHABLE from ComputePlan: the stop
	// step requires the invalidation to be strictly below zone.Low <= zone.High. The guard is
	// written because ComputeTargets is exported and takes the two as arguments.
	RiskRejectNotPositive RiskRejection = "RISK_NOT_POSITIVE"
	// RiskRejectBasisMismatch — the invalidation is on a different price series from the
	// zone, so the subtraction would be arithmetic across two price universes. Also
	// unreachable from ComputePlan, which builds the invalidation on the zone's own basis.
	RiskRejectBasisMismatch RiskRejection = "INVALIDATION_PRICE_BASIS_MISMATCH"
)

// AllRiskRejections is the registry, checked from both ends by the target tests.
var AllRiskRejections = []RiskRejection{RiskRejectNotPositive, RiskRejectBasisMismatch}

// ResistanceCandidate is the level a first target may be clamped to, and what happened to it.
//
// ONE candidate under EP4-v1 and it is the pivot high; see resistanceCandidate for why the
// intraday pivots and the Bollinger band are not in this list.
type ResistanceCandidate struct {
	Source LevelSource  `json:"source"`
	Status Availability `json:"status"`
	// Value is the observed level, COPIED out of the snapshot. Nil unless Status is AVAILABLE.
	Value      *float64   `json:"value,omitempty"`
	PriceBasis PriceBasis `json:"price_basis,omitempty"`
	// Disposition is what happened to it. Never empty.
	Disposition RiskLevelDisposition `json:"disposition"`
}

// value reports the candidate's number, if it has one.
func (c ResistanceCandidate) value() (float64, bool) {
	if c.Status.OK() && c.Value != nil && finitePositive(*c.Value) {
		return *c.Value, true
	}
	return 0, false
}

// resistanceCandidate collects and screens the ONE structural resistance EP4-v1 accepts.
//
// # It is the pivot high, and the pivot high is ALREADY the 60-day high
//
// Measured in internal/scanner/consolidation.go rather than assumed. Both branches put a
// 60-session high in the field, and they differ only in whether a base exists:
//
//	days >= 3:  prevHigh, _ := windowHighLow(candles, n-1-min(60,n-1), n-1); c.PivotHigh = prevHigh
//	days <  3:  c.PivotHigh, _ = windowHighLowF(candles, n-1-min(60,n-1), n-1)
//
// So "the pivot high above the entry" and "the 60-day high" are ONE candidate here, not two.
// Listing them separately would count one observation twice — and a reader of the trace would
// see two independent resistance levels agreeing, which is the most persuasive shape a piece of
// double-counted evidence can take.
//
// # What is deliberately NOT a candidate
//
//   - internal/technical/pivot.go's R1/R2. Its own doc says the levels are derived from the
//     PREVIOUS COMPLETE SESSION's OHLC and are "the ones that were knowable this morning" —
//     classic floor-trader pivots, an INTRADAY construction. entryplan plans entries on a 1–4
//     week horizon (see the package doc), so an R2 is typically a fraction of one ATR away and
//     would clamp nearly every Target1 to a day-trade level while still being printed as a
//     swing target. Point-in-time safety is not timescale compatibility, and only the first of
//     the two is a property of pivot.go.
//   - The Bollinger upper band, which is what the legacy priceTargets used. It is a volatility
//     envelope, not a level anyone defends, and it was used there to push the target FURTHER
//     out (scorer.go:747-750) — the exact move this file inverts.
//
// A resistance at or below zone.High is NOT_ABOVE_ZONE_HIGH: it is not something the trade has
// to get through, it is something it is already through, and clamping to it would publish a
// first target at or below the entry.
func resistanceCandidate(in Snapshot, zone *PriceZone) ResistanceCandidate {
	obs := in.PivotHigh
	c := ResistanceCandidate{
		Source:     LevelBreakoutHigh,
		Status:     obs.Status,
		PriceBasis: obs.BasisOr(in.PriceBasis),
	}
	if obs.Usable() {
		// COPIED, never aliased.
		v := *obs.Value
		c.Status = Available
		c.Value = &v
	} else if c.Status == Available || c.Status == "" {
		c.Status = Unavailable
	}

	if zone == nil || !zone.PriceBasis.Supported() {
		c.Disposition = RiskLevelNotScreened
		return c
	}
	v, ok := c.value()
	if !ok {
		c.Disposition = RiskLevelUnavailable
		return c
	}
	if c.PriceBasis != zone.PriceBasis {
		c.Disposition = RiskLevelBasisMismatch
		return c
	}
	if v <= zone.High {
		c.Disposition = RiskLevelNotAboveZoneHigh
		return c
	}
	c.Disposition = RiskLevelEligibleNotSelected
	return c
}

// TargetResult is the whole target step: the two targets and the risk/reward if they exist, and
// the record of how each absence was reached if they do not.
type TargetResult struct {
	Target1    *PriceLevel `json:"target_1,omitempty"`
	Target2    *PriceLevel `json:"target_2,omitempty"`
	RiskReward *RiskReward `json:"risk_reward,omitempty"`
	// Trace is the work record: every term of the risk, both R multiples, the resistance and
	// its disposition, the valuation ceiling and whether it bit, and each rejection.
	Trace TargetTrace `json:"trace"`
}

// ComputeTargets computes Target1, Target2 and the risk/reward for one snapshot, one zone and
// one invalidation.
//
// PURE and TOTAL: defined for every input including a nil zone and a nil invalidation, no
// clock, no I/O, and it never retains a pointer from its arguments.
//
// # The zone and the invalidation are ARGUMENTS
//
// They must be the ones the plan publishes — ComputePlan passes exactly those. Recomputing
// either here would be two chances for the numbers a reader sees and the numbers the R:R was
// computed from to be edited apart, and R:R is precisely the field where that disagreement
// would be invisible.
//
// # THE DEPENDENCY GRAPH IS NOT WIDENED, in either direction
//
//	no zone            → no risk, so no target and no R:R
//	no invalidation     → no risk, so no target and no R:R
//	no resistance       → Target1 STILL EXISTS, on the R multiple alone, and says so
//	no valuation        → Target2 STILL EXISTS, unclamped, and says so
//	Target2 unavailable → Target1 and the R:R still exist
//
// The two "STILL EXISTS" rows are the ones worth defending.
//
// TARGET 1 WITHOUT RESISTANCE IS PUBLISHED, and the alternative (require structural evidence or
// publish nothing) was considered and rejected for a global reason: a BREAKOUT plan can never
// have a usable resistance in this package. Its zone is [pivot, pivot + halfWidth], so the only
// resistance candidate — the pivot — is at or below zone.High BY CONSTRUCTION and screens out
// as NOT_ABOVE_ZONE_HIGH every time. Requiring resistance would therefore delete Target1, and
// with it the R:R, from EVERY breakout plan the package can produce, which is not a rule about
// evidence but an accident of geometry. And 2R is not a fabricated level: both of its terms —
// the entry band and the invalidation — are already published, structurally derived numbers, so
// the reader can check the arithmetic. It is labelled TARGET_1_R_MULTIPLE_ONLY on the row so it
// can never be mistaken for an observed level.
//
// TARGET 2 WITHOUT A VALUATION IS PUBLISHED, because the valuation is a SANITY BOUND and not an
// input: making Target2 depend on it would delete the target for every company with no filed
// positive-earnings basis — an accounting fact — and would silently re-scope this package into
// one that only plans trades on companies with a usable P/E.
func ComputeTargets(in Snapshot, zone *PriceZone, inv *Invalidation) TargetResult {
	var tr TargetTrace
	tr.Target1RMultiple = copyFloat(target1RMultiple)
	tr.Target2RMultiple = copyFloat(target2RMultiple)
	tr.Resistance = resistanceCandidate(in, zone)

	// 1. THE PUBLISHED ENTRY AND THE PUBLISHED INVALIDATION. Neither has a code of its own
	// here: ZoneTrace.Rejection and StopTrace.Rejection already say why they are missing, and
	// a second code would read as a second, independent failure — the rule ComputeMaxChase
	// follows for the same situation.
	if zone == nil || inv == nil {
		return TargetResult{Trace: tr}
	}
	tr.PriceBasis = zone.PriceBasis
	tr.EntryUsed = copyFloat(zone.High)
	tr.Invalidation = copyFloat(inv.Price)

	// 2. THE RISK DENOMINATOR.
	if inv.PriceBasis != zone.PriceBasis {
		tr.RiskRejection = RiskRejectBasisMismatch
		tr.Target1Rejection = ReasonRiskNotEstablished
		return TargetResult{Trace: tr}
	}
	risk := zone.High - inv.Price
	if !finitePositive(risk) {
		// NO SUBSTITUTE RISK. Not one ATR, not a percentage of the entry: without a
		// thesis-failure price there is no risk this plan can name, and a reward stated
		// against an invented denominator is a made-up ratio in the field a reader trusts most.
		tr.RiskRejection = RiskRejectNotPositive
		tr.Target1Rejection = ReasonRiskNotEstablished
		return TargetResult{Trace: tr}
	}
	tr.Risk = copyFloat(risk)

	// 3. TARGET 1 = min(2R, the nearest resistance above the entry).
	rTarget := zone.High + target1RMultiple*risk
	if !finitePositive(rTarget) {
		tr.Target1Rejection = ReasonTarget1TickAlignmentUnavailable
		return TargetResult{Trace: tr}
	}
	tr.Target1RTarget = copyFloat(rTarget)

	raw1, binding := rTarget, Target1FromRMultiple
	if res, ok := tr.Resistance.value(); ok && tr.Resistance.Disposition.Eligible() {
		// THE MIN, and the direction is the whole point of the rule: the first level in the
		// way is the first place a target can be filled. Taking the max — which is what the
		// legacy code does — publishes a target beyond a level the stock has not cleared.
		if res < raw1 {
			raw1, binding = res, Target1FromResistance
			tr.Resistance.Disposition = RiskLevelSelected
		}
	}
	tr.Target1Raw = copyFloat(raw1)
	tr.Target1Binding = binding
	tr.TickDirection = tickDirection(pricerule.RoundDown)

	// TICK ALIGNMENT, DOWN. A target is a SELL level for a long position, so the order
	// semantics are a sell limit: rounding UP asks the market for more than the rule
	// authorised, and a sell limit above the level the arithmetic justified is one the market
	// may never reach — the position stays open past its own plan. Rounding DOWN gives up at
	// most one tick of profit for a number that is always inside what the rule allows, keeps
	// the reward (and therefore the published R:R) UNDERSTATED rather than overstated, and
	// cannot invert the two targets relative to their raw values because both move the same way.
	target1, ok := pricerule.RoundToTick(raw1, pricerule.RoundDown)
	if !ok {
		// REACHABLE, unlike the chase step's equivalent: 2R is computed from the entry and
		// can leave pricerule's alignable domain (maxAlignablePrice, 1e6 元) even though every
		// input to it is a legal quote. No local rounding is substituted.
		tr.Target1Rejection = ReasonTarget1TickAlignmentUnavailable
		return TargetResult{Trace: tr}
	}
	if !finitePositive(target1) || target1 <= zone.High {
		// A target at or below the top of the entry band is not a target. It is reachable:
		// a resistance one tick above zone.High aligns DOWN onto zone.High itself.
		//
		// REFUSED, not nudged up a tick. The next tick up is a price the rule never computed,
		// and it would be published as the level the trade is aiming at.
		tr.Target1Rejection = ReasonTarget1NotAboveEntry
		return TargetResult{Trace: tr}
	}
	reward := target1 - zone.High
	tr.Target1 = copyFloat(target1)
	tr.Reward = copyFloat(reward)

	out := TargetResult{
		Target1: &PriceLevel{
			Price:      target1,
			PriceBasis: zone.PriceBasis,
			RMultiple:  rMultiple(target1, zone.High, risk),
			Basis:      target1Basis(binding, tr.Resistance.Source),
		},
	}

	// 4. THE RISK / REWARD, to TARGET 1.
	//
	// Computed here rather than in a separate step because it is the same arithmetic, and
	// because a separate step would be free to pick a different entry or a different target —
	// which is exactly how a flattering R:R gets built.
	ratio := reward / risk
	if finitePositive(ratio) {
		out.RiskReward = &RiskReward{
			RiskPerShare:   risk,
			RewardPerShare: reward,
			Ratio:          ratio,
			Target:         riskRewardTarget1,
		}
		tr.Ratio = copyFloat(ratio)
	}
	// else: unreachable — risk and reward are both finite and strictly positive above, so the
	// quotient is too. There is no `Ratio: 0` fallback: doc.go's MISSING ≠ ZERO clause names
	// this field, because 0 reads as "this trade offers no reward" rather than "we could not
	// compute it", and InvariantRiskRewardFinitePositive is the production backstop.

	// 5. TARGET 2 = min(3.5R from the ORIGINAL risk, the valuation base target).
	out.Target2 = target2(in, zone, risk, target1, &tr)

	out.Trace = tr
	return out
}

// target2 computes the second target, or nil with a reason on the trace.
//
// Split out because it is the one step with an OPTIONAL input, and the failure mode being
// designed against is the accidental dependency: every early return here leaves Target1 and the
// R:R already built by the caller.
func target2(in Snapshot, zone *PriceZone, risk, target1 float64, tr *TargetTrace) *PriceLevel {
	// 3.5R FROM THE ORIGINAL RISK. Deliberately not target1 + something: if Target1 was
	// clamped to resistance, deriving Target2 from it would inherit the clamp and the
	// R-multiple stamped on the row would stop being true.
	raw := zone.High + target2RMultiple*risk
	if !finitePositive(raw) {
		tr.Target2Rejection = ReasonTarget2TickAlignmentUnavailable
		return nil
	}
	tr.Target2Raw = copyFloat(raw)

	clamped, outcome := raw, valuationCeilingOutcome(in, zone)
	tr.ValuationSuitability = in.Valuation.Suitability
	if in.Valuation.Usable() {
		tr.ValuationTarget = copyFloat(*in.Valuation.BaseTargetPrice)
	}
	if outcome == ValuationCeilingNotBinding || outcome == ValuationCeilingApplied {
		if ceiling := *in.Valuation.BaseTargetPrice; ceiling < raw {
			clamped, outcome = ceiling, ValuationCeilingApplied
		} else {
			outcome = ValuationCeilingNotBinding
		}
	}
	tr.ValuationCeiling = outcome
	tr.Target2Clamped = copyFloat(clamped)

	if clamped <= zone.High {
		// The ceiling sits at or below the price this plan is willing to pay. A REAL and
		// important finding — the model says the stock is already at or above its base-case
		// fair value at the entry — and it is reported rather than repaired: raising Target2
		// back above the entry would publish a target the ceiling had just refused.
		tr.Target2Rejection = ReasonValuationCeilingBelowEntry
		return nil
	}
	target, ok := pricerule.RoundToTick(clamped, pricerule.RoundDown)
	if !ok {
		tr.Target2Rejection = ReasonTarget2TickAlignmentUnavailable
		return nil
	}
	if !finitePositive(target) || target < target1 {
		// A SECOND TARGET NEARER THAN THE FIRST. Reachable only through the valuation
		// ceiling, and it is refused three ways at once:
		//
		//   NOT SWAPPED   the two fields mean different things (2R-scaled vs 3.5R-scaled with
		//                 a fair-value bound); swapping would publish the ceiling as the
		//                 nearer target and the resistance-clamped level as the further one,
		//                 and both R multiples on the rows would then be lies.
		//   NOT RAISED    lifting Target2 to Target1 would discard the ceiling that produced
		//                 the conflict — the one piece of independent evidence in the step.
		//   NOT DROPPED SILENTLY  the code says which of the two it was.
		tr.Target2Rejection = ReasonTarget2BelowTarget1
		return nil
	}
	tr.Target2 = copyFloat(target)

	basis := []string{basisTarget2RMultiple}
	if outcome == ValuationCeilingApplied {
		basis = append(basis, basisValuationCeiling)
	}
	return &PriceLevel{
		Price:      target,
		PriceBasis: zone.PriceBasis,
		RMultiple:  rMultiple(target, zone.High, risk),
		Basis:      basis,
	}
}

// valuationCeilingOutcome decides whether the valuation target may cap Target2 at all.
//
// THREE independent refusals, each with its own code, and NONE of them removes Target2 — see
// ComputeTargets. The two "may cap" answers (NOT_BINDING / APPLIED) are separated by the
// caller, which is the only place the comparison can be made.
func valuationCeilingOutcome(in Snapshot, zone *PriceZone) ValuationCeilingOutcome {
	if !in.Valuation.Usable() {
		return ValuationCeilingUnavailable
	}
	if !in.Valuation.Suitability.PermitsCeiling() {
		// UNSUITABLE, or a verdict this package cannot read. The number is NOT used, and it
		// is NOT used in the other direction either: an unsuitable valuation must not raise a
		// target any more than it may lower one. See ValuationSuitability.
		return ValuationCeilingModelUnsuitable
	}
	if in.Valuation.BasisOr(in.PriceBasis) != zone.PriceBasis {
		return ValuationCeilingBasisMismatch
	}
	return ValuationCeilingNotBinding
}

// BasisOr returns the valuation target's own basis, falling back to the snapshot's when it
// declared none — the same contract PriceObservation.BasisOr states, and for the same reason.
func (v ValuationEvidence) BasisOr(def PriceBasis) PriceBasis {
	if v.PriceBasis != "" {
		return v.PriceBasis
	}
	return def
}

// target1Basis names what makes this the first target, as stable codes.
func target1Basis(binding Target1Binding, src LevelSource) []string {
	if binding == Target1FromResistance {
		return []string{basisTarget1Resistance, string(src)}
	}
	return []string{basisTarget1RMultipleOnly}
}

// rMultiple is how far a level sits from the entry in units of the initial risk.
//
// Nil when the answer is not a finite number. It is recomputed from the PUBLISHED, tick-aligned
// price rather than carried from the raw one, so the multiple on the row is the multiple of the
// number next to it: after a resistance clamp and a rounding, the target is no longer at 2.0R,
// and stamping 2.0 on it would be the one field in the plan a reader cannot check.
func rMultiple(price, entry, risk float64) *float64 {
	if !finitePositive(risk) {
		return nil
	}
	m := (price - entry) / risk
	if !finite(m) {
		return nil
	}
	return copyFloat(m)
}
