package entryplan

// ── EP-3b: the levels an entry zone may be centred on ─────────────────────────────────
//
// This file answers one question and computes no zone:
//
//	which observed level is THE CENTRE of the entry zone, and for every level that is not,
//	WHY not.
//
// It is deliberately three steps rather than one, because the failure mode being designed
// against is the `if` chain: a single function that walks MA20, then MA60, then the base low,
// then gives up, cannot be tested on its selection rule without also being handed a whole
// snapshot, and the reason a candidate lost is thrown away the moment the next `else if`
// runs. So:
//
//	CandidateLevelsFor    which sources are candidates at all, and what value each carries
//	ScreenCandidateLevels which of them a zone may legally be centred on, and why not
//	SelectCenter          which one wins
//
// Each is pure, total and separately testable, and every candidate keeps its DISPOSITION —
// the code that says what happened to it — so an archived plan can be read back as "MA20 was
// 102.4 and it was above the market", not as "MA20: (blank)".

// LevelSource names WHERE a candidate level came from.
//
// A TYPE with a registry, not a free string. "MA20", "ma20", "20MA" and "MA-20" are four
// spellings of one level, and a stored plan spelled the fourth way joins back to nothing —
// the same argument reasons.go makes for Reason and policy.go for PolicyName. The registry is
// checked from BOTH ends by TestTheLevelVocabulariesAreExactlyWhatProductionEmits: every
// source production can attach is a member, and every member is attached by some input. The
// VALUES are pinned as literals by golden_test.go.
type LevelSource string

const (
	// LevelMA20 — the 20-session moving average. The nearest trend support in the repo's own
	// watchlist wording (「拉回 5/10 日線量縮承接」 is the same idea one timeframe faster).
	LevelMA20 LevelSource = "MA20"
	// LevelMA60 — the 60-session moving average. The slower support, and the one that is
	// still there when a pullback runs deeper than the MA20.
	LevelMA60 LevelSource = "MA60"
	// LevelBaseLow — the low of the consolidation base the current move started from. A
	// STRUCTURAL level rather than a computed one: it is where supply last dried up.
	LevelBaseLow LevelSource = "BASE_LOW"
	// LevelBreakoutHigh — the pivot high a breakout entry is measured from. The ONLY level a
	// breakout zone may be centred on; see SelectCenter.
	LevelBreakoutHigh LevelSource = "BREAKOUT_PIVOT"
)

// AllLevelSources is every source this package can name, in the order candidates are
// reported. Declared rather than derived, because Go cannot enumerate constants.
//
// The ORDER is load-bearing in exactly one place — it breaks a tie between two eligible
// pullback candidates that hold the same price (see SelectCenter) — and it is otherwise only
// the order the evidence rows are written in, which must be stable because an archived plan is
// diffed byte for byte.
var AllLevelSources = []LevelSource{
	LevelMA20,
	LevelMA60,
	LevelBaseLow,
	LevelBreakoutHigh,
}

// KnownLevelSource reports whether a source is in the registry.
func KnownLevelSource(s LevelSource) bool {
	for _, k := range AllLevelSources {
		if k == s {
			return true
		}
	}
	return false
}

// Valid guards against a source string no producer can emit (e.g. decoded from an older
// archive). The REGISTRY decides, not a hand-written disjunction — one list, nothing to drift.
func (s LevelSource) Valid() bool { return KnownLevelSource(s) }

// SupportsSemantic reports whether a source is a candidate for one entry semantic.
//
// This is the whole membership rule and it is stated ONCE. A pullback is a retracement INTO
// support, so its candidates are supports; a breakout is a move THROUGH resistance, so its
// candidate is the pivot being cleared. Mixing them would centre a pullback zone on the level
// price has to break to go up, which is not a pullback.
//
// An unresolved semantic supports nothing: there is no entry shape to be a candidate FOR.
func (s LevelSource) SupportsSemantic(sem EntrySemantic) bool {
	switch sem {
	case EntrySemanticPullback:
		return s == LevelMA20 || s == LevelMA60 || s == LevelBaseLow
	case EntrySemanticBreakout:
		return s == LevelBreakoutHigh
	}
	return false
}

// belowCurrentApplies reports whether the "strictly below the market" screen applies to this
// source.
//
// It applies to SUPPORTS and not to the breakout pivot, and the asymmetry is not an oversight:
//
//   - A support at or above the last price is not a pullback level. Centring a "pullback" zone
//     there prices an entry the market has not come back to, and calling it a retracement is
//     false — the entry is at or above the market, which is a chase.
//   - A pivot high BELOW the last price is a level price has already cleared. That is a real
//     and common state ("the breakout already happened"), and what to do about it — buy the
//     retest, or call it TOO_EXTENDED — is a STATUS decision, which is EP-5's and not this
//     file's. Screening it out here would delete the evidence EP-5 needs in order to make it.
//
// Note what does NOT live here: internal/technical/pivot.go's classic S1/S2/S3. Those are
// INTRADAY pivots derived from one session's high/low/close, and this package plans entries on
// a 1–4 week horizon. Mixing the two timescales would put a day-trade support inside a swing
// zone, and the numbers would look perfectly plausible.
func (s LevelSource) belowCurrentApplies() bool {
	return s == LevelMA20 || s == LevelMA60 || s == LevelBaseLow
}

// LevelDisposition is what happened to one candidate: a stable code, persisted on the evidence
// row and in the plan's trace.
//
// Every candidate always carries one. The alternative — record only the winner — is what makes
// an archived plan unreadable: "the zone is centred on the MA60" does not say whether the MA20
// was missing, was on the wrong price basis, or was simply above the market, and those three
// call for three different actions.
type LevelDisposition string

const (
	// DispositionSelected — this level is the zone's centre.
	DispositionSelected LevelDisposition = "SELECTED"
	// DispositionEligibleNotSelected — a legal candidate that lost the selection rule. It
	// is NOT a rejection, and keeping it distinct is what lets a reader see that a deeper
	// support existed and was passed over deliberately.
	DispositionEligibleNotSelected LevelDisposition = "ELIGIBLE_NOT_SELECTED"
	// DispositionLevelUnavailable — no usable number: not AVAILABLE, absent, non-finite, or
	// not positive. MISSING ≠ ZERO: a 0 here would be a support price nothing trades at.
	DispositionLevelUnavailable LevelDisposition = "LEVEL_UNAVAILABLE"
	// DispositionBasisMismatch — the level is on a different price series from the quote the
	// zone is being built against. See PriceBasis: this package never converts, so a level
	// measured on the adjusted close cannot be compared with a raw last price.
	DispositionBasisMismatch LevelDisposition = "PRICE_BASIS_MISMATCH"
	// DispositionNotBelowCurrent — a support at or above the last price. See
	// belowCurrentApplies.
	DispositionNotBelowCurrent LevelDisposition = "NOT_BELOW_CURRENT_PRICE"
	// DispositionWrongSemantic — a real level that is not a candidate for THIS entry shape.
	// Reported rather than dropped, so the census stays the same width for every input and a
	// missing row can never be read as "the rule did not look".
	DispositionWrongSemantic LevelDisposition = "NOT_A_CANDIDATE_FOR_SEMANTIC"
	// DispositionNotScreened — the screen never ran: there was no usable quote or no usable
	// price basis to screen against.
	//
	// A STATE, not a verdict, and the same distinction EntryStatus draws between
	// INSUFFICIENT_DATA and NO_VALID_ENTRY. "The MA20 is not a legal centre" and "we could
	// not check whether the MA20 is a legal centre" are different sentences.
	DispositionNotScreened LevelDisposition = "NOT_SCREENED"
)

// AllLevelDispositions is every disposition this package can attach. Kept honest from both
// ends by the level tests, for the reason AllReasons is.
var AllLevelDispositions = []LevelDisposition{
	DispositionSelected,
	DispositionEligibleNotSelected,
	DispositionLevelUnavailable,
	DispositionBasisMismatch,
	DispositionNotBelowCurrent,
	DispositionWrongSemantic,
	DispositionNotScreened,
}

// KnownLevelDisposition reports whether a disposition is in the registry.
func KnownLevelDisposition(d LevelDisposition) bool {
	for _, k := range AllLevelDispositions {
		if k == d {
			return true
		}
	}
	return false
}

// Eligible reports whether a screened candidate is one a zone may be centred on.
func (d LevelDisposition) Eligible() bool {
	return d == DispositionEligibleNotSelected || d == DispositionSelected
}

// CandidateLevel is one level, its value, and what happened to it.
//
// Value is a POINTER and Status is its own availability, so that "the MA20 was 94.0 and lost"
// and "there was no MA20" are different rows rather than the same 0.
type CandidateLevel struct {
	Source LevelSource `json:"source"`
	// Status is the level evidence's own availability, downgraded to UNAVAILABLE when the
	// caller labelled it AVAILABLE and it is not a price. The label is not trusted.
	Status Availability `json:"status"`
	// Value is the observed level, COPIED out of the snapshot. Nil unless Status is
	// AVAILABLE.
	Value *float64 `json:"value,omitempty"`
	// PriceBasis is the basis the level was measured on, already resolved through
	// PriceObservation.BasisOr — so an empty basis here means the snapshot did not declare
	// one either, never "RAW".
	PriceBasis PriceBasis `json:"price_basis,omitempty"`
	// Disposition is what happened to this candidate. Never empty.
	Disposition LevelDisposition `json:"disposition"`
}

// value reports the candidate's number, if it has one.
func (c CandidateLevel) value() (float64, bool) {
	if c.Status.OK() && c.Value != nil && finitePositive(*c.Value) {
		return *c.Value, true
	}
	return 0, false
}

// CandidateLevelsFor collects one row per LevelSource for one entry semantic.
//
// ALWAYS the same width, in AllLevelSources order, whatever the semantic and whatever is
// missing. A variable-width candidate list is how a reader loses the ability to tell "this
// rule considered the base low and rejected it" from "this version of the rule had never
// heard of the base low", and the two are a rule change apart.
//
// It DECIDES NOTHING beyond membership: a row is either NOT_A_CANDIDATE_FOR_SEMANTIC, or it
// is carried forward with no disposition yet for ScreenCandidateLevels to fill in. It reads
// no price basis, performs no comparison, and copies every float out of the snapshot so a
// caller mutating its own numbers afterwards cannot change an already-returned candidate.
func CandidateLevelsFor(in Snapshot, semantic EntrySemantic) []CandidateLevel {
	out := make([]CandidateLevel, 0, len(AllLevelSources))
	for _, src := range AllLevelSources {
		obs := in.observationFor(src)
		c := CandidateLevel{
			Source:     src,
			Status:     obs.Status,
			PriceBasis: obs.BasisOr(in.PriceBasis),
		}
		if obs.Usable() {
			// COPIED, never aliased.
			v := *obs.Value
			c.Status = Available
			c.Value = &v
		} else if c.Status == Available || c.Status == "" {
			// Labelled available and not a price, or never labelled at all. The label is
			// not trusted and the value is dropped rather than published — the same
			// treatment ComputePlan gives CURRENT_PRICE.
			c.Status = Unavailable
		}
		if !src.SupportsSemantic(semantic) {
			c.Disposition = DispositionWrongSemantic
		}
		out = append(out, c)
	}
	return out
}

// observationFor maps a source to the snapshot field it reads.
//
// The ONE place the mapping lives. A second copy — in the screen, in the trace, in a report —
// is a second chance to read the MA60 into the MA20's row, which no test on the numbers would
// catch because both are plausible prices.
func (s Snapshot) observationFor(src LevelSource) PriceObservation {
	switch src {
	case LevelMA20:
		return s.MA20
	case LevelMA60:
		return s.MA60
	case LevelBaseLow:
		return s.BaseLow
	case LevelBreakoutHigh:
		return s.PivotHigh
	}
	// Not a source. No field to read, and no fallback: a source string this package does not
	// know is not a level with missing data, it is a decoded typo.
	return PriceObservation{}
}

// ScreenCandidateLevels decides which candidates a zone may legally be centred on.
//
// The conditions are ALL of: available, finite, strictly positive, on the SAME price basis as
// the quote, and — for a support — strictly below the last price. Every one of them is a
// separate disposition code, so a rejection says which condition failed.
//
// PURE: it returns a new slice and does not write through the input's pointers.
//
// A candidate that survives is marked ELIGIBLE_NOT_SELECTED, not SELECTED. Selection is
// SelectCenter's job, and a screen that also selected would make "the centre is the closest
// support" untestable without a snapshot.
//
// # When the screen cannot run
//
// It needs a quote and a basis to screen against. Without either, every candidate is
// NOT_SCREENED — not rejected — because the conditions were never evaluated. Callers must not
// read that as "no legal level": ComputePlan already reports the missing quote or basis as its
// own reason, and reporting it a second time as a level failure would send a reader looking at
// the MA20.
func ScreenCandidateLevels(cands []CandidateLevel, quote PriceObservation, snapshotBasis PriceBasis) []CandidateLevel {
	out := make([]CandidateLevel, len(cands))
	copy(out, cands)

	execBasis := quote.BasisOr(snapshotBasis)
	screenable := quote.Usable() && execBasis.Supported()

	for n := range out {
		c := &out[n]
		if c.Disposition == DispositionWrongSemantic {
			// Not a candidate for this entry shape. Nothing to screen.
			continue
		}
		if !screenable {
			c.Disposition = DispositionNotScreened
			continue
		}
		v, ok := c.value()
		if !ok {
			c.Disposition = DispositionLevelUnavailable
			continue
		}
		if c.PriceBasis != execBasis {
			// NEVER CONVERTED. See PriceBasis: converting needs the adjustment factors,
			// which belong to the fetcher, and a plausible conversion here would be a
			// fabricated price that no downstream check could distinguish from a measured
			// one. An unsupported basis on the candidate lands here too, because it is not
			// equal to a supported execution basis.
			c.Disposition = DispositionBasisMismatch
			continue
		}
		if c.Source.belowCurrentApplies() && v >= *quote.Value {
			c.Disposition = DispositionNotBelowCurrent
			continue
		}
		c.Disposition = DispositionEligibleNotSelected
	}
	return out
}

// SelectCenter picks the level the zone is centred on, and marks it SELECTED.
//
// It returns a NEW slice — the screened rows with at most one disposition changed — and the
// index of the winner, so a caller can report the whole candidate list including the losers.
// ok == false means no candidate was eligible.
//
// # PULLBACK: the MAXIMUM eligible level, and never anything else
//
// The centre is the HIGHEST eligible support, i.e. the one NEAREST the market. That is the
// support price is coming into first, so it is the level a retracement actually tests; the
// deeper ones are the next lines of defence, not the entry.
//
// min / mean / median / a weighted average are all wrong and are wrong in the same way: they
// return a number that IS NOT A LEVEL. The average of the MA20 and the MA60 is not a place
// anyone defends, and a zone centred there is centred on an artefact of arithmetic — while
// still looking exactly like a support-based entry in the output.
//
// A TIE (two eligible supports at the same price) resolves to the earlier source in
// AllLevelSources. It is a real possibility — a flat 20/60 crossover — and the tie-break is
// stated so the answer is deterministic rather than dependent on iteration order.
//
// # BREAKOUT: the pivot high, and it may not be substituted
//
// The only legal breakout centre is LevelBreakoutHigh. If the pivot is not eligible there is
// NO breakout zone: not the current price, not the MA20, not the last close. Each of those
// substitutions produces a "breakout entry" at a price nothing broke out of, and the plan
// would then carry a chase limit measured from a level that does not exist.
//
// The guard below refuses even a non-pivot candidate that arrives ELIGIBLE, which cannot
// happen through CandidateLevelsFor (membership already excludes the supports under BREAKOUT).
// It is here because "the breakout centre can only be the pivot" is a claim, and a claim that
// depends on another function's behaviour is only as strong as that function — this makes it
// true of THIS function, and TestBreakoutRefusesANonPivotCentre plants exactly that input.
func SelectCenter(screened []CandidateLevel, semantic EntrySemantic) ([]CandidateLevel, int, bool) {
	out := make([]CandidateLevel, len(screened))
	copy(out, screened)

	best := -1
	for n := range out {
		if !out[n].Disposition.Eligible() {
			continue
		}
		v, ok := out[n].value()
		if !ok {
			// An eligible row with no number is a screen bug, not a centre. Refused rather
			// than dereferenced.
			return out, -1, false
		}
		switch semantic {
		case EntrySemanticBreakout:
			if out[n].Source != LevelBreakoutHigh {
				return out, -1, false
			}
			if best >= 0 {
				// Two eligible breakout centres is not a tie to be broken, it is a
				// contradiction: there is one pivot.
				return out, -1, false
			}
			best = n
		case EntrySemanticPullback:
			if best < 0 {
				best = n
				continue
			}
			bv, okBest := out[best].value()
			if !okBest {
				return out, -1, false
			}
			// STRICTLY greater, so an equal price leaves the earlier source in place —
			// the tie-break stated in the doc.
			if v > bv {
				best = n
			}
		default:
			// No entry shape to select FOR. Not a refusal of the levels; there is no
			// question. See EntrySemantic.Resolved.
			return out, -1, false
		}
	}
	if best < 0 {
		return out, -1, false
	}
	out[best].Disposition = DispositionSelected
	return out, best, true
}

// levelRejectionReason maps a screened candidate list with no eligible member onto the ONE
// reason that best explains it.
//
// The rule: report the STAGE THE CANDIDATES GOT FURTHEST THROUGH. A set where one support was
// present, on the right basis and merely above the market is a different situation from one
// where no support existed at all, and the first is the more informative sentence — the data
// is there and the market has not come back yet. Every candidate's own disposition is on its
// evidence row either way, so nothing is lost; this only decides which sentence goes on the
// plan.
//
// Deterministic, and it never returns "" for a resolved semantic: a candidate list with no
// eligible member always has a reason.
func levelRejectionReason(cands []CandidateLevel, semantic EntrySemantic) Reason {
	const (
		stageNone = iota
		stageUnavailable
		stageBasis
		stageNotBelow
	)
	stage := stageNone
	for _, c := range cands {
		if !c.Source.SupportsSemantic(semantic) {
			continue
		}
		switch c.Disposition {
		case DispositionLevelUnavailable:
			if stage < stageUnavailable {
				stage = stageUnavailable
			}
		case DispositionBasisMismatch:
			if stage < stageBasis {
				stage = stageBasis
			}
		case DispositionNotBelowCurrent:
			if stage < stageNotBelow {
				stage = stageNotBelow
			}
		}
	}

	switch semantic {
	case EntrySemanticPullback:
		switch stage {
		case stageNotBelow:
			return ReasonPullbackLevelNotBelowCurrentPrice
		case stageBasis:
			return ReasonPullbackLevelsBasisMismatch
		default:
			return ReasonPullbackLevelsUnavailable
		}
	case EntrySemanticBreakout:
		if stage == stageBasis {
			return ReasonBreakoutLevelBasisMismatch
		}
		// NOT_BELOW_CURRENT cannot arise for the pivot — the screen does not apply it to a
		// resistance level — so every other outcome is "there is no pivot to measure from".
		return ReasonBreakoutLevelUnavailable
	}
	return ReasonEntrySemanticUnavailable
}
