package pricingpower

import "math"

// ScoreInputs feeds Compute. Dimensions carry per-dimension availability + earned/max.
// IndependentSources / HasOfficialDisclosure describe the EVIDENCE independence used for
// the confidence ceiling (per the v1 rule: one podcast → LOW regardless of snippet count).
type ScoreInputs struct {
	Dimensions            []DimensionScore
	IndependentSources    int  // count of genuinely INDEPENDENT confirming sources
	HasOfficialDisclosure bool // official price-hike letter / earnings call present
}

// ScoreResult is the fully-explainable scoring output.
type ScoreResult struct {
	Score           int
	Earned          float64
	AvailableWeight float64
	TotalWeight     float64
	Coverage        float64
	Confidence      Confidence
	Dimensions      []DimensionScore
}

// Compute normalizes the earned score over AVAILABLE weight only (graceful degradation:
// NOT_AVAILABLE dimensions are removed from the denominator, never scored zero), and
// derives a deterministic confidence that is capped by data coverage. A dimension that IS
// available but earned 0 still counts toward the denominator (that is a real miss).
func Compute(in ScoreInputs, th Thresholds) ScoreResult {
	var earned, available float64
	fundamentalsAvail := false
	for _, d := range in.Dimensions {
		if d.Availability == NotAvailable {
			continue
		}
		available += d.Max
		earned += d.Earned
		if fundamentalDims[d.Name] {
			fundamentalsAvail = true
		}
	}

	score := 0
	if available > 0 {
		score = int(math.Round(earned / available * 100))
	}
	coverage := available / TotalWeight

	return ScoreResult{
		Score:           score,
		Earned:          earned,
		AvailableWeight: available,
		TotalWeight:     TotalWeight,
		Coverage:        coverage,
		Confidence:      classifyConfidence(coverage, in.IndependentSources, fundamentalsAvail, in.HasOfficialDisclosure, th),
		Dimensions:      in.Dimensions,
	}
}

// classifyConfidence = min(dataCap, sourceCeiling). Data coverage caps confidence (a high
// score from tiny data can never be HIGH); source independence sets the eligibility
// ceiling (single podcast → LOW; ≥2 independent → up to MEDIUM; official + independent →
// HIGH). Fundamentals being present raises the ceiling floor to MEDIUM (hard numbers).
func classifyConfidence(coverage float64, indepSources int, fundamentalsAvail, hasOfficial bool, th Thresholds) Confidence {
	// data-coverage cap
	dataCap := ConfLow
	switch {
	case coverage >= th.CoverageHigh:
		dataCap = ConfHigh
	case coverage >= th.CoverageMedium:
		dataCap = ConfMedium
	}

	// source-independence ceiling
	ceiling := ConfLow
	if indepSources >= 2 {
		ceiling = ConfMedium
	}
	if hasOfficial && indepSources >= 2 {
		ceiling = ConfHigh
	}
	// fundamentals (hard numbers) raise the floor to MEDIUM even without independent news
	if fundamentalsAvail && coverage >= th.FundCoverage && rank(ceiling) < rank(ConfMedium) {
		ceiling = ConfMedium
	}

	if rank(dataCap) < rank(ceiling) {
		return dataCap
	}
	return ceiling
}

func rank(c Confidence) int {
	switch c {
	case ConfHigh:
		return 2
	case ConfMedium:
		return 1
	default:
		return 0
	}
}
