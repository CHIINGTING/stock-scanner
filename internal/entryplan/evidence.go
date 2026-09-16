package entryplan

// ── the evidence key registry ─────────────────────────────────────────────────────────
//
// Every key ComputePlan can put on a census row, in the order it emits them. The six base
// keys are declared in plan.go, next to the code that fills them; this file adds EP-3b's and
// then names the WHOLE set in one place, because an evidence key is a persisted string and a
// persisted string with no registry is a wire format nobody can enumerate.
//
// Kept honest from BOTH ends by TestEvidenceKeyRegistryIsExactlyWhatProductionEmits (and by
// TestTheTestsCensusListIsProductionsRegistry, which holds the census test's own list against
// this one), and the VALUES are pinned as literals by golden_test.go. The literal golden is the half that
// matters: the suite could previously rename a key and every reference with it and stay green,
// which is exactly the change that breaks a reader of an archived row.

const (
	// EvidenceLevelMA20 / MA60 / BaseLow / PivotHigh are the level evidence a zone can be
	// centred on. Each row carries the OBSERVED value and, as Text, the LevelDisposition
	// that says what happened to it — so a reader sees "MA20 was 102.4, above the market",
	// not a blank.
	EvidenceLevelMA20      = "LEVEL_MA20"
	EvidenceLevelMA60      = "LEVEL_MA60"
	EvidenceLevelBaseLow   = "LEVEL_BASE_LOW"
	EvidenceLevelPivotHigh = "LEVEL_BREAKOUT_PIVOT"

	// EvidencePreviousClose is what the daily price limit is computed FROM. Never the
	// current price; see Snapshot.PreviousClose.
	EvidencePreviousClose = "PREVIOUS_CLOSE"

	// EvidenceAvgVolume20 is liquidity, RECORDED AND NEVER GATED. See Snapshot.AvgVolume20:
	// there is no backtested "minimum volume at which a chase fills" contract in this repo,
	// so a threshold invented here would be a fabricated tradeability claim.
	EvidenceAvgVolume20 = "AVG_VOLUME_20"

	// ── EP-4's three rows ─────────────────────────────────────────────────────────────
	//
	// Also RECORDED AND NOT COUNTED BY THE CONFIDENCE CENSUS, for the reason EP-3b's six
	// are not: the grade's scale is already archived, and moving it because a rule item was
	// added produces a grade nobody can compare across two dates. See ComputePlan.

	// EvidenceBaseLowKind is WHAT KIND of level the BASE_LOW row holds — a consolidation
	// base low or today's single-bar low. A TEXT row: its reading is a category.
	//
	// It is its own row rather than extra text on BASE_LOW because that row's Text already
	// carries the level's LevelDisposition, and two categories in one string is a field a
	// consumer has to parse. The distinction it records is the one that decides whether the
	// base low may be an invalidation at all; see BaseLowKind.
	EvidenceBaseLowKind = "BASE_LOW_KIND"

	// EvidenceValuationBaseTarget is the projected BASE-scenario target price the Target2
	// ceiling is taken from. Recorded whether or not the ceiling bit, because its ABSENCE is
	// the whole explanation for an unclamped 3.5R second target.
	EvidenceValuationBaseTarget = "VALUATION_BASE_TARGET"

	// EvidenceValuationSuitability is the projected model-applicability verdict. A TEXT row,
	// and the one that decides whether the target price above is allowed to cap anything;
	// see ValuationSuitability.
	EvidenceValuationSuitability = "VALUATION_SUITABILITY"
)

// AllEvidenceKeys is every key ComputePlan emits, in emission order.
//
// The ORDER is part of the contract, for the reason plan.go gives for putting PRICE_BASIS
// first: it decides what the other price evidence means, and a basis row arriving after the
// price it labels leaves a reader of an archived plan interpreting the price before it knows
// the series.
var AllEvidenceKeys = []string{
	// required (the confidence census counts exactly these four)
	EvidencePriceBasis,
	EvidenceCurrentPrice,
	EvidenceMarketRegime,
	EvidenceEntrySemantic,
	// corroborating (the census counts exactly these two)
	EvidenceATR,
	EvidenceAdjustmentAge,
	// EP-3b inputs. RECORDED, AND NOT COUNTED BY THE CONFIDENCE CENSUS — see ComputePlan's
	// census section for why the grade was deliberately left where EP-2 put it.
	EvidenceLevelMA20,
	EvidenceLevelMA60,
	EvidenceLevelBaseLow,
	EvidenceLevelPivotHigh,
	EvidencePreviousClose,
	EvidenceAvgVolume20,
	// EP-4 inputs. RECORDED, AND NOT COUNTED BY THE CONFIDENCE CENSUS either.
	EvidenceBaseLowKind,
	EvidenceValuationBaseTarget,
	EvidenceValuationSuitability,
}

// KnownEvidenceKey reports whether a key is in the registry. Mirrors KnownReason so a key
// decoded from an archive can be validated.
func KnownEvidenceKey(k string) bool {
	for _, known := range AllEvidenceKeys {
		if known == k {
			return true
		}
	}
	return false
}

// evidenceKeyForLevel maps a level source onto its census key.
//
// The ONE place the mapping lives, for the reason Snapshot.observationFor is the one place the
// source → field mapping lives: a second copy is a second chance to file the MA60's value
// under the MA20's key, and both are plausible prices so no assertion on the numbers would
// catch it. It returns "" for a source that is not in the registry — a key is never invented
// for a code this package does not know.
func evidenceKeyForLevel(src LevelSource) string {
	switch src {
	case LevelMA20:
		return EvidenceLevelMA20
	case LevelMA60:
		return EvidenceLevelMA60
	case LevelBaseLow:
		return EvidenceLevelBaseLow
	case LevelBreakoutHigh:
		return EvidenceLevelPivotHigh
	}
	return ""
}
