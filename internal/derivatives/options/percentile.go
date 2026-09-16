package options

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// Percentiles: where a PCR sits in ITS OWN population, and nothing else's.
//
// §10.8: "Percentiles reuse §10.4's policy verbatim — 60 observations, minimum 20, ratio in
// [0,1], ties at-or-below — and every combination gets its OWN distribution: metric type x
// contract family x session x selection policy. Mixing volume with OI, weekly with monthly, or
// day with night makes a percentile that ranks a figure against a population it does not
// belong to."
//
// So the RULE is M4's and is imported rather than restated: the window and the minimum come
// from institutional.Policy, the tie handling from institutional.PercentileRatioAtOrBelow, and
// the four rule constants below are M4's own. What is M5's is the PARTITION, and it is
// enforced structurally: Percentile takes a history of Observations that must all carry the
// same DistributionKey, and a mismatch is ErrKeyMismatch rather than a wider distribution.
// There is no way to ask this package for a pooled percentile.
//
// The four separations are not interchangeable stylistic choices. On the 2026-09-04 fixture,
// TXO put volume is 63,014 and TXO put open interest is 5,651 — an order of magnitude — so a
// pooled volume/OI distribution would rank every OI reading in the bottom few percent and the
// number would look like a conclusion.

// The rule constants, re-exported from M4 so a caller reads one set and a test can assert the
// two are the same values rather than two spellings that happen to agree today.
const (
	// DefaultPercentileLookbackObservations is the reference window in VALID OBSERVATIONS,
	// including the reading being ranked.
	DefaultPercentileLookbackObservations = institutional.DefaultPercentileLookbackObservations
	// DefaultMinPercentileSample is the fewest contributing values that may produce a
	// percentile.
	DefaultMinPercentileSample = institutional.DefaultMinPercentileSample
	// PercentileIncludesCurrentObservation — the reading is in its own distribution, so the
	// ratio is in (0,1] and never 0.
	PercentileIncludesCurrentObservation = institutional.PercentileIncludesCurrentObservation
	// PercentileTieRule — ties count AT OR BELOW.
	PercentileTieRule = institutional.PercentileTieRule
	// PercentileScale — the answer is in [0,1], not [0,100].
	PercentileScale = institutional.PercentileScale
)

// PercentileStatus is M4's evaluation vocabulary, reused unchanged: §6.1 forbids
// "not enough observations" from ever becoming a source status, and having one type for it in
// the whole layer is the simplest way to keep that true.
type PercentileStatus = institutional.PercentileStatus

// The three states, re-exported for the same reason.
const (
	PercentileAvailable          = institutional.PercentileAvailable
	PercentileInsufficientSample = institutional.PercentileInsufficientSample
	PercentileValueAbsent        = institutional.PercentileValueAbsent
)

// Exclusion reasons for a window entry that contributed nothing.
const (
	// ExcludedNotUsable — the day's snapshot status was not AVAILABLE or PARTIAL.
	ExcludedNotUsable = institutional.ExcludedNotUsable
	// ExcludedRatioAbsent — the day is usable and this metric has no ratio on it. Never
	// zero-filled: a fabricated 0 would drag every percentile towards the middle of a
	// distribution that never happened.
	ExcludedRatioAbsent = "RATIO_ABSENT"
)

// ExcludedReading is one window entry that contributed no value, and why.
type ExcludedReading struct {
	TradingDate string                     `json:"trading_date"`
	Status      institutional.MetricStatus `json:"status"`
	Reason      string                     `json:"reason"`
}

// DistributionKey is §10.8's partition: metric x family x session x selection policy, plus the
// product, because two products' ratios are no more comparable than two metrics'.
//
// It is a TYPE rather than four arguments so that a caller cannot build a distribution without
// stating all of it, and so that Same is the one place the partition can be widened.
type DistributionKey struct {
	Metric MetricType     `json:"metric"`
	Key    ObservationKey `json:"key"`
}

// Same reports whether two readings belong in one distribution.
func (d DistributionKey) Same(o DistributionKey) bool {
	return d.Metric == o.Metric && d.Key.Same(o.Key)
}

// String renders the key for an error message.
func (d DistributionKey) String() string { return string(d.Metric) + "/" + d.Key.String() }

// ReferenceDistribution is one key's reference set, with everything needed to disbelieve it.
type ReferenceDistribution struct {
	Key DistributionKey `json:"key"`
	// AsOf is the reading's trading date. Every contributing observation is at or before it;
	// the fetched_at half of that bound is the reader's, because only the reader knows the
	// cutoff instant.
	AsOf string `json:"as_of"`
	// Values are the contributing ratios, ASCENDING.
	Values []float64 `json:"values"`
	// Dates are the contributing trading dates, NEWEST FIRST.
	Dates                []string          `json:"dates"`
	Excluded             []ExcludedReading `json:"excluded,omitempty"`
	WindowObservations   int               `json:"window_observations"`
	LookbackObservations int               `json:"lookback_observations"`
	IncludesCurrent      bool              `json:"includes_current"`
	TieRule              string            `json:"tie_rule"`
	Scale                string            `json:"scale"`
	RuleVersion          string            `json:"rule_version"`
}

// Size is the number of contributing values.
func (d ReferenceDistribution) Size() int { return len(d.Values) }

// Ratio is the share of the distribution at or below v, in [0,1].
//
// Delegated to institutional.PercentileRatioAtOrBelow so the tie rule has ONE implementation
// in the layer: with `<` instead of `<=`, a reading exactly at its own historical median would
// report below 50%, and the institutional percentile beside it in the same panel would not.
func (d ReferenceDistribution) Ratio(v float64) (float64, bool) {
	return institutional.PercentileRatioAtOrBelow(d.Values, v)
}

// RatioPercentile is one PCR's position in its own distribution, or the recorded reason there
// isn't one.
type RatioPercentile struct {
	Key DistributionKey `json:"key"`
	// Ratio is the share of the reference distribution at or below the reading, in [0,1].
	// Non-nil only when Status is AVAILABLE. Not to be confused with the PCR itself, which
	// is also a ratio — hence the key on every value.
	Ratio                *float64         `json:"ratio,omitempty"`
	Status               PercentileStatus `json:"status"`
	Display              string           `json:"display"`
	Reason               string           `json:"reason,omitempty"`
	SampleSize           int              `json:"sample_size"`
	RequiredSampleSize   int              `json:"required_sample_size"`
	ExcludedCount        int              `json:"excluded_count"`
	LookbackObservations int              `json:"lookback_observations"`
	WindowObservations   int              `json:"window_observations"`
	IncludesCurrent      bool             `json:"includes_current"`
	TieRule              string           `json:"tie_rule"`
	Scale                string           `json:"scale"`
	AsOf                 string           `json:"as_of"`
	FirstDate            string           `json:"first_date,omitempty"`
	LastDate             string           `json:"last_date,omitempty"`
	RuleVersion          string           `json:"rule_version"`
}

// Value returns the percentile and whether there is one.
func (r RatioPercentile) Value() (float64, bool) {
	if r.Status != PercentileAvailable || r.Ratio == nil {
		return 0, false
	}
	return *r.Ratio, true
}

// Percentile ranks one metric's reading against its own reference distribution.
//
// history must already be point-in-time filtered by the caller and must consist of the SAME
// measurement: a different metric, family, session, product or selection policy is
// ErrKeyMismatch, not a wider population. An entry dated at or after the reading is
// ErrLookAhead, not a silently dropped row.
func Percentile(m MetricType, current Observation, history []Observation,
	p institutional.Policy) (RatioPercentile, ReferenceDistribution, error) {

	np, err := p.Normalized()
	if err != nil {
		return RatioPercentile{}, ReferenceDistribution{}, err
	}
	if !m.Valid() {
		return RatioPercentile{}, ReferenceDistribution{},
			fmt.Errorf("options: %q is not a metric type", m)
	}
	curDate, err := parseDate(current.TradingDate)
	if err != nil {
		return RatioPercentile{}, ReferenceDistribution{}, err
	}
	ordered, err := orderedHistory(current, curDate, history)
	if err != nil {
		return RatioPercentile{}, ReferenceDistribution{}, err
	}

	key := DistributionKey{Metric: m, Key: current.Key}
	d := ReferenceDistribution{
		Key:                  key,
		AsOf:                 current.TradingDate,
		LookbackObservations: np.PercentileLookbackObservations,
		IncludesCurrent:      PercentileIncludesCurrentObservation,
		TieRule:              PercentileTieRule,
		Scale:                PercentileScale,
		RuleVersion:          PercentileRuleVersion,
	}

	// Decision 1 and decision 3: the window is the reading itself plus the most recent
	// earlier USABLE observations, up to L in total. A non-usable day does not consume a
	// slot — a fortnight of NOT_PUBLISHED must not silently shorten the window to a
	// fortnight of nothing.
	window := make([]Observation, 0, np.PercentileLookbackObservations)
	if current.Usable() {
		window = append(window, current)
	} else {
		d.Excluded = append(d.Excluded, ExcludedReading{
			TradingDate: current.TradingDate, Status: current.SourceStatus,
			Reason: ExcludedNotUsable,
		})
	}
	for _, h := range ordered {
		if len(window) >= np.PercentileLookbackObservations {
			break
		}
		if !h.obs.Usable() {
			d.Excluded = append(d.Excluded, ExcludedReading{
				TradingDate: h.obs.TradingDate, Status: h.obs.SourceStatus,
				Reason: ExcludedNotUsable,
			})
			continue
		}
		window = append(window, h.obs)
	}
	d.WindowObservations = len(window)

	for _, w := range window {
		wp, err := w.PCR(m)
		if err != nil {
			return RatioPercentile{}, ReferenceDistribution{}, err
		}
		v, ok := wp.Ratio.Value()
		if !ok {
			d.Excluded = append(d.Excluded, ExcludedReading{
				TradingDate: w.TradingDate, Status: w.SourceStatus,
				Reason: ExcludedRatioAbsent,
			})
			continue
		}
		d.Values = append(d.Values, v)
		d.Dates = append(d.Dates, w.TradingDate)
	}
	sort.Float64s(d.Values)
	sort.Sort(sort.Reverse(sort.StringSlice(d.Dates)))
	sort.SliceStable(d.Excluded, func(i, j int) bool {
		return d.Excluded[i].TradingDate > d.Excluded[j].TradingDate
	})

	cur, err := current.PCR(m)
	if err != nil {
		return RatioPercentile{}, ReferenceDistribution{}, err
	}
	return d.rankOf(cur.Ratio, np.MinPercentileSample), d, nil
}

// rankOf places one reading in this distribution, or records why it could not be placed.
func (d ReferenceDistribution) rankOf(r ObservedRatio, minSample int) RatioPercentile {
	out := RatioPercentile{
		Key:                  d.Key,
		SampleSize:           len(d.Values),
		RequiredSampleSize:   minSample,
		ExcludedCount:        len(d.Excluded),
		LookbackObservations: d.LookbackObservations,
		WindowObservations:   d.WindowObservations,
		IncludesCurrent:      d.IncludesCurrent,
		TieRule:              d.TieRule,
		Scale:                d.Scale,
		AsOf:                 d.AsOf,
		RuleVersion:          d.RuleVersion,
	}
	if len(d.Dates) > 0 {
		out.FirstDate = d.Dates[len(d.Dates)-1]
		out.LastDate = d.Dates[0]
	}

	v, ok := r.Value()
	if !ok {
		// The reading has no number. That is a fact about today, not about the length of
		// the history, and the two must not both render as "no percentile".
		out.Status = PercentileValueAbsent
		out.Display = string(PercentileValueAbsent)
		out.Reason = fmt.Sprintf("%s is %s for %s, so there is nothing to rank",
			d.Key.Metric, r.Status, d.AsOf)
		if r.Reason != "" {
			out.Reason += " (" + r.Reason + ")"
		}
		return out
	}
	if len(d.Values) < minSample {
		out.Status = PercentileInsufficientSample
		out.Display = string(PercentileInsufficientSample)
		out.Reason = fmt.Sprintf(
			"%s has %d reference observations at or before %s and %d are required; a "+
				"percentile over %d points reads as authoritative and is not one",
			d.Key, len(d.Values), d.AsOf, minSample, len(d.Values))
		return out
	}
	ratio, has := d.Ratio(v)
	if !has {
		out.Status = PercentileValueAbsent
		out.Display = string(PercentileValueAbsent)
		out.Reason = fmt.Sprintf("%s: the reading %v could not be ranked", d.Key, v)
		return out
	}
	out.Status = PercentileAvailable
	out.Ratio = &ratio
	// The one x100 in this package, and it is a DISPLAY of a percentile — never of a PCR.
	out.Display = fmt.Sprintf("%.1f%%", ratio*100)
	return out
}
