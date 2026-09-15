// Package entryplan is the Entry Planning layer: given that the scanner has already decided a
// stock is worth owning, at what PRICE should it be bought.
//
// The governing rule, from which everything else follows:
//
//	entryplan TRANSLATES an existing entry semantic into a price. It does not invent a
//	second opinion about whether to buy.
//
// This repo already says when to buy, in several vocabularies that took work to earn:
// scanner.ActPullbackBuy and scanner.ActBreakoutBuy, the OVERHEATED "太熱不要追" entry
// caution, and the watchlist's 「拉回 5/10 日線量縮承接」 wording. None of those is
// re-litigated here. EntryStatus is the EXECUTION-SIDE reading of them — WAIT_PULLBACK is
// what ActPullbackBuy means once it has to be turned into a limit order — and the mapping
// itself is EP-5's work, not this file's.
//
// Direction of the wiring, which the architecture test enforces:
//
//	scanner output / evidence  →  entryplan (post-pass)  →  report
//
// and never entryplan → scanner decision. The scanner must not be able to consult a price
// plan while deciding whether there is a trade at all; that would make the plan an input to
// the very verdict it is supposed to be downstream of.
//
// # What EP-1 is
//
// The skeleton and the vocabulary. ComputePlan answers INSUFFICIENT_DATA for every input and
// emits no numbers whatsoever. Zones, chase limits, invalidation levels, targets and R:R are
// later work items; the types exist so those items have a contract to fill, and the tests
// exist so filling it cannot quietly change what the words mean.
//
// # What EP-2 is
//
// The PERMISSION layer, and the one number-free half of the policy: what kind of buying each
// market regime allows (policy.go), which kind of entry is being asked about
// (EntrySemantic), and what a multi-day regime series is allowed to be (series.go).
//
// EP-2 emitted no price at all. That claim was swept over the whole input matrix by a test
// called TestEP1EmitsNoExecutionPrices, which EP-3b retired and replaced with the narrower
// TestNoLevelEvidenceMeansNoPriceAtAll — the half of it that is still true, and still worth
// having: with no level to centre a zone on, every price pointer must be nil. The chase
// tolerances in ChasePolicy were MULTIPLIERS that nothing multiplied.
//
// Three lines EP-2 draws, each of which is a test rather than a paragraph:
//
//   - BEAR ≠ UNKNOWN. BEAR maps to PolicyDefensive with every permission DISALLOWED, which
//     is a DECISION. UNKNOWN maps to PolicyUnresolved with every permission UNRESOLVED,
//     which is the ABSENCE of one. regime_engine.go's R0 rule is quoted at PolicyUnresolved
//     because it is the same sentence one layer down: UNKNOWN "is a state, not a failure, and
//     it must never fall through to SIDEWAYS ('we looked, no edge') which is a verdict".
//   - ALLOWED / DISALLOWED / UNRESOLVED, and nothing else. EP-2 does not produce BUY_NOW,
//     WAIT_PULLBACK or WAIT_BREAKOUT: each of those needs a price relative to a level, and
//     turning a permission into one of them is EP-5's decide step.
//   - LIVE ≠ REPLAY, in the type. A RegimeSeries carries ONE RegimeSource, its fields are
//     unexported so no struct literal can bypass the check, and there is no constructor,
//     loader or fallback through which the two archives can be blended. The measurement that
//     forced this is on RegimeSource: same session, identical prices, breadth off by 3.91pp.
//
// # What EP-3a is
//
// The GUARD, shipped before the thing it guards. invariants.go turns the invariants this
// package has only ever stated in prose into a checker — PlanInvariantViolations — and the
// tests feed it Plans built BY HAND, because EP-2 still produces none that could break one.
//
// It is a hard precondition of EP-3b rather than a nice-to-have. EP-1's defence of plain
// float64 on PriceZone.Low/High, Invalidation.Price, PriceLevel.Price and the RiskReward
// fields is "the struct exists only when it has real values", and that argument has cost
// nothing so far: no struct was ever built, so the suite could assert the stronger property
// (every price pointer is nil) and no plain float was ever exposed. EP-3b returned the first
// zone, that test retired, and the argument now has to be TRUE — which is why the checker had
// to exist before the producer rather than after it.
//
// Still no price. EP-3a computes nothing, decides nothing and is called by nothing: see
// invariants.go on why the item that writes a guard must not also decide what happens when it
// fires. RuleVersion stayed EP2-v1, and golden_test.go pins that value as a LITERAL, which
// the suite previously did not — every version assertion compared the constant to itself.
//
// # What EP-3b is
//
// THE FIRST ACTIONABLE PRICE THIS PACKAGE HAS EVER PRODUCED. Two of them:
//
//	IdealEntry     the band this entry may be bought in       (ComputeIdealEntryZone)
//	MaxChasePrice  the highest price the plan permits paying   (ComputeMaxChase)
//
// and, under EP-3b, nothing else.
//
// # What EP-4 is
//
// THE RISK SIDE, and it is what turns a pair of entry prices into a plan a reader can decide
// on. Four more numbers:
//
//	Invalidation  the price at which the support / breakout THESIS stops being true
//	Target1       the first level the move can realistically reach
//	Target2       the second, optionally capped by a projected valuation target
//	RiskReward    the reward to Target1 over the risk to the invalidation
//
// and NO STATUS YET at that point in the sequence: EP-4 stopped before deciding what the market's
// position relative to those prices MEANT.
//
// # What EP-5 is
//
// EP-5 answers that, and it is the layer that first publishes a verdict. DecideStatus is an
// ORDERED table (S0-S19, first match wins) over what EP-2/EP-3/EP-4 already established — the
// entry semantic, the regime policy, the current price, the zone, the chase ceiling and the
// invalidation — and it computes no price of its own. DecisionInput carries no ATR, no level, no
// tick size, no previous close and no valuation, so recomputation is unwritable there rather
// than merely forbidden.
//
// The six statuses and the two distinctions that make them worth having:
//
//	BUY_NOW             two authorised shapes only, S10 (inside the band) and S14 (a BREAKOUT
//	                    above it, at or below a ceiling, under a policy that explicitly permits
//	                    paying at the market). There is no third path.
//	WAIT_PULLBACK       S17. Reached when chasing is refused, because "you may not chase" is a
//	                    different permission from "you may not wait".
//	WAIT_BREAKOUT       S18. The level has not been reached.
//	TOO_EXTENDED        S9, and the only arm that produces it: a finite ceiling exists and the
//	                    market is strictly above it. The zone, the ceiling, the invalidation and
//	                    the targets all SURVIVE — the answer to "too expensive" is "wait for
//	                    what", and a status with no band cannot say it.
//	NO_VALID_ENTRY      a VERDICT. The scanner's Action contradicts the thesis (S1, EP-6D — and
//	                    then no price is published), the policy refused (S4, the BEAR arm), or a complete search
//	                    found no legal risk boundary (S8).
//	INSUFFICIENT_DATA   a STATE. Something could not be read: the semantic, the price, the
//	                    policy, the zone, the ceiling, or the stop's own evidence.
//
// The last two are the pair this package exists to keep apart, and the policy arms sit ahead of
// every price arm so that no price relation can move either: BEAR is a refusal at S4 before
// TOO_EXTENDED is ever asked, and UNKNOWN is unresolved at S3.
//
// TestEP5PublishesAVerdictAndEveryPriceEP4Computed sweeps every input and requires all six
// statuses to occur; TestTheDecisionMatrixIsTheSpecifiedTable drives DecideStatus directly over
// 71 rows reaching all 19 arms; TestTheDecisionPrecedenceIsTheSpecifiedOrder hands two arms an
// input they both match and pins which one wins.
//
// The rules, in one place:
//
//	pullback centre  = max(eligible support below the last price)   MA20 / MA60 / base low
//	breakout centre  = the pivot high, and NEVER a substitute
//	half width       = max(0.5 × ATR(14), 2 × tick(centre))
//	pullback zone    = centre ± halfWidth      aligned DOWN / UP
//	breakout zone    = [pivot, pivot + halfWidth]  aligned UP / UP
//	rawMaxChase      = zone.High + policy.MaxATR × ATR(14)
//	MaxChasePrice    = min(rawMaxChase, limitUp(previousClose)) aligned DOWN
//	Invalidation     = max(structural levels strictly below zone.Low)   aligned DOWN
//	                   fallback, only if none: zone.Low − 0.5 × ATR(14)
//	risk             = zone.High − Invalidation                (zone.High, NEVER the midpoint)
//	Target1          = min(zone.High + 2.0 × risk, nearest resistance above zone.High)  DOWN
//	Target2          = min(zone.High + 3.5 × risk, valuation base target)               DOWN
//	RiskReward       = (Target1 − zone.High) / risk
//
// Four of EP-4's decisions are worth reading twice, each because the plausible alternative
// produces a number instead of an absence, or a flattering number instead of an honest one:
//
//   - AN INVALIDATION IS A STRUCTURAL STATEMENT, NOT A LOSS BUDGET. The legacy priceTargets
//     (scanner/scorer.go:723) publishes entry − 2×ATR, bbLower × 0.99 and entry × 0.93 through
//     the same kind of field. None is a level, all three are finite, positive, below the entry
//     and on the tick grid, and each answers "how much am I willing to lose" — a different
//     question with a different owner. When no structural level qualifies, EP-4 publishes NO
//     invalidation and loses its targets and its R:R with it.
//   - max(), NOT min(), AND NOT FOR THE REASON THE ENTRY USES max(). Every structural candidate
//     is a SUFFICIENT falsifier, so the thesis holds only above ALL of them and the boundary of
//     that conjunction is the highest one. min() would keep a thesis alive past the point it was
//     already known to be wrong AND inflate the risk denominator. See SelectInvalidation.
//   - RESISTANCE IS A CEILING ON TARGET1, NOT A FLOOR. The legacy code does
//     `if bbUpper > t1 { t1 = bbUpper }` — it pushes the target FURTHER OUT. EP-4 takes the min:
//     the first level in the way is the first place a target can be filled. See ComputeTargets.
//   - THE RISK IS MEASURED FROM zone.High, NOT THE MIDPOINT. The midpoint shrinks the
//     denominator and lengthens the numerator at once, inflating R:R twice, on the one number a
//     reader decides with. Systematic optimism is the worst bias available on an order ticket.
//
// And EVERY EP-4 PRICE ROUNDS THE WAY THAT MAKES THE PLAN LOOK WORSE: the invalidation DOWN
// (further away, so risk is never understated and the thesis is never called dead early), both
// targets DOWN (nearer, so the reward and the R:R are never overstated). The direction is
// argued from order semantics at each call site, not asserted here.
//
// Four decisions in there are worth reading twice, because each one had a plausible
// alternative that would have produced a number instead of an absence:
//
//   - AN UNAVAILABLE ATR MEANS AN UNAVAILABLE ZONE. Not a tick-only width, not a percentage
//     of the last price, not the legacy 2.5%. A missing ATR is a missing DEPENDENCY, not the
//     trigger of a second rule, and the two widths differ by an order of magnitude at the same
//     price — so publishing either through one field leaves a reader unable to tell which rule
//     produced the number in front of them. See ReasonZoneATRUnavailable.
//   - A PULLBACK ZONE IS NEVER CLAMPED TO THE LAST PRICE. If its high lands above the market
//     the zone stays as computed and the fact is FLAGGED. Clamping would cut the high while
//     leaving the low at centre − halfWidth, and "the zone is the support ± half an ATR" —
//     the only sentence that makes the number defensible — would become false while still
//     being printed. See ReasonPullbackZoneOverlapsCurrentPrice.
//   - THE PREVIOUS CLOSE IS NOT THE CURRENT PRICE. A daily price limit is a band around the
//     PREVIOUS session's reference price, so computing it from today's price gives a ceiling
//     that moves with the thing it bounds. Its absence removes the CEILING and keeps the ZONE:
//     the two availabilities are independent.
//   - THE ADJUSTMENT AGE IS A CAVEAT, NEVER A GATE. The residue in a Wilder ATR decays as
//     ((N-1)/N)^age — 1.03% at 62 bars, 0.96% at 63 — so 63 is where a report stops mentioning
//     it and NOT the day anything becomes clean. Suppressing a plan over a residue of a
//     fraction of a percent would delete a usable entry.
//
// EVERY EXECUTABLE PRICE GOES THROUGH internal/pricerule, which owns the tick grid and the
// ±10% band as transcribed market rules. There is no math.Round(v*10)/10 and no %.1f in this
// package; TestNoPriceIsRoundedOutsidePriceRule scans the source for both, and
// TestTheZoneAndChaseRulesUsePriceRule asserts the positive direction so the ban cannot be
// satisfied by computing nothing. Every pricerule call checks its `ok`: the EP-0 review
// measured ok == false at 0.6% of real inputs (12 of 1,995 cached codes classify UNKNOWN under
// ClassifyLimitRule), which is exactly the rate at which a discarded error is never noticed.
//
// EP-3b also WIRES IN the two things EP-2 and EP-3a deliberately left unwired, which is why
// they were separate items:
//
//   - THE REGIME → POLICY TABLE, for PERMISSION only. A DISALLOWED semantic produces no zone;
//     an UNRESOLVED one produces no zone under a DIFFERENT code. The three chase nils
//     (CHASE_NOT_ALLOWED / CHASE_POLICY_UNRESOLVED / PREVIOUS_CLOSE_UNAVAILABLE) stay apart,
//     which is what ChasePolicy's split of the decision from the number was for. It still
//     produces no EntryStatus: turning a permission plus a price into a verdict is EP-5's.
//   - THE INVARIANT CHECKER, as a gate on the returned plan. EnforcePlanInvariants withdraws
//     the affected executable prices from a plan that breaks one, keeps the evidence and the
//     trace, and records both the violated codes and the withdrawn field names — so "the gate
//     ran and found nothing" and "the gate is gone" are different outputs, and so are "it took
//     the second target" and "it took everything". No input reaches it; the reason code is
//     RESERVED and the gate is driven directly by test.
//
// And it adds the TRACE. A price a reader can act on cannot be explained by naming the rule
// that produced it, so Plan.EntryTrace carries the arithmetic: every candidate level and what
// happened to it, both terms of the width, the raw bounds before alignment, the previous
// close, the legal ceiling, whether the clamp bit, and which way each price was rounded.
//
// Four properties are load-bearing and are asserted by tests rather than promised here:
//
//   - PURE. ComputePlan is a deterministic total function of its Snapshot. No clock, no
//     randomness, no network, no database, no filesystem, no AI, no report rendering. A
//     source scan asserts it, so "pure" cannot decay into "pure last time anyone looked".
//   - MISSING ≠ ZERO. Every price that can be absent is a pointer, and absent means nil.
//     A 0 here would be a real price — TWSE quotes are positive — so a zero-valued
//     MaxChasePrice would read as "never chase", which is a decision this layer did not make.
//   - MISSING ≠ LOW CONFIDENCE. ClassifyConfidence checks availability BEFORE it grades, for
//     the same reason valuation.ClassifyQuality does: "we computed it, trust it less" and
//     "there is nothing to trust or distrust" are different sentences.
//   - NO NaN / Inf ESCAPES. A reflective sweep walks every float reachable from a Plan,
//     through pointers and slices, and fails on a non-finite value. Since EP-3a the same
//     sweep exists in PRODUCTION as part of the invariant checker (invariants.go), and the
//     test's own walker is kept as an independent second implementation on purpose —
//     TestTheTwoFloatWalkersAgree cross-checks the two so one bug cannot blind both.
//
// # INSUFFICIENT_DATA is not a verdict, and NO_VALID_ENTRY is
//
// The distinction is inherited, not invented. internal/market/model's Regime doc says
// UNKNOWN "is not a sixth market condition: it is the honest answer when the data cannot
// support a call", and analyzer.DecideRegime's R0 rule says UNKNOWN "is a state, not a
// failure, and it must never fall through to SIDEWAYS ('we looked, no edge') which is a
// verdict". The same two sentences apply one level down:
//
//	INSUFFICIENT_DATA  is a state.   We could not look.
//	NO_VALID_ENTRY     is a verdict. We looked, and there is no legal price to buy at.
//
// Collapsing them would make "the ATR was unavailable" and "every entry level has already
// been taken out" the same row in a watchlist, and only one of those is worth acting on.
// TestInsufficientDataIsNotAVerdict holds the line.
//
// # TODO — carried follow-ups, with the work item that must clear them
//
// Recorded in code rather than in a review thread, because a follow-up that lives only in a
// thread is a follow-up nobody is holding. NONE of them is implemented by EP-2, and each names
// the item that MUST do it.
//
// The J-* and D-3 entries came out of the EP-2 review. Each one is a guard a comment CLAIMED
// and did not provide; the claims have been corrected in place — that part is done, and it is
// the part that could mislead a reader — while the guards themselves are listed here.
//
// DONE (EP-3a) — was TODO(EP-3, BEFORE the first `return &PriceZone{...}`):
// planInvariantViolations + a planted bad Plan. The checker is invariants.go
// (PlanInvariantViolations / AuditPlanInvariants), and it is verified in invariants_test.go
// against Plans built by hand precisely because production still cannot build one — the
// subject under test is the CHECKER, in the pattern the purity sweep's planted `os.WriteFile`
// line and the walkFloats 7-floats/5-non-finite fixture already use.
//
// WHAT IT COVERS, since EP-6D, is TWENTY invariants (EIGHTEEN under EP-5, plus EP-6D's two:
// BUY_NOW_THESIS_NOT_CONTRADICTED — a BUY_NOW's decision trace must record the thesis standing
// NOT_CONTRADICTED — and THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY — a CONTRADICTED standing carries
// no executable price and no entering or waiting status). EP-3b's seven: a zone bound is strictly
// positive (twice), Low <= High, no NaN, no Inf, MaxChasePrice > 0, MaxChasePrice >= IdealEntry.High.
// EP-5's seven are listed at AllPlanInvariants and check the VERDICT against the prices beside it
// — that the status is a canonical value, that it carries a reason, that BUY_NOW has a zone and a
// price the policy authorised, that TOO_EXTENDED keeps its band and sits above its ceiling, and
// that a WAIT matches the semantic it is waiting for. And EP-4's
// RiskReward field finite and strictly positive. The first seven were EP-3a's; the last four
// were DELIBERATELY LEFT to the item that first produces a stop level, and EP-4 is that item, so
// TestTheStopAndTargetInvariantsAreDeliberatelyNotImplementedYet has been replaced by
// TestTheStopAndTargetInvariantsAreNowImplementedAndProductionClean.
//
// STILL NOT COVERED, on purpose: that RiskReward.Ratio equals RewardPerShare / RiskPerShare.
// That is an arithmetic-consistency claim rather than a geometry one, and every term is on
// TargetTrace so a reader can do the division. See invariants.go.
//
// WIRED IN BY EP-3b, which is the item that can produce a violation. The decision it made is
// enforce.go's: withdraw the offending prices, keep the plan, and record the outcome on the
// trace. The other two candidates — return the illegal price with a warning attached, or refuse
// to return at all — are argued and rejected there. EP-4 NARROWED the withdrawal from "every
// price" to "the violated field and everything computed from it", because with six executable
// fields and a real dependency graph the old rule would delete a legal entry zone over an
// inconsistent OPTIONAL second target; the field names it removed are recorded on the trace.
//
// F1 — DELIVERED AND REVIEWED AS PART OF EP-7 (not part of EP-6G): the mixed price+status
// repair gets the distinct stamp PRICES_AND_STATUS_WITHDRAWN on EntryTrace.InvariantCheck, so
// neither half of the repair is lost when plans are grouped by that field, and a planted
// mixed-plan test (TestTheGateRecordsMixedPriceAndStatusWithdrawal) covers the otherwise
// production-unreachable case. EP-7's evidence projection does not persist InvariantCheck (it is
// not one of the ep_* keys in internal/research/entryplan_evidence.go), so the stamp is carried
// on EntryTrace only and is not in the research store.
//
// TODO(hardening, no deadline): stripProse in architecture_test.go does not understand RUNE
// literals. A production line that put a quote character in a rune literal on the same line
// as a forbidden call — a map from a rune to something read out of the environment — would
// leave the stripper mid-"string" and the rest of the line unscanned. It has no such line
// today, and the gap is real. NOTE: the fix is to teach the stripper about runes; it is NOT
// to edit that function's doc comment to claim it already handles them.
//
// DONE (EP-3b) — was TODO(the first item that ARCHIVES or PERSISTS a Plan): the literal
// golden. golden_test.go now pins the PERSISTED VALUE of RuleVersion, of every Reason, of
// every Evidence key (in emission order), of every PolicyName, Requirement, EntrySemantic,
// EntryStatus, Availability, PriceBasis, Allowance, Confidence and PlanInvariant, of EP-3b's
// seven new vocabularies, and of the JSON field names of every price-bearing and trace type.
// It was brought forward from "the first item that archives a plan" because EP-3b is the item
// that makes the strings matter: a plan now carries a price, so somebody will store one.
//
// DONE (EP-3b) — was TODO(EP-3 / MaxChase): the Snapshot's missing fields. PreviousClose is
// there and is read by ComputeMaxChase in the same commit that declared it; MA20, MA60,
// BaseLow and PivotHigh are read by ComputeIdealEntryZone. AvgVolume20 is there too and is
// RECORDED ONLY — deliberately not an execution gate, because this repo has no backtested
// "minimum volume at which a chase fills" contract and a threshold invented here would be a
// fabricated tradeability claim. TestLiquidityIsRecordedAndNeverGates holds that line.
//
// TODO(EP-4, J-1) — NOT DONE BY EP-3b, whose scope was the entry zone and the chase ceiling.
// Re-tagged from EP-3 rather than left addressed to an item that has shipped, because a TODO
// pointing at a finished item is a TODO nobody holds. The same applies to J-2 and J-3 below;
// J-3's registry half is now partly covered, in that golden_test.go pins the literal values of
// AllRequirements, but the "not yet evaluated" guard and KnownRequirement are still absent.
//
// TODO(EP-4, J-1): an EQUIVALENCE TEST between RegimeSession.Project and ComputePlan's regime
// row. The two read model.Regime three ways and agree on the row that matters (UNKNOWN →
// INSUFFICIENT_DATA), but they DISAGREE on "not a regime at all": Project returns UNAVAILABLE,
// ComputePlan returns INSUFFICIENT_DATA when the caller labelled the evidence AVAILABLE. Nothing
// compares them, so the drift was found by reading, not by a failure. Project's reading is the
// defensible one — nothing computed a string that is not a regime — so the likely fix is in
// plan.go, which makes this a behaviour change and not a doc fix. Project's doc has been
// corrected to stop claiming the two "cannot drift".
//
// TODO(EP-4, J-2): a STRUCTURAL guard on SessionEvidence's field set, of the shape
// TestEveryProjectionNamesItsArchive claims to have and does not. A new per-row field —
// `BreadthSource RegimeSource`, hardcoded to LIVE inside Project — currently passes the whole
// suite, which is exactly the "a source blended after the fact" failure RegimeSeries.source
// exists to prevent. The reflective walk in policy_test.go's price sweep is the pattern:
// enumerate the fields, and require every RegimeSource-typed one to be the series' own.
//
// TODO(EP-4, J-3): Requirement has no structural guard for "not yet evaluated", and no
// well-formedness check. TestThePolicyLayerCannotSeeAPrice covers INSIDE_VALID_ZONE and
// NEAR_SUPPORT only incidentally — they are price conditions — and does NOT cover
// ELEVATED_EVIDENCE, which is a setup SCORE and would slip through a price-shaped sweep.
// AllRequirements also lacks the three checks AllPolicyNames and AllReasons both have
// (uniqueness, non-empty, SCREAMING_SNAKE) and a KnownRequirement helper for consumers to
// validate a decoded code with.
//
// TODO(the ADAPTER work item, D-3): "it hands its rows to NewRegimeSeries" (see RegimeSeries)
// is currently a documented EXPECTATION with no mechanism behind it — an adapter returning
// []RegimeSession, or building a RegimeSeries by other means, would satisfy every test in this
// package. The adapter's own work item must carry a test that its exported loaders RETURN
// RegimeSeries and never []RegimeSession, so the invariants are unavoidable rather than
// merely recommended.
package entryplan
