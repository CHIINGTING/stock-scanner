package entryplanbacktest

import (
	"fmt"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ── Regime provenance (EP-9 §5) ───────────────────────────────────────────────────────────

// RegimeProvenance is WHERE an observation's market regime came from, per observation.
//
// It is a third thing beside entryplan.RegimeSource's LIVE and REPLAY, and the reason is that
// RegimeSource has no value for "there is none". entryplan refuses "" as a source
// (internal/entryplan/series.go:49-53) precisely so an absent regime cannot be read as LIVE,
// which leaves the study needing somewhere to put the absence. This is it.
type RegimeProvenance string

const (
	// RegimeArchived — a LIVE regime recorded ON the session itself, by the market pipeline
	// (data/market/market_<date>.json, or the market.regime evidence row a scan persisted).
	// entryplan.RegimeSourceLive.
	RegimeArchived RegimeProvenance = "ARCHIVED"

	// RegimeReplayedPIT — a point-in-time replay from the local price cache
	// (model.RegimeReplay, data/market_replay/regime_<date>.json).
	// entryplan.RegimeSourceReplay.
	//
	// NOT INTERCHANGEABLE WITH ARCHIVED, and the repo has already measured by how much: on
	// 2026-08-31, the one session both archives cover, all ten price metrics matched to the
	// last decimal while breadth_above_ma20 read 46.65 live against 50.56 replayed and
	// advancing_ratio 22.39 against 30.60 (internal/market/model/regime_replay.go:49-54).
	// A replay also carries no institutional posture and no Market Score at all, so rules R4
	// and R5 can never fire in one (internal/market/analyzer/regime_replay.go:20-23).
	//
	// A study segment must therefore be labelled with the provenance it used, and a mixed
	// segment is not a segment.
	RegimeReplayedPIT RegimeProvenance = "REPLAYED_PIT"

	// RegimeUnavailable — neither archive covers this session.
	//
	// IT IS NOT A REGIME. It must never be merged into SIDEWAYS or UNKNOWN; see
	// RegimeProvenance.Regime.
	RegimeUnavailable RegimeProvenance = "UNAVAILABLE"
)

// Valid reports whether p is one of the three defined provenances.
func (p RegimeProvenance) Valid() bool {
	switch p {
	case RegimeArchived, RegimeReplayedPIT, RegimeUnavailable:
		return true
	}
	return false
}

// Regime returns the model.Regime an observation with this provenance may carry.
//
// UNAVAILABLE RETURNS ("", false) AND NOT SIDEWAYS, AND NOT UNKNOWN. Both substitutions are
// already forbidden one layer down and this function is where the study inherits the ban:
//
//   - SIDEWAYS is a VERDICT. analyzer.DecideRegime's R0 rule, quoted in
//     internal/entryplan/doc.go, says UNKNOWN "is a state, not a failure, and it must never
//     fall through to SIDEWAYS ('we looked, no edge') which is a verdict".
//   - UNKNOWN is a MEASUREMENT OUTCOME: the regime layer ran on that session and the data
//     could not support a call. model.RegimeUnknown is a legal value of a RegimeSession
//     (internal/entryplan/series.go:141-143: "a day the regime layer could not call is a real
//     row"). "No archive covers this session" is not that; nothing ran.
//
// So a regime-segmented table in Phase 2 has a row for UNAVAILABLE, or it excludes those
// observations and says how many — it does not have them quietly sitting in SIDEWAYS.
func (p RegimeProvenance) Regime(r model.Regime) (model.Regime, bool) {
	switch p {
	case RegimeArchived, RegimeReplayedPIT:
		if !r.Valid() {
			return "", false
		}
		return r, true
	default:
		return "", false
	}
}

// ── Valuation provenance (EP-9 §6) ────────────────────────────────────────────────────────

// ValuationProvenance is what Phase 2 can say about ONE observation's valuation evidence.
//
// The three values are the three genuinely different situations EP-6G's limitation leaves
// behind, and collapsing any two of them would make the valuation ceiling's effect on Target2
// unreadable.
type ValuationProvenance string

const (
	// ValuationReproducibleSuitable — a valuation archive dated on or before the signal
	// session exists for this symbol, and its suitability permits the Target2 ceiling
	// (entryplan.ValuationSuitability.PermitsCeiling: anything known except UNSUITABLE).
	// The plan's Target2 could have been capped, and the study can reproduce the cap.
	ValuationReproducibleSuitable ValuationProvenance = "REPRODUCIBLE_SUITABLE"

	// ValuationAvailableUnsuitable — the archive reaches this session and the valuation was
	// screened out: UNSUITABLE, so no ceiling applies. This is a MEASUREMENT, and the plan's
	// structural Target2 standing is a fact rather than a gap.
	ValuationAvailableUnsuitable ValuationProvenance = "AVAILABLE_UNSUITABLE"

	// ValuationUnavailableHistorically — no valuation archive dated on or before the signal
	// session covers this symbol, so nothing about its valuation can be reconstructed.
	//
	// NOT the same as UNSUITABLE. The first says the ceiling could not be looked up; the
	// second says it was looked up and refused. An UNCAPPED Target2 means opposite things
	// under the two, and a study that merged them could not tell a rule's effect from a
	// data gap.
	ValuationUnavailableHistorically ValuationProvenance = "UNAVAILABLE_HISTORICALLY"
)

// Valid reports whether v is one of the three defined provenances.
func (v ValuationProvenance) Valid() bool {
	switch v {
	case ValuationReproducibleSuitable, ValuationAvailableUnsuitable, ValuationUnavailableHistorically:
		return true
	}
	return false
}

// PermitsCeiling reports whether an observation with this provenance may carry a
// valuation-capped Target2. UNAVAILABLE_HISTORICALLY never does — a study must not restore a
// cap the plan could not have had.
func (v ValuationProvenance) PermitsCeiling() bool { return v == ValuationReproducibleSuitable }

// Validate refuses an undefined provenance pair on one observation.
func ValidateProvenance(r RegimeProvenance, v ValuationProvenance) error {
	if !r.Valid() {
		return fmt.Errorf("entryplanbacktest: regime provenance %q is not ARCHIVED, "+
			"REPLAYED_PIT or UNAVAILABLE", r)
	}
	if !v.Valid() {
		return fmt.Errorf("entryplanbacktest: valuation provenance %q is not "+
			"REPRODUCIBLE_SUITABLE, AVAILABLE_UNSUITABLE or UNAVAILABLE_HISTORICALLY", v)
	}
	return nil
}
