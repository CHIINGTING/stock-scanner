package valuation

import "math"

// Valuation evidence QUALITY — how much a distribution deserves to be trusted, as a statement
// separate from whether it could be computed at all.
//
// # Why this is not an availability status
//
// The availability contract answers one question: are there at least MinHistoricalPESamples
// usable observations? It is deterministic, it gates the target price, and it must keep
// meaning exactly what it has always meant. But it is a floor, and two stocks standing on it
// can be nothing alike:
//
//	2330 台積電   225 valid / 225 archived sessions   coverage 100%   samples span 339 days
//	2337 旺宏      26 valid / 225 archived sessions   coverage  12%   samples span  36 days
//
// Both are AVAILABLE, and both produce a target price. One of them is a year of a company's
// own trading history; the other is five weeks, drawn from a window in which the company
// mostly had no earnings to divide by. A reader shown only "AVAILABLE" cannot tell them apart.
//
// So quality is a SECOND axis, reported alongside availability and never in place of it:
//
//	AVAILABLE + HIGH        AVAILABLE + MEDIUM        AVAILABLE + LOW        INSUFFICIENT_DATA
//
// Nothing here can make a computable distribution uncomputable. A LOW grade is a caveat
// printed next to a number, not the removal of the number.
//
// # What it deliberately does NOT judge
//
// Whether P/E is the right lens for this company at all. 3037 欣興 has 225 of 225 sessions and
// a median around 120x; the data could not be better, and the multiple may still be the wrong
// model for a cyclical substrate maker at the top of a cycle. That is a question about the
// MODEL, not about the evidence, and answering it needs sector knowledge this layer does not
// have. Conflating the two would make "we have excellent data" and "this multiple makes sense"
// the same claim, which is how a data-quality grade turns into an investment opinion.
//
// Recorded as its own follow-up: Valuation Model Suitability.

// Quality grades the evidence behind a P/E distribution.
//
// A separate type from Availability on purpose. They are different questions with different
// vocabularies, and one enum carrying both would let a caller write a switch that silently
// treats LOW as a kind of missing.
type Quality string

const (
	// QualityHigh — a long, densely covered history. The distribution describes how this
	// stock has actually traded.
	QualityHigh Quality = "HIGH"
	// QualityMedium — enough history to be a distribution rather than a snapshot, but with
	// real gaps in it. Usable; worth saying out loud.
	QualityMedium Quality = "MEDIUM"
	// QualityLow — clears the availability floor and nothing more. The statistics are
	// arithmetically valid and rest on very little.
	QualityLow Quality = "LOW"
	// QualityInsufficient — the distribution itself is not available, so there is no
	// evidence to grade. Distinct from LOW: LOW means "we computed it, trust it less",
	// INSUFFICIENT means there is nothing to trust or distrust.
	QualityInsufficient Quality = "INSUFFICIENT"
)

// QualityRuleVersion identifies the thresholds a grade was produced by.
//
// It travels with every grade and is persisted with it. Thresholds WILL change — these are a
// first, deliberately conservative cut — and a stored "LOW" with no version attached becomes
// uninterpretable the moment they do: a reader cannot tell whether the stock changed or the
// rule did. Bump this whenever any threshold or rule below changes.
const QualityRuleVersion = "FU9-v1"

// The thresholds.
//
// HEURISTIC — NOT BACKTEST-FITTED. Nothing here was tuned against outcomes, because there is
// no outcome data that could tune it: this repo has one archived quarter of health checks, and
// fitting a confidence grade to that would produce a number that describes 2026 rather than a
// rule. They are derived from the ARITHMETIC OF THE CALENDAR instead, which is a claim that
// can be checked without any returns at all:
//
//	the Taiwanese exchanges trade roughly 21 sessions a month, so
//	  a quarter  ≈  60 sessions   ≈  90 days
//	  half a year ≈ 120 sessions  ≈ 180 days
//
// And a trailing P/E moves for exactly two reasons: price, which moves daily, and reported
// earnings, which move QUARTERLY. A distribution spanning less than one reporting cycle has
// seen the denominator change zero times — it is one earnings regime observed at many prices,
// which is a much narrower claim than "how this stock is valued". One quarter is therefore the
// smallest span that can be called a distribution at all, and two quarters the smallest that
// has seen the denominator move more than once.
//
// That reasoning fixes the sample counts and the spans together; the coverage floors are the
// looser of the two constraints at each level, chosen so that a company profitable for most of
// the window is not marked down for the months before it was.
//
// Explicitly NOT fitted to any stock in the current universe. 2337 旺宏 fails three of the
// MEDIUM conditions independently (26 < 60 samples, 12% < 30% coverage, 36 < 90 days), so no
// threshold here is load-bearing for its grade — which is the property that makes them
// general rather than reverse-engineered from one case.
const (
	// HIGH: at least two reporting cycles of samples, spanning at least two.
	QualityHighMinSamples  = 120
	QualityHighMinCoverage = 0.70
	QualityHighMinSpanDays = 180

	// MEDIUM: at least one reporting cycle of samples, spanning at least one.
	QualityMediumMinSamples  = 60
	QualityMediumMinCoverage = 0.30
	QualityMediumMinSpanDays = 90
)

// QualityMetrics is the measured evidence a grade is computed from.
//
// Every field is a count or a date this repo can point at, so a grade can always be explained
// by showing its inputs rather than by appealing to the rule that produced it.
type QualityMetrics struct {
	// ValidSamples is the number of SESSIONS contributing a usable P/E — the same count the
	// availability floor is applied to.
	ValidSamples int `json:"valid_samples"`
	// WindowSessions is the number of sessions ARCHIVED in the window, whether or not the
	// exchange published a P/E for them.
	//
	// This is the denominator, and getting it from anywhere else is the single most likely
	// way to break FU-9. A session the exchange published no P/E for is an ACQUIRED session
	// — the company had no positive earnings, which is a fact about the company and not a
	// gap in this repo's data. Counting only valid samples would make coverage 100% for
	// every stock by construction, including a stock with four usable observations.
	WindowSessions int `json:"window_sessions"`

	// OldestValidSession / NewestValidSession bound the usable samples. Empty when none.
	OldestValidSession string `json:"oldest_valid_session,omitempty"`
	NewestValidSession string `json:"newest_valid_session,omitempty"`
	// OldestSession / NewestSession bound the archive, valid or not.
	OldestSession string `json:"oldest_session,omitempty"`
	NewestSession string `json:"newest_session,omitempty"`

	// SampleSpanSessions is how many ARCHIVED sessions lie between the oldest and newest
	// valid sample, inclusive — the width of the region the samples were drawn from,
	// measured in the archive's own units.
	SampleSpanSessions int `json:"sample_span_sessions"`
	// SampleSpanDays is the same width in calendar days, inclusive.
	//
	// Both are carried because they answer different questions and can disagree: 26 samples
	// spanning 36 days is a month of dense observation; 26 spanning 300 days is a year of
	// sparse ones. Neither count alone distinguishes those, and they are not the same
	// evidence.
	SampleSpanDays int `json:"sample_span_days"`
}

// CoverageRatio is valid samples ÷ archived sessions, in [0,1].
//
// A RATIO, matching CurrentPercentile and the rest of this repo. The ×100 happens once, at
// presentation. Returns nil when there is no window to divide by — never 0, which would read
// as "we looked and found nothing" for a stock nobody has archived yet.
func (m QualityMetrics) CoverageRatio() *float64 {
	if m.WindowSessions <= 0 {
		return nil
	}
	r := float64(m.ValidSamples) / float64(m.WindowSessions)
	return &r
}

// SampleConcentration reports whether the valid samples occupy a small part of the archived
// window — the 2337 shape, where every usable observation is from the last few weeks.
//
// It is a DESCRIPTION, not a grade input: the span thresholds already carry this into the
// classification, and having two rules pulling on the same evidence would make a grade change
// for reasons nobody could trace. Nil when there is nothing to compare.
func (m QualityMetrics) SampleConcentration() *float64 {
	if m.WindowSessions <= 0 || m.SampleSpanSessions <= 0 {
		return nil
	}
	r := float64(m.SampleSpanSessions) / float64(m.WindowSessions)
	return &r
}

// ClassifyQuality grades the evidence.
//
// Deterministic and total: same metrics, same grade, for every possible input. No clock, no
// I/O, no model. The AI layer receives the result and may explain it; nothing in the AI path
// can reach this function.
//
// available is the distribution's own availability. It is passed in rather than re-derived
// because the availability contract lives in one place — historicalStats, against
// MinHistoricalPESamples — and a second implementation of it here would be free to drift.
func ClassifyQuality(m QualityMetrics, available bool) Quality {
	// Not computable → nothing to grade. Checked FIRST so a stock with no usable P/E can
	// never be described as merely low-quality: 3055 蔚華科 has 225 archived sessions and not
	// one positive P/E, and "LOW" would tell a reader the number in front of them is weak
	// when there is no number at all.
	if !available || m.ValidSamples <= 0 {
		return QualityInsufficient
	}

	cov := m.CoverageRatio()
	if cov == nil {
		// Samples with no archived window to measure them against. That is a caller
		// assembling history by hand, not a state the archive can produce; grade it LOW
		// rather than inventing a coverage of 1.0 to clear the bar with.
		return QualityLow
	}

	switch {
	case m.ValidSamples >= QualityHighMinSamples &&
		*cov >= QualityHighMinCoverage &&
		m.SampleSpanDays >= QualityHighMinSpanDays:
		return QualityHigh
	case m.ValidSamples >= QualityMediumMinSamples &&
		*cov >= QualityMediumMinCoverage &&
		m.SampleSpanDays >= QualityMediumMinSpanDays:
		return QualityMedium
	default:
		// Clears the availability floor and nothing above it.
		return QualityLow
	}
}

// Dispersion is how wide the distribution is, which is a different worry from how much of it
// there is.
//
// It is REPORTED but does not feed ClassifyQuality, and that is a deliberate line. A wide P/E
// range usually means the market re-rated the company — a real event, correctly recorded —
// not that the observations are poor. Letting width lower a data-quality grade would file
// "this stock re-rated" and "our archive is thin" under the same warning, and would mark 3037
// 欣興 down for having faithfully captured a re-rating. Whether a re-rating makes the median a
// poor anchor is a MODEL question, which is the follow-up this work item explicitly excludes.
type Dispersion struct {
	Status Availability `json:"status"`
	Reason string       `json:"reason,omitempty"`
	// IQR is P75 − P25, in P/E points.
	IQR *float64 `json:"iqr,omitempty"`
	// RelativeIQR is IQR ÷ Median — the same width expressed as a fraction of the level, so
	// a 10-point spread around 15x and around 120x are not called equally wide.
	RelativeIQR *float64 `json:"relative_iqr,omitempty"`
}

// ComputeDispersion measures the width of an available distribution.
//
// Refuses rather than approximates. A non-positive or missing median has no meaningful
// fraction to divide by, and 0 would read as "perfectly tight" — the opposite of the truth.
func ComputeDispersion(h HistoricalPEStats) Dispersion {
	if h.Status != Available {
		return Dispersion{Status: InsufficientData,
			Reason: "分布尚未可用，無法計算離散度"}
	}
	p25, ok25 := finite(h.P25)
	p75, ok75 := finite(h.P75)
	med, okMed := finite(h.Median)
	if !ok25 || !ok75 {
		return Dispersion{Status: Unavailable, Reason: "缺少 P25 或 P75"}
	}
	iqr := p75 - p25
	if math.IsNaN(iqr) || math.IsInf(iqr, 0) {
		return Dispersion{Status: Unavailable, Reason: "四分位距不是有限值"}
	}
	d := Dispersion{Status: Available, IQR: &iqr}
	if !okMed || med <= 0 {
		// The absolute width still stands; only the ratio is undefined.
		d.Status = Partial
		d.Reason = "中位數不為正，相對離散度無法計算"
		return d
	}
	rel := iqr / med
	d.RelativeIQR = &rel
	return d
}
