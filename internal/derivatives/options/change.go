package options

import (
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// PCR changes: the difference of two RATIOS over N valid OBSERVATIONS, and every reason there
// is no difference to publish.
//
// §10.8: "PCRChangeN = CurrentPCR − BaselinePCR, over N valid OBSERVATIONS (§10.4's rule,
// reused). Differencing the numerators instead is not a PCR change. Absent when the sample is
// short, when a denominator is zero, when a rollover intervenes, or when the scopes differ."
//
// The rule really is M4's, and the reused pieces are named rather than re-derived:
// institutional.Policy supplies the horizons and both tolerance numbers,
// institutional.Policy.ToleranceDays is the one implementation of the horizon-scaled
// allowance, and the skip vocabulary is M4's exported constants. What is NOT M4's is the
// predicate for "can this observation anchor a difference" — a PCR anchors on a RATIO, and an
// observation whose ratio is absent cannot anchor one however complete its rows are.

// ChangeLabel names a window in OBSERVATIONS.
//
// The suffix is OBS and not D for M4's reason: a multi-day difference labelled CHANGE_1D is
// the defect the whole baseline rule exists to prevent, and a label that says D invites
// exactly that reading.
func ChangeLabel(n int) string { return fmt.Sprintf("PCR_CHANGE_%dOBS", n) }

// Skip reasons specific to a PCR baseline. M4's constants cover the source statuses; these two
// are about the ratio itself.
const (
	// SkipRatioAbsent — the day is usable and this metric has no ratio on it (a side was
	// absent, the denominator was zero, the settlement feed was not read). It cannot anchor
	// a difference, and the numerators must never stand in for it.
	SkipRatioAbsent = "RATIO_ABSENT"
	// SkipKeyMismatch — the observation is a different measurement. Reached only through a
	// caller error, and recorded rather than silently skipped.
	SkipKeyMismatch = "KEY_MISMATCH"
)

// SkippedObservation is one date the resolver walked past, and why.
type SkippedObservation struct {
	TradingDate string                     `json:"trading_date"`
	Status      institutional.MetricStatus `json:"status"`
	Reason      string                     `json:"reason"`
}

// BaselineResolution is what a difference is measured against, with enough detail to be
// disbelieved.
type BaselineResolution struct {
	Metric             MetricType `json:"metric"`
	Horizon            int        `json:"horizon"`
	CurrentTradingDate string     `json:"current_trading_date"`
	// PreviousTradingDate is the Nth VALID OBSERVATION back, not the Nth calendar day back.
	PreviousTradingDate string `json:"previous_trading_date,omitempty"`
	ObservationGap      int    `json:"observation_gap"`
	// CalendarGapDays is reported ALONGSIDE the observation gap, never instead of it: the
	// pair (1 observation, 4 calendar days) is the fact §10.4 exists to keep visible.
	CalendarGapDays int `json:"calendar_gap_days"`
	// EffectiveObservations is how many valid earlier observations the series actually held,
	// capped at Horizon. With Horizon 10 and 4 available it says 4 and the status says
	// INSUFFICIENT_HISTORY — the horizon is NOT shortened to 4.
	EffectiveObservations int                        `json:"effective_observations"`
	SkippedDates          []SkippedObservation       `json:"skipped_dates,omitempty"`
	BaselineStatus        institutional.MetricStatus `json:"baseline_status,omitempty"`
	BaselineRatio         *float64                   `json:"baseline_ratio,omitempty"`
	ToleranceDays         int                        `json:"tolerance_days"`
	Status                RatioStatus                `json:"status"`
	Reason                string                     `json:"reason,omitempty"`
}

// SkippedDateList is the bare dates, for a compact rendering.
func (r BaselineResolution) SkippedDateList() []string {
	out := make([]string, 0, len(r.SkippedDates))
	for _, s := range r.SkippedDates {
		out = append(out, s.TradingDate)
	}
	return out
}

// Rollover records whether the two readings are aggregates over the same contracts.
type Rollover struct {
	Crossed  bool     `json:"crossed"`
	Added    []string `json:"added_expiries,omitempty"`
	Removed  []string `json:"removed_expiries,omitempty"`
	Rule     string   `json:"rule"`
	Previous []string `json:"previous_expiries,omitempty"`
	Current  []string `json:"current_expiries,omitempty"`
}

// RatioChange is a DIFFERENCE of two ratios, and a distinct type from ObservedRatio so that a
// change cannot be rendered where a level belongs.
//
// It is the same absence discipline: Delta is nil whenever Observed is false, an observed zero
// (the ratio did not move) is Observed with a non-nil zero behind it, and Display is never "0"
// for a difference that does not exist.
type RatioChange struct {
	Metric   MetricType  `json:"metric"`
	Observed bool        `json:"observed"`
	Delta    *float64    `json:"delta,omitempty"`
	Status   RatioStatus `json:"status"`
	Display  string      `json:"display"`
	Scale    string      `json:"scale"`
	Reason   string      `json:"reason,omitempty"`
}

// Value returns the difference and whether there is one.
func (c RatioChange) Value() (float64, bool) {
	if !c.Observed || c.Delta == nil {
		return 0, false
	}
	return *c.Delta, true
}

// Absent is the complement of Observed.
func (c RatioChange) Absent() bool { return !c.Observed }

func newObservedChange(m MetricType, v float64) RatioChange {
	r := newObservedRatio(m, v)
	if !r.Observed {
		return RatioChange{Metric: m, Status: r.Status, Display: r.Display,
			Scale: RatioScale, Reason: r.Reason}
	}
	d := v
	display := formatRatio(v)
	if v > 0 {
		display = "+" + display
	}
	return RatioChange{Metric: m, Observed: true, Delta: &d, Status: RatioAvailable,
		Display: display, Scale: RatioScale}
}

func newAbsentChange(m MetricType, status RatioStatus, reason string) RatioChange {
	r := newAbsentRatio(m, status, reason)
	return RatioChange{Metric: m, Status: r.Status, Display: r.Display, Scale: RatioScale,
		Reason: r.Reason}
}

// HorizonChange is one PCRChangeN with everything it has to carry.
type HorizonChange struct {
	Metric                MetricType         `json:"metric"`
	Key                   ObservationKey     `json:"key"`
	Horizon               int                `json:"horizon"`
	Label                 string             `json:"label"`
	Change                RatioChange        `json:"change"`
	CurrentTradingDate    string             `json:"current_trading_date"`
	PreviousTradingDate   string             `json:"previous_trading_date,omitempty"`
	EffectiveObservations int                `json:"effective_observations"`
	CalendarGapDays       int                `json:"calendar_gap_days"`
	Status                RatioStatus        `json:"status"`
	Rollover              Rollover           `json:"rollover"`
	Resolution            BaselineResolution `json:"resolution"`
	Warnings              []Warning          `json:"warnings,omitempty"`
}

type datedObservation struct {
	obs  Observation
	date time.Time
}

// orderedHistory validates the series and sorts it newest first.
//
// Every entry must be the SAME measurement — same product, family, session and selection
// policy — and must be EARLIER than the reading. A history entry dated at or after the current
// one is ErrLookAhead rather than a silently ignored row: §5's whole subject is that a
// look-ahead which returns a number cannot be detected downstream.
func orderedHistory(current Observation, curDate time.Time, history []Observation) (
	[]datedObservation, error) {

	out := make([]datedObservation, 0, len(history))
	seen := map[string]bool{}
	for _, h := range history {
		if !current.Key.Same(h.Key) {
			return nil, fmt.Errorf("%w: %s and %s", ErrKeyMismatch, current.Key, h.Key)
		}
		d, err := parseDate(h.TradingDate)
		if err != nil {
			return nil, err
		}
		if !d.Before(curDate) {
			return nil, fmt.Errorf("%w: %s is not earlier than %s",
				ErrLookAhead, h.TradingDate, current.TradingDate)
		}
		if seen[h.TradingDate] {
			return nil, fmt.Errorf("%w: two observations for %s on %s",
				ErrDuplicateObservation, current.Key, h.TradingDate)
		}
		seen[h.TradingDate] = true
		out = append(out, datedObservation{obs: h, date: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.After(out[j].date) })
	return out, nil
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(institutional.DateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", ErrBadDate, s)
	}
	return t, nil
}

func calendarDays(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

// skipReason is the ONE place that decides whether an earlier observation can anchor a PCR
// difference.
//
// The source-status half is M4's ruling, reused verbatim including the one that is not in
// §10.4's list: STALE is skipped and that is not configurable, because a reading whose
// freshness contract already failed would launder that failure into every change computed
// from it.
func skipReason(m MetricType, current Observation, o Observation) (string, bool) {
	if !current.Key.Same(o.Key) {
		return SkipKeyMismatch, true
	}
	switch o.SourceStatus {
	case institutional.StatusNoSession:
		return institutional.SkipNoSession, true
	case institutional.StatusNotPublished:
		return institutional.SkipNotPublished, true
	case institutional.StatusError:
		return institutional.SkipError, true
	case institutional.StatusMissing:
		return institutional.SkipMissing, true
	case institutional.StatusStale:
		return institutional.SkipStale, true
	case institutional.StatusNotAvailable:
		return institutional.SkipNotAvailable, true
	}
	p, err := o.PCR(m)
	if err != nil || p.Ratio.Absent() {
		// The rows may be perfectly good and the ratio still absent — one side missing, a
		// zero denominator, no settlement read. Differencing against the numerators instead
		// is not a PCR change (§10.8).
		return SkipRatioAbsent, true
	}
	return "", false
}

// ResolveBaseline walks a series backwards and returns the Nth valid observation for a metric.
//
// history is ALREADY point-in-time filtered by the caller; this package does no querying and
// takes the as-of bound on trust exactly once, here, where it is checked.
func ResolveBaseline(m MetricType, current Observation, history []Observation, n int,
	p institutional.Policy) (BaselineResolution, error) {

	np, err := p.Normalized()
	if err != nil {
		return BaselineResolution{}, err
	}
	if !m.Valid() {
		return BaselineResolution{}, fmt.Errorf("options: %q is not a metric type", m)
	}
	if n < 1 {
		return BaselineResolution{}, fmt.Errorf(
			"options: horizon %d is not a positive observation count", n)
	}
	curDate, err := parseDate(current.TradingDate)
	if err != nil {
		return BaselineResolution{}, err
	}
	ordered, err := orderedHistory(current, curDate, history)
	if err != nil {
		return BaselineResolution{}, err
	}

	res := BaselineResolution{
		Metric:             m,
		Horizon:            n,
		CurrentTradingDate: current.TradingDate,
		ToleranceDays:      np.ToleranceDays(n),
	}

	found := 0
	for _, h := range ordered {
		if reason, skip := skipReason(m, current, h.obs); skip {
			res.SkippedDates = append(res.SkippedDates, SkippedObservation{
				TradingDate: h.obs.TradingDate,
				Status:      h.obs.SourceStatus,
				Reason:      reason,
			})
			continue
		}
		found++
		if found < n {
			continue
		}
		bp, err := h.obs.PCR(m)
		if err != nil {
			return BaselineResolution{}, err
		}
		v, _ := bp.Ratio.Value()
		ratio := v
		res.PreviousTradingDate = h.obs.TradingDate
		res.ObservationGap = found
		res.EffectiveObservations = found
		res.CalendarGapDays = calendarDays(h.date, curDate)
		res.BaselineStatus = h.obs.SourceStatus
		res.BaselineRatio = &ratio
		res.Status = RatioAvailable
		break
	}

	if res.Status != RatioAvailable {
		res.EffectiveObservations = found
		res.Status = RatioInsufficientHistory
		res.Reason = fmt.Sprintf(
			"%s %s needs %d valid observations before %s and the series has %d; a "+
				"%d-observation span must not be reported under a %d-observation label",
			m, current.Key, n, current.TradingDate, found, found, n)
		return res, nil
	}
	if res.CalendarGapDays > res.ToleranceDays {
		res.Status = RatioStaleBaseline
		res.Reason = fmt.Sprintf(
			"the %d-observation baseline is %s, %d calendar days back, beyond the %d-day tolerance",
			n, res.PreviousTradingDate, res.CalendarGapDays, res.ToleranceDays)
	}
	return res, nil
}

// ChangeOver computes PCR(D) − PCR(N valid observations earlier) for one metric.
//
// Ratios on both sides, never numerators: differencing the numerators produces a number every
// day and it is not a PCR change. A rollover between the two is ABSENT with ROLLOVER_BOUNDARY
// rather than a difference between two different things — OI falling to zero at settlement is
// the contract ending, not the market collapsing, and M6 reads this metadata to tell an
// expiring wall from a collapsing one.
func ChangeOver(m MetricType, current Observation, history []Observation, n int,
	p institutional.Policy) (HorizonChange, error) {

	res, err := ResolveBaseline(m, current, history, n, p)
	if err != nil {
		return HorizonChange{}, err
	}
	cur, err := current.PCR(m)
	if err != nil {
		return HorizonChange{}, err
	}

	hc := HorizonChange{
		Metric:                m,
		Key:                   current.Key,
		Horizon:               n,
		Label:                 ChangeLabel(n),
		CurrentTradingDate:    current.TradingDate,
		PreviousTradingDate:   res.PreviousTradingDate,
		EffectiveObservations: res.EffectiveObservations,
		CalendarGapDays:       res.CalendarGapDays,
		Resolution:            res,
		Rollover:              Rollover{Rule: RolloverRule, Current: cur.Identity.Expiries},
	}

	// The current side first: a change against a ratio that does not exist is not a change,
	// whatever the history holds.
	curV, ok := cur.Ratio.Value()
	if !ok {
		hc.Change = newAbsentChange(m, cur.Ratio.Status,
			"the current "+string(m)+" is absent ("+cur.Ratio.Reason+")")
		hc.Status = hc.Change.Status
		hc.Resolution.Status = hc.Change.Status
		hc.Resolution.Reason = hc.Change.Reason
		return hc, nil
	}

	if res.Status != RatioAvailable {
		hc.Change = newAbsentChange(m, res.Status, res.Reason)
		hc.Status = hc.Change.Status
		return hc, nil
	}

	baseObs, err := observationOn(history, res.PreviousTradingDate)
	if err != nil {
		return HorizonChange{}, err
	}
	base, err := baseObs.PCR(m)
	if err != nil {
		return HorizonChange{}, err
	}
	hc.Rollover.Previous = base.Identity.Expiries
	if !cur.Identity.Same(base.Identity) {
		added, removed := cur.Identity.Diff(base.Identity)
		hc.Rollover.Crossed = true
		hc.Rollover.Added, hc.Rollover.Removed = added, removed
		reason := fmt.Sprintf(
			"%s on %s is an aggregate over %v and on %s over %v (added %v, removed %v); a "+
				"difference across a contract-identity change is not a change",
			m, current.TradingDate, cur.Identity.Expiries, base.TradingDate,
			base.Identity.Expiries, added, removed)
		hc.Change = newAbsentChange(m, RatioRolloverBoundary, reason)
		hc.Status = hc.Change.Status
		hc.Warnings = append(hc.Warnings, Warning{
			Code: WarnRolloverBoundary, Metric: m, Subject: hc.Label, Message: reason,
		})
		return hc, nil
	}

	hc.Change = newObservedChange(m, curV-*res.BaselineRatio)
	hc.Status = hc.Change.Status
	return hc, nil
}

// Change is ChangeOver with N = 1 — the same code path as every other horizon, because two
// implementations of "the previous one" is how one of them ends up meaning D − 1.
func Change(m MetricType, current Observation, history []Observation, p institutional.Policy) (
	RatioChange, BaselineResolution, error) {

	hc, err := ChangeOver(m, current, history, 1, p)
	if err != nil {
		return RatioChange{}, BaselineResolution{}, err
	}
	return hc.Change, hc.Resolution, nil
}

// ChangeHorizons computes every window in the policy, in ascending order.
func ChangeHorizons(m MetricType, current Observation, history []Observation,
	p institutional.Policy) ([]HorizonChange, error) {

	np, err := p.Normalized()
	if err != nil {
		return nil, err
	}
	out := make([]HorizonChange, 0, len(np.Horizons))
	for _, n := range np.Horizons {
		hc, err := ChangeOver(m, current, history, n, np)
		if err != nil {
			return nil, err
		}
		out = append(out, hc)
	}
	return out, nil
}

func observationOn(history []Observation, date string) (Observation, error) {
	for _, h := range history {
		if h.TradingDate == date {
			return h, nil
		}
	}
	return Observation{}, fmt.Errorf(
		"options: the baseline resolved to %s and no observation for that date is in the "+
			"history — the resolver and the series disagree", date)
}
