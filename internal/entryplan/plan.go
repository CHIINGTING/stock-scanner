package entryplan

// Evidence keys. Stable identifiers, so an archived plan can be joined back to what it was
// computed from after the field names in this file have moved on.
const (
	EvidenceCurrentPrice   = "CURRENT_PRICE"
	EvidencePriceBasis     = "PRICE_BASIS"
	EvidenceMarketRegime   = "MARKET_REGIME"
	EvidenceEntrySemantic  = "ENTRY_SEMANTIC"
	EvidenceATR            = "ATR"
	EvidenceAdjustmentAge  = "ADJUSTMENT_AGE"
	evidenceSourceSnapshot = "snapshot"
	// evidenceSourceAdjustmentAge names the function the age came from, because the number
	// is meaningless without knowing which one — see AdjustmentAge.
	evidenceSourceAdjustmentAge = "fetcher.LastAdjustmentAge"
)

// ComputePlan turns a Snapshot into a Plan.
//
// PURE and TOTAL: a deterministic function of its argument, defined for every input including
// the zero Snapshot. No clock, no I/O, no randomness, no AI. It never mutates its argument
// and never retains a pointer from it — every number it publishes is copied, so a caller
// mutating its own float afterwards cannot change an already-returned plan.
//
// # What it does under EP6G-v1 (EP-5's table plus the EP-6D thesis standing: S1 answers a
// CONTRADICTED standing with NO_VALID_ENTRY and no price, and an unclassified one removes
// BUY_NOW; EP-6G changes no rule in this function — it makes the BASE valuation ceiling that
// EP-4 already defined reachable from the production bridge, and withholds the valuation
// target's value from the VALUATION_BASE_TARGET evidence row of an S1 plan)
//
// It computes SIX NUMBERS and, since EP-5, ONE VERDICT:
//
//	IdealEntry     the band this entry may be bought in            (ComputeIdealEntryZone)
//	MaxChasePrice  the highest price the plan permits paying        (ComputeMaxChase)
//	Invalidation   the price at which the thesis is no longer true  (ComputeInvalidation)
//	Target1        the first level the move can realistically reach (ComputeTargets)
//	Target2        the second, optionally capped by a valuation      "
//	RiskReward     reward to Target1 over the risk to Invalidation   "
//	Status         BUY_NOW / WAIT_PULLBACK / WAIT_BREAKOUT /        (DecideStatus)
//	               TOO_EXTENDED / NO_VALID_ENTRY / INSUFFICIENT_DATA
//
// THE ORDER OF THOSE SEVEN IS THE DEPENDENCY ORDER, and the last one consumes the other six
// without recomputing any of them. DecideStatus is handed a PROJECTION — the published prices
// and the codes that explain the absent ones — so the verdict and the numbers a reader sees
// cannot be edited apart, and no rule in decide.go could re-derive a level even if it tried.
//
// EVERY ABSENT PRICE IS PASSED WITH THE CODE THAT EXPLAINS IT. That is the single most
// consequential line in the wiring: `Zone == nil` alone would merge "the ATR was missing" with
// "there is nowhere legal to buy", and those are INSUFFICIENT_DATA and NO_VALID_ENTRY.
//
// The regime → policy table (policy.go) is consulted ONCE and threaded into all of it: a
// DISALLOWED semantic produces no zone; an UNRESOLVED one produces no zone for a DIFFERENT
// reason, with a different code; and EP-5 reads the same PolicyResult to tell BEAR's verdict
// from UNKNOWN's state.
//
// # What it refuses to do
//
// It does not substitute. There is no `if atr <= 0 { atr = currentPrice * 0.025 }`, no
// `if support <= 0 { support = currentPrice * 0.95 }`, no `entry = currentPrice`, and no
// `if previousClose == nil { previousClose = &currentPrice }`. Every one of those is a
// DIFFERENT RULE published through the output field of the rule the reader believes produced
// it, and each survives every downstream check — the number is finite, positive, ordered and
// on the tick grid, and it is not a level. So a missing input produces a MISSING OUTPUT plus
// the code that names which input was missing.
//
// Every executable price goes through internal/pricerule, which is where the tick grid and
// the daily price limit live. Nothing here rounds a price itself.
//
// # The confidence census is EP-2's, unchanged, on purpose
//
// Four required items and two corroborating ones — exactly as under EP2-v1, even though six
// new evidence rows arrived with EP-3b and three more arrive with EP-4. The level evidence, the
// previous close, the liquidity row, the base low's KIND and the projected valuation are all
// RECORDED AND NOT COUNTED.
//
// The reason is that Confidence grades "how much evidence stands behind the plan" on a scale
// whose meaning is already archived: moving MA20 and the previous close onto the census would
// re-grade every stock that has no base low from HIGH to MEDIUM without anything about the
// stock or the plan having changed, and a grade that shifts because a rule item was added is
// a grade nobody can compare across two dates. Re-scaling confidence is a confidence-rule
// change and it needs its own item, its own argument and its own review.
// TestTheConfidenceCensusIsStillTheOneEP2Shipped asserts the counts rather than leaving them
// to drift.
func ComputePlan(in Snapshot) Plan {
	p := Plan{
		// Copied verbatim, including empty, so a malformed input is traceable to the caller
		// that sent it rather than silently renamed.
		Symbol:      in.Symbol,
		AsOf:        in.AsOf,
		Status:      StatusInsufficientData,
		RuleVersion: RuleVersion,
	}

	// 0. Not a stock. Checked FIRST, for the reason valuation.ClassifySuitability checks its
	// empty archive first: every rule below reports on the EVIDENCE FOR A SYMBOL AT A
	// SESSION, and when neither is established there is nothing for the evidence to be about.
	// A per-item diagnosis here would dress a caller bug up as a market condition.
	if !in.WellFormed() {
		p.Reasons = []Reason{ReasonSnapshotMalformed}
		p.Confidence = ClassifyConfidence(ConfidenceMetrics{}, false)
		p.Caveats = []string{"輸入快照缺少代號或交易日,無法對應到任何標的"}
		return p
	}

	var (
		m       ConfidenceMetrics
		reasons []Reason
		ev      []Evidence
	)

	// ── required evidence ─────────────────────────────────────────────────────────────
	//
	// "Required" here means required for ANY entry price to exist, not required by a
	// particular rule. Rule-specific requirements belong to the rules.

	// 1. Price basis. First, because it decides what the other price evidence even means.
	basis := in.CurrentPrice.BasisOr(in.PriceBasis)
	m.RequiredTotal++
	basisEv := Evidence{Key: EvidencePriceBasis, Status: Unavailable, Source: evidenceSourceSnapshot}
	if basis.Supported() {
		m.RequiredPresent++
		basisEv.Status = Available
		basisEv.Text = string(basis)
		basisEv.PriceBasis = basis
	} else {
		reasons = append(reasons, ReasonPriceBasisUnavailable)
	}
	ev = append(ev, basisEv)

	// 2. Current price. The one input with no substitute: every output of this package is a
	// price stated relative to it.
	m.RequiredTotal++
	priceEv := Evidence{Key: EvidenceCurrentPrice, Status: in.CurrentPrice.Status,
		PriceBasis: basis, Source: evidenceSourceSnapshot}
	if in.CurrentPrice.Usable() {
		m.RequiredPresent++
		priceEv.Status = Available
		// COPIED, not aliased.
		v := *in.CurrentPrice.Value
		priceEv.Value = &v
	} else {
		reasons = append(reasons, ReasonCurrentPriceUnavailable)
		// A labelled-AVAILABLE price that is absent, non-finite or non-positive is not
		// available, whatever the label said. The value is dropped rather than published.
		if priceEv.Status == Available || priceEv.Status == "" {
			priceEv.Status = Unavailable
		}
	}
	ev = append(ev, priceEv)

	// 3. Market regime. Required because the same 5% pullback is a gift in BULL_PULLBACK and
	// a knife in BEAR, and a plan that cannot say which is not a plan.
	m.RequiredTotal++
	regimeEv := Evidence{Key: EvidenceMarketRegime, Status: in.Regime.Status, Source: evidenceSourceSnapshot}
	if in.Regime.Usable() {
		m.RequiredPresent++
		regimeEv.Status = Available
		regimeEv.Text = string(in.Regime.Regime)
	} else {
		reasons = append(reasons, ReasonMarketRegimeUnavailable)
		if in.Regime.Status.OK() {
			// Computed, and the data could not support a call — model.RegimeUnknown. That
			// is a DIFFERENT fact from never having been computed, and it survives here as
			// the text even though both produce the same reason code.
			regimeEv.Status = InsufficientData
			regimeEv.Text = string(in.Regime.Regime)
		} else if regimeEv.Status == "" {
			regimeEv.Status = Unavailable
		}
	}
	ev = append(ev, regimeEv)

	// 4. The entry semantic — WHICH KIND of entry is being priced.
	//
	// Required because this package TRANSLATES an existing entry semantic into a price
	// (doc.go) and must not invent one: a pullback entry and a breakout entry sit on opposite
	// sides of the market, and guessing between them here would be the second opinion the
	// architecture test exists to prevent.
	//
	// UNDER EP-1 THIS ITEM WAS UNFILLABLE. The census counted "an entry evaluation" as
	// required while the Snapshot had no field that could carry one, so the required floor
	// could never clear for ANY input and the whole confidence axis was dead — a permanently
	// failing assertion rather than a requirement. EP-2 supplies the field the item was always
	// about. The remaining gap is the RULE, and a missing rule is not missing evidence: it is
	// reported as ReasonEntryEvaluationNotImplemented below and it holds Status at
	// INSUFFICIENT_DATA, which is where it belongs.
	m.RequiredTotal++
	semEv := Evidence{Key: EvidenceEntrySemantic, Status: in.EntrySemantic.Status,
		Source: evidenceSourceSnapshot}
	if in.EntrySemantic.Usable() {
		m.RequiredPresent++
		semEv.Status = Available
		semEv.Text = string(in.EntrySemantic.Semantic)
	} else {
		reasons = append(reasons, ReasonEntrySemanticUnavailable)
		if in.EntrySemantic.Status.OK() {
			// Handed to us, and the upstream layer could not say which kind of entry this
			// is (EntrySemanticUnknown, or a semantic no producer emits). A DIFFERENT fact
			// from never having been handed one, preserved on the row even though both
			// produce the same reason code.
			semEv.Status = InsufficientData
			semEv.Text = string(in.EntrySemantic.Semantic)
		} else if semEv.Status == "" {
			semEv.Status = Unavailable
		}
	}
	ev = append(ev, semEv)

	// ── optional (corroborating) evidence ─────────────────────────────────────────────

	// ATR. RECORDED, NEVER REQUIRED — see ATREvidence. Its absence must not decide anything
	// here: the hard requirement belongs to the work item that multiplies by it, which is the
	// only one able to say what it does when the multiplier is missing.
	m.OptionalTotal++
	atrEv := Evidence{Key: EvidenceATR, Status: in.ATR.Status,
		PriceBasis: in.ATR.PriceBasis, Source: evidenceSourceSnapshot}
	if in.ATR.Status.OK() && in.ATR.Value != nil && finitePositive(*in.ATR.Value) {
		m.OptionalPresent++
		atrEv.Status = Available
		v := *in.ATR.Value
		atrEv.Value = &v
	} else if atrEv.Status == Available || atrEv.Status == "" {
		atrEv.Status = Unavailable
	}
	ev = append(ev, atrEv)

	// Adjustment age, from fetcher.LastAdjustmentAge. Also recorded and not gated: EP-1
	// applies no "age > N" rule, and 63 (not 65) is the number when one is eventually
	// applied. See AdjustmentAge.
	m.OptionalTotal++
	ageEv := Evidence{Key: EvidenceAdjustmentAge, Status: Unavailable, Source: evidenceSourceAdjustmentAge,
		Text: "LastAdjustmentAge 無答案(bars 不足 / 比值恆為 1 / 視窗內無事件),不等於「近期沒有除權息」"}
	if in.AdjustmentAge.Known() {
		m.OptionalPresent++
		age := float64(*in.AdjustmentAge.BarsAgo)
		if finite(age) {
			ageEv.Status = Available
			ageEv.Value = &age
			ageEv.Text = ""
		}
	}
	ev = append(ev, ageEv)

	// ── EP-3b: the level evidence, the previous close, and liquidity ──────────────────
	//
	// Emitted BEFORE the reasons are assembled but computed from the same candidate list the
	// zone was actually built from, so the row a reader sees and the number the rule used are
	// the same number by construction rather than by agreement.

	// The policy is computed ONCE and threaded into both steps. Two calls would be two
	// chances for the zone's policy and the chase's policy to be edited apart, and they are
	// two halves of one sentence: SIDEWAYS permits a pullback entry and permits no chase.
	policy := PolicyFor(in.Regime.Regime, in.EntrySemantic.Semantic)

	// ── EP-6D: A CONTRADICTED THESIS COMPUTES NO ENTRY ────────────────────────────────
	//
	// The user's rule: a stock whose primary Action is SELL / REDUCE / TAKE PROFIT / STOP LOSS
	// shows NO entry — no zone, chase ceiling, stop or target, not as a published field and not
	// as a number inside EntryTrace either. Whether a plan is in that case is DecideStatus's S1,
	// and S1 reads only the entry shape and the thesis standing (S0 and S1 are the first two
	// arms, and neither reads a price), so asking the decision layer NOW, with nothing but those
	// two, should give the answer the full call below gives — an argument from the arm order, not
	// a proof. The full call is still made. What is TESTED is agreement ("decided at S1" ⇔ "no
	// step ran") on the inputs TestThePreDecisionAgreesWithTheFullDecision and
	// TestAContradictedShapeWithANonOKStatusIsStillS1WithNoSteps cover: every zone/risk case, a
	// subset of matrix() with an UNKNOWN or absent semantic, zoneBase with the semantic
	// UNAVAILABLE / UNKNOWN / unlabelled (those three kinds under all three standings), and
	// PULLBACK / BREAKOUT under a non-OK status (under CONTRADICTED only). Outside that set the
	// gate's THESIS_CONTRADICTED_PUBLISHES_NO_ENTRY is a backstop in ONE direction only: a plan
	// decided at S1 that still ran a step or carries a price. The other direction — pre-decision
	// says S1, full decision says otherwise — skips the steps, publishes no price and is not S1,
	// so no invariant fires; only the arm-order argument above rules it out.
	//
	// NOT COMPUTED rather than computed-and-stripped. Stripping means every entry-derived number
	// exists in the plan's assembly and every future field that copies one has to remember to be
	// stripped too — which is precisely how the traces leaked the first time. A step that never
	// runs cannot leak. The market OBSERVATIONS the snapshot was handed (current price, ATR,
	// MA20 / MA60 / base low / pivot high, previous close, volume, adjustment age) are evidence
	// rather than recommendations, and stay on the evidence census with their values. The
	// projected valuation TARGET is not a market observation but a strategy target, so since
	// EP-6G its row stays on the census WITHOUT a value (UNAVAILABLE, NOT_SCREENED); the
	// suitability verdict, a category with no number, stays as it is.
	contradicted := DecideStatus(DecisionInput{Semantic: in.EntrySemantic.Semantic,
		Policy: policy, Thesis: in.Thesis}).Rule == RuleThesisContradicted
	var (
		zoneRes   ZoneResult
		chaseRes  ChaseResult
		stopRes   StopResult
		targetRes TargetResult
	)
	if !contradicted {
		zoneRes = ComputeIdealEntryZone(in, policy)
		chaseRes = ComputeMaxChase(in, zoneRes.Zone, policy)

		// EP-4's two steps, threaded the same way and in DEPENDENCY ORDER: the stop is measured
		// against the zone this plan publishes, and the targets against that zone AND that stop.
		// Neither recomputes what the previous step produced, so the numbers a reader sees and the
		// numbers the risk/reward was computed from cannot be edited apart.
		//
		// THE POLICY IS NOT PASSED TO EITHER. A regime decides whether an entry SHAPE is permitted
		// and how far a plan may chase — both are decisions about buying. Where the thesis stops
		// being true and what the trade is worth are properties of the CHART, and a regime-scaled
		// stop would move the published risk of the same setup without anything about the setup
		// having changed. If a later item wants regime-dependent risk, it is a policy change with
		// its own argument.
		stopRes = ComputeInvalidation(in, zoneRes.Zone)
		targetRes = ComputeTargets(in, zoneRes.Zone, stopRes.Invalidation)
	}

	// One row per LevelSource, driven by the REGISTRY rather than by the trace, so the census
	// is the same width for every input even if a step returned early. Each row carries the
	// observed level AND, as Text, the disposition that says what happened to it — "MA20 was
	// 102.4 and above the market" rather than a blank.
	for _, src := range AllLevelSources {
		c := candidateFor(zoneRes.Trace.Candidates, src)
		if contradicted {
			// No zone step ran, so there is no screening to report — but the level was still
			// OBSERVED, and the observation stays: NOT_SCREENED, with the snapshot's own value.
			o := in.observationFor(src)
			c = CandidateLevel{Source: src, Status: Unavailable, Disposition: DispositionNotScreened,
				PriceBasis: o.BasisOr(in.PriceBasis)}
			if o.Usable() {
				c.Status, c.Value = Available, o.Value
			}
		}
		row := Evidence{
			Key:        evidenceKeyForLevel(src),
			Status:     c.Status,
			PriceBasis: c.PriceBasis,
			Text:       string(c.Disposition),
			Source:     evidenceSourceSnapshot,
		}
		if c.Status.OK() && c.Value != nil {
			// COPIED again: the trace holds its own pointer, and two fields of one plan
			// sharing storage is a mutation in one place showing up in another.
			v := *c.Value
			row.Value = &v
		}
		ev = append(ev, row)
	}

	// The previous close. Recorded whether or not the chase used it, because its ABSENCE is
	// the whole explanation for a missing ceiling on an otherwise complete plan.
	prevEv := Evidence{Key: EvidencePreviousClose, Status: in.PreviousClose.Status,
		PriceBasis: in.PreviousClose.BasisOr(in.PriceBasis), Source: evidenceSourceSnapshot}
	if in.PreviousClose.Usable() {
		v := *in.PreviousClose.Value
		prevEv.Status = Available
		prevEv.Value = &v
	} else if prevEv.Status == Available || prevEv.Status == "" {
		prevEv.Status = Unavailable
	}
	ev = append(ev, prevEv)

	// Liquidity. RECORDED AND NEVER GATED — see Snapshot.AvgVolume20. No PriceBasis: a share
	// count is not restated by an adjustment factor. Zero is a legitimate reading (a stock
	// that did not trade), which is why the gate here is "finite and not negative" rather
	// than the finitePositive one every price uses.
	volEv := Evidence{Key: EvidenceAvgVolume20, Status: Unavailable, Source: evidenceSourceSnapshot}
	if in.AvgVolume20 != nil && finite(*in.AvgVolume20) && *in.AvgVolume20 >= 0 {
		v := *in.AvgVolume20
		volEv.Status = Available
		volEv.Value = &v
	}
	ev = append(ev, volEv)

	// ── EP-4: the base low's KIND, and the projected valuation ────────────────────────
	//
	// Three rows, recorded and NOT counted by the confidence census for the reason EP-3b's
	// six are not. Each is emitted for every well-formed snapshot including the ones where
	// it is absent, because the absence is the whole explanation for a missing stop or an
	// unclamped second target.

	// The base low's KIND. A TEXT row: its reading is a category, not a price. UNKNOWN is
	// recorded as INSUFFICIENT_DATA with the word preserved — the producer looked and could
	// not say — while "" is UNAVAILABLE, nobody said. See BaseLowKind and RegimeEvidence for
	// why the two absences are not merged.
	kindEv := Evidence{Key: EvidenceBaseLowKind, Status: Unavailable, Source: evidenceSourceSnapshot,
		Text: "未指明 BaseLow 是「整理平台低點」還是「當日單根最低」;未指明時不作為停損候選"}
	switch {
	case in.BaseLowKind == BaseLowKindUnknown:
		kindEv.Status = InsufficientData
		kindEv.Text = string(BaseLowKindUnknown)
	case KnownBaseLowKind(in.BaseLowKind):
		kindEv.Status = Available
		kindEv.Text = string(in.BaseLowKind)
	}
	ev = append(ev, kindEv)

	// The projected valuation target. A PRICE row, so it carries its basis.
	//
	// EP-6G: ON AN S1 PLAN THE ROW CARRIES NO NUMBER. Unlike the level rows above, this is not
	// a market observation — it is a STRATEGY TARGET (what the trade is worth), and the user's
	// rule is that a valuation target must not re-enter an S1 plan through Target2, another
	// price field, EntryTrace or the serialized JSON. The row is KEPT (the evidence census is
	// the same width for every well-formed input — see AllEvidenceKeys) with status UNAVAILABLE,
	// no Value, and the same NOT_SCREENED text the level rows carry when no step ran, so "no
	// valuation existed" and "a valuation existed but this plan does not screen targets" stay
	// distinguishable without publishing the price.
	valEv := Evidence{Key: EvidenceValuationBaseTarget, Status: in.Valuation.Status,
		PriceBasis: in.Valuation.BasisOr(in.PriceBasis), Source: evidenceSourceSnapshot}
	if contradicted {
		valEv.Status = Unavailable
		valEv.Text = string(DispositionNotScreened)
	} else if in.Valuation.Usable() {
		v := *in.Valuation.BaseTargetPrice
		valEv.Status = Available
		valEv.Value = &v
	} else if valEv.Status == Available || valEv.Status == "" {
		valEv.Status = Unavailable
	}
	ev = append(ev, valEv)

	// The projected suitability verdict. A TEXT row, and the one that decides whether the
	// number above may cap anything. INSUFFICIENT_DATA is carried through as this package's
	// own INSUFFICIENT_DATA status, because it is the same statement one layer up.
	suitEv := Evidence{Key: EvidenceValuationSuitability, Status: Unavailable,
		Source: evidenceSourceSnapshot,
		Text:   "未指明 P/E 目標價對這家公司是否適用;未指明時不作為 Target2 上限"}
	switch {
	case in.Valuation.Suitability == SuitabilityInsufficientData:
		suitEv.Status = InsufficientData
		suitEv.Text = string(SuitabilityInsufficientData)
	case KnownValuationSuitability(in.Valuation.Suitability):
		suitEv.Status = Available
		suitEv.Text = string(in.Valuation.Suitability)
	}
	ev = append(ev, suitEv)

	// ── the reasons the two price steps reached ───────────────────────────────────────
	//
	// Deduplicated (appendReason), because the zone step and the chase step can land on the
	// same underlying cause and a repeated code reads as two independent findings.
	reasons = appendReason(reasons, zoneRes.Trace.Rejection)
	if zoneRes.Trace.OverlapsCurrentPrice {
		// A flag ON A PUBLISHED ZONE, not a refusal. See ReasonPullbackZoneOverlapsCurrentPrice.
		reasons = appendReason(reasons, ReasonPullbackZoneOverlapsCurrentPrice)
	}
	reasons = appendReason(reasons, chaseRes.Trace.Rejection)
	reasons = appendReason(reasons, stopRes.Trace.Rejection)
	reasons = appendReason(reasons, targetRes.Trace.Target1Rejection)
	reasons = appendReason(reasons, targetRes.Trace.Target2Rejection)

	// The adjustment age. A CAVEAT, NEVER A GATE: see recentAdjustment. The prices above were
	// computed before this line and are not touched by it.
	recent := recentAdjustment(in.AdjustmentAge)
	if recent {
		reasons = appendReason(reasons, ReasonRecentPriceAdjustment)
	}

	// ── EP-5: THE DECISION ────────────────────────────────────────────────────────────
	//
	// LAST among the rules, because it consumes every one of them and computes none of them
	// again. The projection below is the whole of what DecideStatus may see, and it is built
	// here rather than inside the decision so that this file — the one that already holds all
	// six prices — is where the narrowing is visible.
	//
	// EACH ABSENT PRICE TRAVELS WITH THE CODE THAT EXPLAINS IT. `Zone == nil` would collapse
	// "the ATR was missing" into "there is nowhere to buy", and `Invalidation == nil` would
	// collapse "no candidate was comparable" into "no legal risk boundary exists" — the two
	// merges that decide between INSUFFICIENT_DATA and NO_VALID_ENTRY.
	//
	// The line that used to be here — an unconditional ENTRY_EVALUATION_NOT_IMPLEMENTED — is
	// gone, and its own doc said this item would delete it. Under EP5-v1 the sentence it
	// asserted is false for every input.
	decision := DecideStatus(DecisionInput{
		Semantic:      in.EntrySemantic.Semantic,
		Policy:        policy,
		CurrentPrice:  currentPriceFor(in),
		Zone:          zoneRes.Zone,
		ZoneAbsence:   zoneRes.Trace.Rejection,
		MaxChasePrice: chaseRes.MaxChasePrice,
		ChaseAbsence:  chaseRes.Trace.Rejection,
		Invalidation:  stopRes.Invalidation,
		StopAbsence:   stopRes.Trace.Rejection,
		// EP-6D. Passed VERBATIM — "" stays "" — so the decision, not this line, is where an
		// unclassified or contradicted standing is answered. Deliberately NOT an evidence row and
		// NOT on the confidence census: it gates entries and grades nothing. It is recorded on the
		// decision trace below, which is what makes an archived verdict re-derivable.
		Thesis: in.Thesis,
	})
	p.Status = decision.Status
	for _, r := range decision.Reasons {
		reasons = appendReason(reasons, r)
	}

	p.Reasons = reasons
	p.Evidence = ev
	p.Confidence = ClassifyConfidence(m, true)

	// ── EP-6D: A CONTRADICTED THESIS PUBLISHES NO PRICE ───────────────────────────────
	//
	// For a plan answered at S1 the steps never ran (see above), so the six executable fields
	// and the four step traces are empty by construction. The assignment below is guarded
	// anyway, so the published fields cannot disagree with the decision even if the pre-decision
	// and the full decision were ever edited apart; the gate then checks the result
	// (InvariantThesisContradictedPublishesNoEntry).
	if decision.Rule != RuleThesisContradicted {
		p.IdealEntry = zoneRes.Zone
		p.MaxChasePrice = chaseRes.MaxChasePrice
		p.Invalidation = stopRes.Invalidation
		p.Target1 = targetRes.Target1
		p.Target2 = targetRes.Target2
		p.RiskReward = targetRes.RiskReward
	}
	p.EntryTrace = &EntryTrace{
		RuleVersion:       RuleVersion,
		AdjustmentAgeBars: copyBars(in.AdjustmentAge),
		RecentAdjustment:  recent,
		Policy:            policyTrace(policy),
		Zone:              zoneRes.Trace,
		Chase:             chaseRes.Trace,
		Stop:              stopRes.Trace,
		Targets:           targetRes.Trace,
		Decision:          decisionTrace(in, policy, zoneRes, chaseRes, stopRes, decision),
	}
	if contradicted {
		// No step ran, so there is no absence to explain and no reading of one: leaving
		// decisionTrace's "" codes and UNCLASSIFIED readings would describe evidence gaps
		// that never happened.
		d := &p.EntryTrace.Decision
		d.ZoneAbsence, d.ZoneReading, d.ChaseAbsence, d.ChaseReading = "", "", "", ""
		d.StopAbsence, d.StopReading = "", ""
	}
	if contradicted {
		// The price caveats describe numbers this plan does not publish, so they are not
		// rendered; the one caveat that applies says why there are none.
		p.Caveats = planCaveats(basis, recent, ZoneResult{}, ChaseResult{}, StopResult{}, TargetResult{})
		p.Caveats = append(p.Caveats, "掃描器的主要建議(Action)為賣出/減碼/停利/停損,與進場想法矛盾:"+
			"本計畫判定 NO_VALID_ENTRY,且不公布任何進場區間、追價上限、失效價或目標價")
	} else {
		p.Caveats = planCaveats(basis, recent, zoneRes, chaseRes, stopRes, targetRes)
	}

	// ── the production invariant gate ─────────────────────────────────────────────────
	//
	// LAST, on the finished plan, because the invariants are relations BETWEEN fields
	// (MaxChasePrice >= IdealEntry.High) and there is no finished plan to relate them on
	// until here. EP-3a's checker plus EP-3b's decision about what to do when it fires: see
	// EnforcePlanInvariants. The outcome is stamped on the trace, so removing this call
	// turns a test red instead of silently removing a guard.
	p, _ = EnforcePlanInvariants(p)
	return p
}

// candidateFor finds one source's row in a candidate list.
//
// It returns a NOT_SCREENED row built from nothing when the source is absent from the list,
// which no step in this package produces — every return path in ComputeIdealEntryZone sets the full
// candidate list. The fallback exists so the evidence census stays the width of the REGISTRY
// rather than the width of whatever the last step happened to return: a census that can
// shrink is a census a later item can shrink by accident, and a missing row reads downstream
// as "the rule never looked at the MA60".
func candidateFor(cands []CandidateLevel, src LevelSource) CandidateLevel {
	for _, c := range cands {
		if c.Source == src {
			return c
		}
	}
	return CandidateLevel{Source: src, Status: Unavailable, Disposition: DispositionNotScreened}
}

// adjustmentAgeCaveatBars is the age BELOW WHICH a corporate-action adjustment is reported as
// recent. SIXTY-THREE, and it is a REPORTING threshold, not a gate.
//
// Why 63 and not 65: AdjustmentAge's doc has the derivation. An ex-dividend restates every
// close before the event bar by one ratio, so exactly ONE delta mixes bases, its age is what
// fetcher.LastAdjustmentAge returns, and the delta-age threshold for a Wilder ATR(14) is
// ((14-1)/14)^63 < 1%. 65 is the RSI answer for an isolated bump to one close — a real number
// for a different question.
//
// # NOTHING BECOMES CLEAN ON DAY 63
//
// The residue decays CONTINUOUSLY, as ((N-1)/N)^age. At 62 bars it is 1.03% of the event and
// at 63 it is 0.96%: the threshold is where a report stops mentioning it, and treating it as
// the day the number becomes trustworthy would be reading a smooth curve as a cliff. That is
// also why this is not a gate — suppressing a plan over a residue of a few tenths of a
// percent would delete a usable entry to avoid a rounding difference.
const adjustmentAgeCaveatBars = 63

// recentAdjustment reports whether a corporate-action adjustment is recent enough to mention.
//
// An UNKNOWN age is NOT recent and is NOT clean: fetcher.LastAdjustmentAge answers ok == false
// for a series too short to test, for a ratio that never moves, and for a window with no event
// in it, and only the last of those means "no recent adjustment". The plan says so on the
// ADJUSTMENT_AGE evidence row rather than resolving it here, because this function cannot tell
// the three apart and neither can its caller.
func recentAdjustment(a AdjustmentAge) bool {
	return a.Known() && *a.BarsAgo < adjustmentAgeCaveatBars
}

// copyBars copies the adjustment age onto the heap, so the trace cannot alias the caller's int.
func copyBars(a AdjustmentAge) *int {
	if a.BarsAgo == nil {
		return nil
	}
	v := *a.BarsAgo
	return &v
}

// policyTrace copies a PolicyResult onto the plan.
//
// COPIED, not referenced: PolicyResult carries a *EntryPolicy and a plan holding that pointer
// would change value if a caller wrote through it — the aliasing failure
// TestComputePlanIsPureAndDoesNotAliasItsInput exists to catch, one struct deeper.
func policyTrace(policy PolicyResult) PolicyTrace {
	out := PolicyTrace{SemanticAllowance: policy.Allowance}
	if len(policy.Requirements) != 0 {
		out.Requirements = make([]Requirement, len(policy.Requirements))
		copy(out.Requirements, policy.Requirements)
	}
	if policy.Policy != nil {
		out.Name = policy.Policy.Name
		out.ChaseAllowance = policy.Policy.Chase.Allowance
		if policy.Policy.Chase.MaxATR != nil {
			out.ChaseMaxATR = copyFloat(*policy.Policy.Chase.MaxATR)
		}
	}
	return out
}

// planCaveats is the PRESENTATION half of the plan: prose, in the caller's language, never
// parsed and never a decision input.
//
// Every caveat here has a machine-readable partner — a Reason code or a trace field — because
// a limitation that exists only as prose is a limitation no consumer can act on. The prose
// exists because a human reader must not have to open this source to find out what the plan
// assumed.
func planCaveats(basis PriceBasis, recent bool, zone ZoneResult, chase ChaseResult,
	stop StopResult, targets TargetResult) []string {
	out := []string{"EP-5 在 EP-4 的「理想進場區間」、「最高追價」、「想法失效價」與兩個目標價之上," +
		"依固定順序的決策表判定進場狀態(BUY_NOW / WAIT_PULLBACK / WAIT_BREAKOUT / TOO_EXTENDED / " +
		"NO_VALID_ENTRY / INSUFFICIENT_DATA);它只比較既有價位,不重新推導任何價格"}

	if recent {
		out = append(out, "近 63 個交易日內曾發生除權息或減資調整,ATR 與均線仍殘留該事件的影響。"+
			"殘留是 ((N-1)/N)^age 的連續衰減,63 天不是乾淨的分界,只是殘留低於 1% 的位置")
	}
	if zone.Zone != nil && basis != PriceBasisRaw {
		out = append(out, "本計畫的價位以 "+string(basis)+" 序列計算,不是委託單上的價格;"+
			"漲跌幅上限也因此無法計算(規則以交易所的原始參考價為準)")
	}
	if zone.Trace.OverlapsCurrentPrice {
		out = append(out, "拉回區間的上緣已在現價之上或與現價相同:區間仍以支撐為中心、未被裁切,"+
			"「現在能不能買」屬於進場狀態判斷,本階段不做")
	}
	if chase.Trace.LimitUp != nil {
		// The pricerule caller contract, in full. It cannot be verified by anything in this
		// process — there is no calendar and no corporate-action source here — so it is
		// stated as an assumption rather than implied by a number.
		out = append(out, "漲停夾限假設今天是這檔股票的「普通交易日」:非除權息、除權、減資換發日"+
			"(那些日子的漲跌幅是以除權息參考價計算,不是前一日收盤價),也不在新上市或恢復交易後"+
			"的前五個交易日內(該區間沒有漲跌幅限制)")
	}
	if zone.Trace.HalfWidthBinding == BindingTickFloor {
		out = append(out, "這檔股票的 0.5 倍 ATR 比兩檔跳動還小,區間寬度改由跳動下限決定,"+
			"不是由波動度決定")
	}

	// ── EP-4's caveats. Each has a machine-readable partner on the trace or in the reason
	// list; the prose exists so a human reader does not have to open this source.
	if stop.Trace.SelectedSource == StopSourceZoneATRBuffer {
		out = append(out, "「想法失效價」不是觀察到的結構價位,而是由區間下緣往下 0.5 倍 ATR 推得"+
			"(等於支撐往下一整個 ATR / 突破點往下半個 ATR):在沒有可用的整理平台低點或月線時才使用,"+
			"0.5 這個倍數是啟發式,沒有回測支撐,也不宣稱是最佳值")
	}
	if stop.Trace.BaseLowKind == BaseLowLatestBar || stop.Trace.BaseLowKind == BaseLowKindUnknown {
		out = append(out, "BASE_LOW 這一列不是整理平台低點(NO_BASE 時掃描器放的是當日單根最低),"+
			"因此沒有被當成結構性停損候選;它仍可以是進場區間的中心,兩者問的不是同一個問題")
	}
	if targets.Trace.Target1Binding == Target1FromRMultiple && targets.Target1 != nil {
		out = append(out, "第一目標價只由風險倍數推得(進場上緣 + 2R),上方沒有可用的結構壓力"+
			"(突破點/60 日高)可以夾限;它不是觀察到的壓力位置")
	}
	if targets.Trace.ValuationCeiling == ValuationCeilingApplied {
		out = append(out, "第二目標價被本益比 BASE 情境的目標價夾限,不是本套件的 3.5R 算術結果;"+
			"估值模型的適用性判定(SUITABLE / CONDITIONAL / WEAK)一併記錄在證據列上")
	}
	if targets.Trace.RiskRejection != "" {
		out = append(out, "無法建立風險基準(進場上緣 − 失效價),因此不計算目標價與風險報酬比;"+
			"這裡不會用 ATR 或固定百分比代替風險基準")
	}
	return out
}

// currentPriceFor copies the snapshot's current price onto the heap, or nil when there is none.
//
// COPIED for the reason every number this file publishes is copied: a decision holding the
// caller's pointer would change value if the caller wrote through it, and a verdict that changes
// after it was reached is not a verdict.
//
// The USABILITY gate is PriceObservation.Usable, the same one the evidence census applies, so
// "the CURRENT_PRICE row says UNAVAILABLE" and "the decision saw no price" cannot disagree.
func currentPriceFor(in Snapshot) *float64 {
	if !in.CurrentPrice.Usable() {
		return nil
	}
	return copyFloat(*in.CurrentPrice.Value)
}

// decisionTrace records what EP-5 was handed and what it answered.
//
// The READINGS are recorded next to the codes, not derived by a reader: ReadAbsence is what
// turns NO_VALID_INVALIDATION into a verdict and INVALIDATION_EVIDENCE_UNAVAILABLE into a state,
// and a trace that showed only the nil pointer could explain neither.
func decisionTrace(in Snapshot, policy PolicyResult, zone ZoneResult, chase ChaseResult,
	stop StopResult, d DecisionResult) DecisionTrace {
	tr := DecisionTrace{
		Semantic:      in.EntrySemantic.Semantic,
		CurrentPrice:  currentPriceFor(in),
		Rule:          d.Rule,
		Status:        d.Status,
		PolicyDecided: policy.Allowance,
		Thesis:        in.Thesis,
	}
	if len(d.Reasons) != 0 {
		tr.Reasons = make([]Reason, len(d.Reasons))
		copy(tr.Reasons, d.Reasons)
	}
	if zone.Zone == nil {
		tr.ZoneAbsence, tr.ZoneReading = zone.Trace.Rejection, ReadAbsence(zone.Trace.Rejection)
	}
	if chase.MaxChasePrice == nil {
		tr.ChaseAbsence, tr.ChaseReading = chase.Trace.Rejection, ReadAbsence(chase.Trace.Rejection)
	}
	if stop.Invalidation == nil {
		tr.StopAbsence, tr.StopReading = stop.Trace.Rejection, ReadAbsence(stop.Trace.Rejection)
	}
	return tr
}
