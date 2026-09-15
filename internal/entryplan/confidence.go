package entryplan

// Confidence is how much evidence stands behind a Plan.
//
// A SECOND AXIS, reported alongside Status and never in place of it — exactly as
// valuation.Quality is reported alongside valuation.Availability.
//
// # The two axes measure different things, and that is why both exist
//
//	Confidence  how complete the EVIDENCE is       — "is what we were handed enough?"
//	Status      whether a VERDICT could be reached — "is there a rule that turns it into a price?"
//
// So the pairings are a PRODUCT, not a diagonal, and the table below is the authoritative one:
//
//	WAIT_PULLBACK     + HIGH   WAIT_PULLBACK + MEDIUM   WAIT_PULLBACK + LOW
//	INSUFFICIENT_DATA + HIGH   ← EP-2's own output: every input in hand, and NO RULE YET
//	INSUFFICIENT_DATA + LOW / MEDIUM   ← same, on thinner corroboration
//	INSUFFICIENT_DATA + INSUFFICIENT_DATA   ← the EVIDENCE is what is missing
//
// # A MISSING RULE IS NOT MISSING EVIDENCE
//
// The second row is the one this table used to deny. It paired an INSUFFICIENT_DATA status with
// an INSUFFICIENT_DATA confidence and with nothing else, which described EP-1 — where required
// item #4 was unfillable, so the census could never clear its floor for ANY input and this
// whole axis was dead. EP-2 supplies the field (plan.go, required item #4: "a missing rule is
// not missing evidence"), so a caller can hold everything the plan needs and grade HIGH while
// Status stays INSUFFICIENT_DATA because no rule computes a price yet.
//
// Collapsing the two axes would make "we have the evidence and no rule" the same row as "we
// have no evidence", and only one of those is fixed by fetching more data. Which direction the
// collapse ran would not even be recoverable from an archived plan.
//
// A LOW grade is a caveat printed next to a plan, not the removal of the plan.
type Confidence string

const (
	// ConfidenceHigh — everything required is present, and so is every corroborating item
	// the caller asked about.
	ConfidenceHigh Confidence = "HIGH"
	// ConfidenceMedium — everything required is present, and the corroboration is partial.
	ConfidenceMedium Confidence = "MEDIUM"
	// ConfidenceLow — everything required is present and NOTHING corroborates it. The plan
	// is arithmetically valid and rests on the minimum.
	ConfidenceLow Confidence = "LOW"
	// ConfidenceInsufficientData — the required evidence is not there, so there is no plan
	// to be confident about.
	//
	// Distinct from LOW, and the distinction is the whole point of this file: LOW means "we
	// computed it, trust it less", INSUFFICIENT means there is nothing to trust or distrust.
	ConfidenceInsufficientData Confidence = "INSUFFICIENT_DATA"
)

// ConfidenceMetrics is the evidence census a grade is computed from.
//
// Counts, not scores. Every field is something the caller can point at, so a grade is always
// explained by showing its inputs — the discipline valuation.QualityMetrics established.
//
// REQUIRED vs OPTIONAL is the caller's declaration, not this file's: which evidence a
// particular plan cannot be made without is a policy question owned by the work item that
// makes the plan. This function only counts what it is handed.
type ConfidenceMetrics struct {
	// RequiredTotal is how many pieces of evidence the plan cannot be made without;
	// RequiredPresent is how many of them are actually in hand.
	RequiredTotal   int `json:"required_total"`
	RequiredPresent int `json:"required_present"`

	// OptionalTotal / OptionalPresent are the corroborating evidence — items whose absence
	// weakens a plan without preventing it.
	OptionalTotal   int `json:"optional_total"`
	OptionalPresent int `json:"optional_present"`
}

// ClassifyConfidence grades the evidence behind a plan.
//
// Deterministic and total: same metrics, same grade, for every possible input. No clock, no
// I/O, no model.
//
// available is passed IN rather than re-derived, for the reason valuation.ClassifyQuality
// takes it as a parameter: the availability contract lives in one place — here, ComputePlan's
// gap check — and a second implementation inside the grader would be free to drift from it.
//
// STRUCTURAL, with no numeric thresholds anywhere. Every branch turns on an ordering or an
// emptiness, which is deliberate at EP-1: a tuned cut-off would be a policy decision made
// before there is any outcome data to tune against, and it would then be quoted as though it
// had been.
func ClassifyConfidence(m ConfidenceMetrics, available bool) Confidence {
	// AVAILABILITY FIRST. Nothing to grade → say so, before any grading rule can run.
	//
	// This ordering is load-bearing and is what TestMissingEvidenceIsNotLowConfidence exists
	// to protect. FU-9's M11 mutation was precisely this check being bypassed, so a
	// below-floor stock came out graded instead of refused — and the failure mode here is
	// worse than there, because "LOW confidence entry plan" reads as a plan a reader may
	// take with a smaller size, when in fact there are no prices in it at all.
	//
	// RequiredPresent < RequiredTotal belongs on THIS side of the line, not in the switch:
	// a missing required input is an availability fact, the analogue of ValidSamples <= 0.
	if !available || m.RequiredTotal <= 0 || m.RequiredPresent < m.RequiredTotal {
		return ConfidenceInsufficientData
	}

	switch {
	case m.OptionalTotal <= 0:
		// Everything required is in hand and no corroborating evidence was even asked
		// for. That is the minimum, so it grades as the minimum — rather than as HIGH,
		// which an empty optional census would otherwise satisfy vacuously.
		return ConfidenceLow
	case m.OptionalPresent >= m.OptionalTotal:
		return ConfidenceHigh
	case m.OptionalPresent > 0:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}
