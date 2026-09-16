package institutional

import (
	"fmt"
	"math"
	"sort"
)

// Percentiles: where a reading sits in its OWN semantics' history, and nothing else's.
//
// §10.4: "FLOW, POSITION and CHANGE each get their own reference distribution; sharing one
// would rank a traded-lot figure against held-lot figures. The distribution for a reading dated
// A contains only observations at or before A that were visible at A — the §7.3 rule, applied
// to history rather than to a single row. Below the configured minimum the answer is
// INSUFFICIENT_SAMPLE with the actual and required N, not a percentile computed from four
// points."
//
// The live 2026-09-04 session says why the three distributions may never be pooled: 投信 traded
// +1,785 lots while holding +76,174. A pooled distribution would put that FLOW reading in the
// bottom few percent of a set dominated by held-lot magnitudes, and the number would look like
// a conclusion.
//
// # The eight policy decisions, fixed here rather than left to a call site
//
//  1. LOOKBACK WINDOW — DefaultPercentileLookbackObservations (60) VALID OBSERVATIONS at or
//     before the reading, counted in observations and never in calendar days, for the same
//     reason the CHANGE horizons are (§10.4: "N counts observations, not calendar days"). 60 is
//     about a trading quarter; it is HEURISTIC and NOT BACKTEST-FITTED. Non-usable days do not
//     consume a slot — a fortnight of NOT_PUBLISHED must not silently shorten the window to a
//     fortnight of nothing.
//
//  2. MINIMUM SAMPLE — DefaultMinPercentileSample (20) contributing values. Below it there is
//     no percentile: Status is INSUFFICIENT_SAMPLE, Ratio is nil, and both the actual and the
//     required N travel with it. A "percentile" over four points reads as authoritative and
//     means nothing.
//
//  3. THE CURRENT OBSERVATION IS INCLUDED in its own reference distribution. This matches
//     internal/indicator's BIASPercentile, which ranks the latest BIAS against a series that
//     contains it, and it is the reason a ratio is never 0: the reading is always at or below
//     itself, so the answer is in (0,1]. Excluding it would make the two places in this repo
//     that compute a percentile disagree about the same number.
//
//  4. TIES COUNT AT OR BELOW (`<=`). Identical to internal/indicator.PercentileRank and
//     internal/valuation.percentileOf, and stated because the alternative is silently
//     different: with `<`, a reading exactly at its own historical median reports below 50%.
//
//  5. NULLS ARE EXCLUDED, NEVER ZERO-FILLED. An observation whose net column was absent for
//     this institution and scope contributes nothing to the distribution and is counted in
//     Excluded with its reason. Filling it with 0 would drag every percentile towards the
//     middle of a distribution that never happened — §6's "a value with nothing behind it must
//     never look like a number", applied to a reference set.
//
//  6. STALE IS EXCLUDED, and that is not configurable — the same ruling §10.4 makes for the
//     CHANGE baseline. A stale reading is one whose freshness contract already failed, and
//     admitting it to a reference distribution would launder that failure into every percentile
//     computed from it. So would MISSING, ERROR, NOT_PUBLISHED, NO_SESSION and NOT_AVAILABLE:
//     only AVAILABLE and PARTIAL observations contribute, and a PARTIAL contributes only for
//     the semantics whose value it actually carries.
//
//  7. REVISION / AS-OF — every observation in the distribution is the revision that was VISIBLE
//     AT THE CUTOFF, resolved per trading date by the persistence-backed reader (§7.3). This
//     package re-checks the half it can see without a database: an entry dated at or after the
//     reading is ErrLookAhead, not a silently dropped row. The fetched_at half of the bound is
//     the reader's, because only the reader knows the cutoff instant.
//
//  8. CHANGE IS RECOMPUTED PER DATE, never differenced across the window and never derived from
//     FLOW. Each historical date's CHANGE is POSITION(D) − POSITION(the previous valid
//     observation before D that is visible at the same cutoff), under the same Policy as the
//     current reading, and a date whose CHANGE is not AVAILABLE contributes nothing.
//
// Two of these are numbers and live on Policy so a caller can widen or narrow them; the other
// six are rules and are constants, because a caller who could switch them off could produce a
// percentile that is not comparable with the one beside it.

// PercentileRuleVersion labels the rule set, so a stored percentile stays interpretable after
// the rules move. Separate from FeatureVersion for the same reason AlignmentRuleVersion is.
const PercentileRuleVersion = "R15-M4-percentile-v1"

// DefaultPercentileLookbackObservations is decision 1: the reference window, in valid
// observations, INCLUDING the reading being ranked.
const DefaultPercentileLookbackObservations = 60

// DefaultMinPercentileSample is decision 2: the fewest contributing values that may produce a
// percentile.
const DefaultMinPercentileSample = 20

// PercentileIncludesCurrentObservation is decision 3, as a constant a test can assert on.
const PercentileIncludesCurrentObservation = true

// PercentileTieRule is decision 4, carried on every result so a stored figure says which rule
// produced it.
const PercentileTieRule = "AT_OR_BELOW"

// PercentileScale records that the ratio is in [0,1] rather than [0,100].
//
// internal/valuation.percentileOf is [0,1] and internal/indicator.PercentileRank is [0,100];
// they are the SAME rule at two scales. R15 uses the ratio, and percentile_test.go pins it
// against indicator.PercentileRank/100 on the same inputs so the two can never drift into
// being two rules.
const PercentileScale = "RATIO_0_1"

// PercentileStatus is the percentile's OWN absence vocabulary, and it is deliberately not
// MetricStatus.
//
// §6.1 is explicit that "not enough observations yet" is an EVALUATION question and must never
// become a source status: a caller that could write INSUFFICIENT_SAMPLE into a data-health
// record would be describing a fetch that succeeded as a fetch that failed. MetricStatus
// already carries two questions (a fetch outcome and a derivation outcome) with a stated
// justification; a third would make the switch in §6.1's warning writable.
type PercentileStatus string

const (
	// PercentileAvailable — there is a ratio.
	PercentileAvailable PercentileStatus = "AVAILABLE"
	// PercentileInsufficientSample — fewer contributing values than the configured minimum.
	// The actual and required N travel with it (§10.4).
	PercentileInsufficientSample PercentileStatus = "INSUFFICIENT_SAMPLE"
	// PercentileValueAbsent — the READING has no number, so there is nothing to rank. A
	// different fact from a thin distribution, and confusing the two would report a data gap
	// as a history gap.
	PercentileValueAbsent PercentileStatus = "VALUE_ABSENT"
)

// PercentileStatuses is the closed set.
var PercentileStatuses = []PercentileStatus{
	PercentileAvailable, PercentileInsufficientSample, PercentileValueAbsent,
}

// Valid reports whether s is one of the three.
func (s PercentileStatus) Valid() bool {
	for _, k := range PercentileStatuses {
		if k == s {
			return true
		}
	}
	return false
}

// Numeric reports whether a reader may render this as a number.
func (s PercentileStatus) Numeric() bool { return s == PercentileAvailable }

// Reasons a window entry contributed nothing. Closed set: an exclusion whose reason is not one
// of these is a rule nobody wrote down.
const (
	// ExcludedNotUsable — the day's snapshot status was not AVAILABLE or PARTIAL. Decision 6.
	ExcludedNotUsable = "SOURCE_NOT_USABLE"
	// ExcludedValueAbsent — the day is usable and this semantics' net column was absent.
	// Decision 5.
	ExcludedValueAbsent = "VALUE_ABSENT"
	// ExcludedChangeNotAvailable — the day carries a POSITION but no computable CHANGE
	// (no baseline, a stale baseline, or too little history). Decision 8.
	ExcludedChangeNotAvailable = "CHANGE_NOT_AVAILABLE"
)

// ExcludedReading is one window entry that contributed no value, and why.
type ExcludedReading struct {
	TradingDate string       `json:"trading_date"`
	Status      MetricStatus `json:"status"`
	Reason      string       `json:"reason"`
}

// ReferenceDistribution is one semantics' reference set, with everything needed to disbelieve
// it: which dates are in it, which were dropped and why, and under what window and cutoff.
type ReferenceDistribution struct {
	Semantics   ValueSemantics  `json:"semantics"`
	Institution string          `json:"institution"`
	Scope       InstrumentScope `json:"scope"`
	// AsOf is the reading's trading date. Every contributing observation is at or before it
	// (decision 7); the fetched_at half of that bound is enforced by the reader.
	AsOf string `json:"as_of"`
	// Values are the contributing readings, ASCENDING. Sorted here so a rank is a search
	// rather than a scan, and so two runs over the same data serialise identically.
	Values []float64 `json:"values"`
	// Dates are the contributing trading dates, NEWEST FIRST.
	Dates []string `json:"dates"`
	// Excluded are the window entries that contributed nothing, newest first.
	Excluded []ExcludedReading `json:"excluded,omitempty"`
	// WindowObservations is how many entries the window actually held (decision 1's L, or
	// fewer when the series is shorter).
	WindowObservations int `json:"window_observations"`
	// LookbackObservations is the configured L.
	LookbackObservations int    `json:"lookback_observations"`
	IncludesCurrent      bool   `json:"includes_current"`
	TieRule              string `json:"tie_rule"`
	Scale                string `json:"scale"`
	RuleVersion          string `json:"rule_version"`
}

// Size is the number of contributing values.
func (d ReferenceDistribution) Size() int { return len(d.Values) }

// Ratio is the share of the distribution at or below v, in [0,1].
//
// Ties count at or below (decision 4), identical to internal/indicator.PercentileRank (which
// returns the same figure ×100) and internal/valuation.percentileOf. An empty distribution has
// no ratio; the caller must not treat that as 0, which is why the second return exists.
func (d ReferenceDistribution) Ratio(v float64) (float64, bool) {
	if len(d.Values) == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return PercentileRatioAtOrBelow(d.Values, v)
}

// PercentileRatioAtOrBelow is decision 4 — the tie rule — as one function.
//
// ascending must be sorted. The result is the share of the slice at or below v, in [0,1],
// identical to internal/indicator.PercentileRank (which returns the same figure x100) and to
// internal/valuation.percentileOf. The second return is false when there is nothing to rank;
// a caller must not read that as 0.
//
// Exported for R15 M5, which ranks PCR ratios against their own distributions and must use
// THIS rule rather than a second one: with `<` instead of `<=`, a reading exactly at its own
// historical median reports below 50%, and the two percentiles rendered side by side in the
// same panel would be computed under different rules.
func PercentileRatioAtOrBelow(ascending []float64, v float64) (float64, bool) {
	if len(ascending) == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	// The values are ascending, so the first index at or past v, walked past any run of exact
	// ties, is the count of values <= v.
	i := sort.SearchFloat64s(ascending, v)
	for i < len(ascending) && ascending[i] <= v {
		i++
	}
	return float64(i) / float64(len(ascending)), true
}

// PercentileRank is one reading's position in its own distribution, or the recorded reason
// there isn't one.
//
// Ratio is a pointer for the same reason ObservedMetric.Value is: a genuine 0.02 and "no
// percentile" must not be the same bits, and Display is never a number for an absent one.
type PercentileRank struct {
	Semantics ValueSemantics `json:"semantics"`
	// Ratio is the share of the reference distribution at or below the reading, in [0,1].
	// Non-nil only when Status is AVAILABLE.
	Ratio   *float64         `json:"ratio,omitempty"`
	Status  PercentileStatus `json:"status"`
	Display string           `json:"display"`
	Reason  string           `json:"reason,omitempty"`
	// SampleSize is how many values the distribution actually held; RequiredSampleSize is
	// the configured minimum. §10.4 requires BOTH to travel with an INSUFFICIENT_SAMPLE.
	SampleSize         int `json:"sample_size"`
	RequiredSampleSize int `json:"required_sample_size"`
	// ExcludedCount is how many window entries contributed nothing, so a thin sample over a
	// long window is visible as such.
	ExcludedCount        int    `json:"excluded_count"`
	LookbackObservations int    `json:"lookback_observations"`
	WindowObservations   int    `json:"window_observations"`
	IncludesCurrent      bool   `json:"includes_current"`
	TieRule              string `json:"tie_rule"`
	Scale                string `json:"scale"`
	AsOf                 string `json:"as_of"`
	// FirstDate and LastDate bound the reference set, newest last.
	FirstDate   string `json:"first_date,omitempty"`
	LastDate    string `json:"last_date,omitempty"`
	RuleVersion string `json:"rule_version"`
}

// Value returns the ratio and whether there is one. The only supported way to read it.
func (r PercentileRank) Value() (float64, bool) {
	if r.Status != PercentileAvailable || r.Ratio == nil {
		return 0, false
	}
	return *r.Ratio, true
}

// PercentileSet is the three distributions and the three ranks, always together.
//
// Together, because the failure this file exists to prevent is one of them being computed from
// another's reference set. A caller that wanted only POSITION would otherwise have to build a
// distribution itself, and the second implementation is where the pooling comes back.
type PercentileSet struct {
	AsOf        string          `json:"as_of"`
	Institution string          `json:"institution"`
	Scope       InstrumentScope `json:"scope"`

	Flow     PercentileRank `json:"flow"`
	Position PercentileRank `json:"position"`
	Change   PercentileRank `json:"change"`

	// Distributions are the three reference sets, in FLOW, POSITION, CHANGE order. They are
	// carried rather than discarded so a percentile can be re-derived and disbelieved.
	Distributions []ReferenceDistribution `json:"distributions"`
	RuleVersion   string                  `json:"rule_version"`
}

// Distribution returns one semantics' reference set.
func (s PercentileSet) Distribution(sem ValueSemantics) (ReferenceDistribution, bool) {
	for _, d := range s.Distributions {
		if d.Semantics == sem {
			return d, true
		}
	}
	return ReferenceDistribution{}, false
}

// Rank returns one semantics' percentile.
func (s PercentileSet) Rank(sem ValueSemantics) (PercentileRank, bool) {
	switch sem {
	case SemanticsFlow:
		return s.Flow, true
	case SemanticsPosition:
		return s.Position, true
	case SemanticsChange:
		return s.Change, true
	}
	return PercentileRank{}, false
}

// Percentiles computes FLOW, POSITION and CHANGE percentiles for one reading against THREE
// separate reference distributions.
//
// history must already be point-in-time filtered by the caller — the same contract
// ResolveBaseline takes, and checked the same way: an entry dated at or after current is
// ErrLookAhead rather than a silently ignored row. What this package cannot check is the
// fetched_at half of §7.3's bound, which needs the cutoff instant and therefore belongs to the
// persistence-backed reader.
func Percentiles(current Observation, history []Observation, p Policy) (PercentileSet, error) {
	np, err := p.Normalized()
	if err != nil {
		return PercentileSet{}, err
	}
	curDate, err := parseDate(current.TradingDate)
	if err != nil {
		return PercentileSet{}, err
	}
	ordered, err := orderedHistory(current, curDate, history)
	if err != nil {
		return PercentileSet{}, err
	}

	// Decision 1 and decision 3: the window is the reading itself plus the most recent
	// earlier USABLE observations, up to L in total. A non-usable day does not consume a
	// slot; it is recorded so a window that reached back through an outage says so.
	window := make([]datedObservation, 0, np.PercentileLookbackObservations)
	var skipped []ExcludedReading
	if current.SourceStatus.Usable() {
		window = append(window, datedObservation{obs: current, date: curDate})
	} else {
		skipped = append(skipped, ExcludedReading{
			TradingDate: current.TradingDate,
			Status:      current.SourceStatus,
			Reason:      ExcludedNotUsable,
		})
	}
	for _, h := range ordered {
		if len(window) >= np.PercentileLookbackObservations {
			break
		}
		if !h.obs.SourceStatus.Usable() {
			skipped = append(skipped, ExcludedReading{
				TradingDate: h.obs.TradingDate,
				Status:      h.obs.SourceStatus,
				Reason:      ExcludedNotUsable,
			})
			continue
		}
		window = append(window, h)
	}

	set := PercentileSet{
		AsOf:        current.TradingDate,
		Institution: current.Institution,
		Scope:       current.Scope,
		RuleVersion: PercentileRuleVersion,
	}

	// Three distributions, built independently and never from each other's values.
	for _, sem := range []ValueSemantics{SemanticsFlow, SemanticsPosition, SemanticsChange} {
		dist, err := buildDistribution(sem, current, ordered, window, skipped, np)
		if err != nil {
			return PercentileSet{}, err
		}
		set.Distributions = append(set.Distributions, dist)

		reading, err := currentMetric(sem, current, history, np)
		if err != nil {
			return PercentileSet{}, err
		}
		rank := dist.rankOf(reading, np.MinPercentileSample)
		switch sem {
		case SemanticsFlow:
			set.Flow = rank
		case SemanticsPosition:
			set.Position = rank
		case SemanticsChange:
			set.Change = rank
		}
	}
	return set, nil
}

// currentMetric is the reading being ranked, taken through the SAME accessors a view uses —
// never re-derived here, because a second path to "this day's POSITION" is a second place the
// FLOW fallback §10.4 forbids could reappear.
func currentMetric(sem ValueSemantics, current Observation, history []Observation, p Policy) (ObservedMetric, error) {
	switch sem {
	case SemanticsFlow:
		return current.Flow().ObservedMetric, nil
	case SemanticsPosition:
		return current.Position().ObservedMetric, nil
	case SemanticsChange:
		hc, err := ChangeOver(current, history, 1, p)
		if err != nil {
			return ObservedMetric{}, err
		}
		return hc.Change.ObservedMetric, nil
	}
	return ObservedMetric{}, fmt.Errorf("institutional: %q is not a value semantics", sem)
}

// buildDistribution turns the window into one semantics' reference set.
//
// ordered is the whole point-in-time history, newest first; window is the subset that may
// CONTRIBUTE. The two are different on purpose: a window date's CHANGE may legitimately be
// anchored on an observation older than the window, and clipping the baseline to the window
// would silently shorten a horizon — §10.4's named defect in another costume.
func buildDistribution(sem ValueSemantics, current Observation, ordered, window []datedObservation,
	skipped []ExcludedReading, p Policy) (ReferenceDistribution, error) {

	d := ReferenceDistribution{
		Semantics:            sem,
		Institution:          current.Institution,
		Scope:                current.Scope,
		AsOf:                 current.TradingDate,
		WindowObservations:   len(window),
		LookbackObservations: p.PercentileLookbackObservations,
		IncludesCurrent:      PercentileIncludesCurrentObservation,
		TieRule:              PercentileTieRule,
		Scale:                PercentileScale,
		RuleVersion:          PercentileRuleVersion,
	}
	d.Excluded = append(d.Excluded, skipped...)

	for _, w := range window {
		v, status, reason, ok, err := readingFor(sem, w, ordered, p)
		if err != nil {
			return ReferenceDistribution{}, err
		}
		if !ok {
			d.Excluded = append(d.Excluded, ExcludedReading{
				TradingDate: w.obs.TradingDate,
				Status:      status,
				Reason:      reason,
			})
			continue
		}
		d.Values = append(d.Values, v)
		d.Dates = append(d.Dates, w.obs.TradingDate)
	}
	sort.Float64s(d.Values)
	sort.Sort(sort.Reverse(sort.StringSlice(d.Dates)))
	sort.SliceStable(d.Excluded, func(i, j int) bool {
		return d.Excluded[i].TradingDate > d.Excluded[j].TradingDate
	})
	return d, nil
}

// readingFor is one window entry's contribution to one distribution.
//
// FLOW and POSITION are the observation's own net columns. CHANGE is RECOMPUTED against the
// previous valid observation before that date (decision 8) — never differenced across the
// window, and never taken from FLOW.
func readingFor(sem ValueSemantics, w datedObservation, ordered []datedObservation, p Policy) (
	value float64, status MetricStatus, reason string, ok bool, err error) {

	switch sem {
	case SemanticsFlow:
		if w.obs.FlowNetLots == nil {
			return 0, w.obs.SourceStatus, ExcludedValueAbsent, false, nil
		}
		return *w.obs.FlowNetLots, w.obs.SourceStatus, "", true, nil
	case SemanticsPosition:
		if w.obs.PositionNetLots == nil {
			return 0, w.obs.SourceStatus, ExcludedValueAbsent, false, nil
		}
		return *w.obs.PositionNetLots, w.obs.SourceStatus, "", true, nil
	case SemanticsChange:
		before := make([]Observation, 0, len(ordered))
		for _, h := range ordered {
			if h.date.Before(w.date) {
				before = append(before, h.obs)
			}
		}
		hc, err := ChangeOver(w.obs, before, 1, p)
		if err != nil {
			return 0, "", "", false, err
		}
		v, has := hc.Change.Lots()
		if !has {
			return 0, hc.Status, ExcludedChangeNotAvailable, false, nil
		}
		return v, hc.Status, "", true, nil
	}
	return 0, "", "", false, fmt.Errorf("institutional: %q is not a value semantics", sem)
}

// rankOf places one reading in this distribution, or records why it could not be placed.
func (d ReferenceDistribution) rankOf(m ObservedMetric, minSample int) PercentileRank {
	r := PercentileRank{
		Semantics:            d.Semantics,
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
		r.FirstDate = d.Dates[len(d.Dates)-1]
		r.LastDate = d.Dates[0]
	}

	v, has := m.Lots()
	if !has {
		// The reading has no number. That is a data fact about today, not a statement about
		// the length of the history, and the two must not both render as "no percentile".
		r.Status = PercentileValueAbsent
		r.Display = string(PercentileValueAbsent)
		r.Reason = fmt.Sprintf("%s is %s for %s, so there is nothing to rank", d.Semantics,
			m.Status, d.AsOf)
		if m.Reason != "" {
			r.Reason += " (" + m.Reason + ")"
		}
		return r
	}
	if len(d.Values) < minSample {
		r.Status = PercentileInsufficientSample
		r.Display = string(PercentileInsufficientSample)
		r.Reason = fmt.Sprintf(
			"%s has %d reference observations at or before %s and %d are required; a percentile "+
				"over %d points reads as authoritative and is not one",
			d.Semantics, len(d.Values), d.AsOf, minSample, len(d.Values))
		return r
	}
	ratio, ok := d.Ratio(v)
	if !ok {
		r.Status = PercentileValueAbsent
		r.Display = string(PercentileValueAbsent)
		r.Reason = fmt.Sprintf("%s: the reading %v could not be ranked", d.Semantics, v)
		return r
	}
	r.Status = PercentileAvailable
	r.Ratio = &ratio
	r.Display = fmt.Sprintf("%.1f%%", ratio*100)
	return r
}
