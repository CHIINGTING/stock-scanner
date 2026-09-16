package institutional

import (
	"fmt"
	"sort"
	"time"
)

// CHANGE, its baseline, and the horizons — the half of M4 that a plausible shortcut ruins.
//
// §10.4: "CHANGE's baseline is NOT D − 1. It is the most recent EARLIER trading date that has,
// for the same dataset, investor, instrument scope, session policy and value semantics, an
// observation that is AVAILABLE and visible at the current as-of cutoff." Across a long weekend
// or a typhoon closure that can be four days back, and a multi-day difference labelled CHANGE_1D
// is the defect this file exists to prevent.

// Policy is every knob in one place, so a caller configures the layer rather than each call.
type Policy struct {
	// Horizons are the ChangeN windows to compute, in OBSERVATIONS. Sorted and deduplicated
	// on validation; 1 is always present because CHANGE is ChangeN with N=1 and nothing is
	// allowed to make them two different rules.
	Horizons []int
	// MaxBaselineCalendarGapDays is the tolerance for the single-step baseline: if the
	// previous valid observation is further back than this many CALENDAR days, the answer is
	// STALE_BASELINE rather than a number.
	//
	// The default is 10, which is Lunar New Year. The exchange closes for about nine calendar
	// days, and a CHANGE across that gap is a real, computable, interesting figure; one across
	// a three-week hole in this repository's own archive is not.
	MaxBaselineCalendarGapDays int
	// CalendarDaysPerObservation extends that tolerance for a longer horizon: the allowance
	// for ChangeN is MaxBaselineCalendarGapDays + (N−1) x this. A Change10 normally spans
	// about 14 calendar days, so the default 3 leaves room for a holiday week without
	// letting a half-empty archive through.
	//
	// §10.4 specifies the tolerance for the single-step baseline only; the extension is this
	// package's choice, made explicit here rather than hidden in a constant.
	CalendarDaysPerObservation int
	// AcceptPartialWithPosition decides whether a PARTIAL observation that DOES carry the
	// POSITION column may anchor a CHANGE.
	//
	// §10.4 skips "a PARTIAL snapshot lacking the POSITION columns", which reads as keeping
	// one that has them, while the sentence above it says the baseline must be AVAILABLE.
	// The two do not quite agree; the default follows the more specific rule (accept), and
	// the resolution records which observation it used and with what status, so a caller who
	// disagrees can both switch it off and see when it mattered.
	AcceptPartialWithPosition bool
	// MinimumSignificanceLots is the |value| below which a component counts as NEUTRAL rather
	// than directional in the alignment rule. 0 means any non-zero lot count has a direction.
	MinimumSignificanceLots float64
	// MinAlignmentComponents is how many components must be observed before alignment reports
	// anything but INSUFFICIENT_DATA. Default 2: one component agrees with itself.
	MinAlignmentComponents int
	// PercentileLookbackObservations is the reference window for every percentile, in VALID
	// OBSERVATIONS — never calendar days, for the same reason the horizons are not. Default
	// DefaultPercentileLookbackObservations (60, about a trading quarter).
	//
	// It is the same number for FLOW, POSITION and CHANGE, and that is not the same thing as
	// sharing a distribution: three windows of the same LENGTH over the same DATES still
	// produce three separate reference sets (percentile.go, decision 1).
	PercentileLookbackObservations int
	// MinPercentileSample is the fewest contributing values that may produce a percentile.
	// Below it the answer is INSUFFICIENT_SAMPLE with the actual and required N, never a
	// number (§10.4). Default DefaultMinPercentileSample (20).
	MinPercentileSample int
}

// DefaultHorizons are §10.4's four windows, in observations.
var DefaultHorizons = []int{1, 3, 5, 10}

// DefaultPolicy is the shipped configuration.
func DefaultPolicy() Policy {
	return Policy{
		Horizons:                   append([]int{}, DefaultHorizons...),
		MaxBaselineCalendarGapDays: 10,
		CalendarDaysPerObservation: 3,
		AcceptPartialWithPosition:  true,
		MinimumSignificanceLots:    0,
		MinAlignmentComponents:     2,

		PercentileLookbackObservations: DefaultPercentileLookbackObservations,
		MinPercentileSample:            DefaultMinPercentileSample,
	}
}

// Normalized returns a validated copy: horizons sorted, deduplicated, 1 guaranteed present,
// and the zero value of each knob replaced by its default so a caller passing Policy{} gets the
// shipped behaviour rather than a tolerance of zero days.
func (p Policy) Normalized() (Policy, error) {
	d := DefaultPolicy()
	if len(p.Horizons) == 0 {
		p.Horizons = d.Horizons
	}
	if p.MaxBaselineCalendarGapDays == 0 {
		p.MaxBaselineCalendarGapDays = d.MaxBaselineCalendarGapDays
	}
	if p.CalendarDaysPerObservation == 0 {
		p.CalendarDaysPerObservation = d.CalendarDaysPerObservation
	}
	if p.MinAlignmentComponents == 0 {
		p.MinAlignmentComponents = d.MinAlignmentComponents
	}
	if p.PercentileLookbackObservations == 0 {
		p.PercentileLookbackObservations = d.PercentileLookbackObservations
	}
	if p.MinPercentileSample == 0 {
		p.MinPercentileSample = d.MinPercentileSample
	}
	if p.MaxBaselineCalendarGapDays < 0 || p.CalendarDaysPerObservation < 0 {
		return Policy{}, fmt.Errorf("institutional: negative tolerance in policy %+v", p)
	}
	if p.PercentileLookbackObservations < 1 {
		return Policy{}, fmt.Errorf(
			"institutional: percentile lookback %d is not a positive observation count",
			p.PercentileLookbackObservations)
	}
	if p.MinPercentileSample < 1 {
		return Policy{}, fmt.Errorf(
			"institutional: minimum percentile sample %d is not a positive count",
			p.MinPercentileSample)
	}
	if p.MinPercentileSample > p.PercentileLookbackObservations {
		// The window is the ceiling on the sample, so a minimum above it is a policy that
		// can never be satisfied — every percentile would be INSUFFICIENT_SAMPLE forever,
		// and the panel would report a permanent data gap that is really a typo.
		return Policy{}, fmt.Errorf(
			"institutional: minimum percentile sample %d exceeds the %d-observation lookback "+
				"window, so no percentile could ever be published",
			p.MinPercentileSample, p.PercentileLookbackObservations)
	}
	if p.MinimumSignificanceLots < 0 {
		return Policy{}, fmt.Errorf(
			"institutional: minimum significance must be a magnitude, got %v", p.MinimumSignificanceLots)
	}
	seen := map[int]bool{1: true}
	out := []int{1}
	for _, n := range p.Horizons {
		if n < 1 {
			return Policy{}, fmt.Errorf("institutional: horizon %d is not a positive observation count", n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	p.Horizons = out
	return p, nil
}

// ToleranceDays is the calendar allowance for a baseline n valid observations back:
// MaxBaselineCalendarGapDays + (n−1) x CalendarDaysPerObservation.
//
// Exported for R15 M5, whose PCR baselines walk the same rule over a different series. A
// second copy of this formula is how a flat single-hop tolerance ends up marking every
// Change10 stale — §10.4's named defect, which is exactly what the (n−1) term prevents.
func (p Policy) ToleranceDays(n int) int {
	return p.MaxBaselineCalendarGapDays + (n-1)*p.CalendarDaysPerObservation
}

// toleranceDays is ToleranceDays under this package's own spelling.
func (p Policy) toleranceDays(n int) int { return p.ToleranceDays(n) }

// Why an earlier observation was passed over. Closed set: a skip whose reason is not one of
// these is a rule nobody wrote down.
const (
	// SkipNoSession — the exchange did not trade. Weekends and holidays reach the resolver
	// either as a NO_SESSION row or as no row at all, and both are handled: the gap is
	// measured in calendar days from the dates, so an absent weekend is visible even when
	// nothing was recorded for it.
	SkipNoSession = "NO_SESSION"
	// SkipNotPublished — the session happened, the file was not out when this was observed.
	SkipNotPublished = "NOT_PUBLISHED"
	// SkipError — the fetch or parse failed for that date.
	SkipError = "ERROR"
	// SkipMissing — nothing was observed for that date.
	SkipMissing = "MISSING"
	// SkipStale — the observation was already too old for its own dataset when recorded.
	SkipStale = "STALE"
	// SkipNotAvailable — no authorized source for that date.
	SkipNotAvailable = "NOT_AVAILABLE"
	// SkipPartialWithoutPosition — a PARTIAL snapshot whose POSITION column is absent. It
	// may carry a perfectly good FLOW, and that is precisely why it must be skipped rather
	// than used: differencing POSITION against FLOW is §10.4's named defect.
	SkipPartialWithoutPosition = "PARTIAL_WITHOUT_POSITION"
	// SkipPositionAbsent — an otherwise AVAILABLE observation with no POSITION value.
	SkipPositionAbsent = "POSITION_ABSENT"
)

// SkippedObservation is one date the resolver walked past, and why.
type SkippedObservation struct {
	TradingDate string       `json:"trading_date"`
	Status      MetricStatus `json:"status"`
	Reason      string       `json:"reason"`
}

// BaselineResolution is the answer to "what is this difference measured against", with enough
// detail that the answer can be disbelieved.
type BaselineResolution struct {
	// Horizon is N, in valid OBSERVATIONS.
	Horizon            int    `json:"horizon"`
	CurrentTradingDate string `json:"current_trading_date"`
	// PreviousTradingDate is the date of the observation the difference is measured against
	// — for Horizon N, the Nth valid observation back, not the Nth calendar day back. Empty
	// when none was found.
	PreviousTradingDate string `json:"previous_trading_date,omitempty"`
	// ObservationGap is how many valid observations back the baseline is. Equal to Horizon
	// when one was found; it is stated rather than assumed because the pair
	// (ObservationGap=1, CalendarGapDays=4) is the fact §10.4 exists to keep visible.
	ObservationGap int `json:"observation_gap"`
	// CalendarGapDays is the plain date difference. Reported alongside ObservationGap,
	// never instead of it.
	CalendarGapDays int `json:"calendar_gap_days"`
	// EffectiveObservations is how many valid earlier observations the series actually
	// contains, capped at Horizon. With Horizon 10 and 4 available it says 4, and the status
	// says INSUFFICIENT_HISTORY — the horizon is NOT shortened to 4.
	EffectiveObservations int `json:"effective_observations"`
	// SkippedDates are every earlier date walked past to reach the baseline, with reasons.
	SkippedDates []SkippedObservation `json:"skipped_dates,omitempty"`
	// BaselineStatus is the source status of the observation used, so a caller can see when
	// a PARTIAL-with-POSITION was accepted under the policy.
	BaselineStatus MetricStatus `json:"baseline_status,omitempty"`
	// BaselinePositionLots is the anchor value, for audit.
	BaselinePositionLots *float64 `json:"baseline_position_lots,omitempty"`
	ToleranceDays        int      `json:"tolerance_days"`
	// Status is AVAILABLE, INSUFFICIENT_HISTORY, STALE_BASELINE, or the current
	// observation's own non-usable status when there was nothing to anchor.
	Status MetricStatus `json:"status"`
	Reason string       `json:"reason,omitempty"`
}

// SkippedDateList is the bare dates, in the order they were walked, for a compact rendering.
func (r BaselineResolution) SkippedDateList() []string {
	out := make([]string, 0, len(r.SkippedDates))
	for _, s := range r.SkippedDates {
		out = append(out, s.TradingDate)
	}
	return out
}

// ResolveBaseline walks a series backwards from current and returns the Nth valid observation.
//
// history is ALREADY point-in-time filtered by the caller — this package does no querying, and
// takes the as-of bound on trust exactly once, here, where it is checked: an entry dated on or
// after current is ErrLookAhead rather than a silently ignored row.
//
// Order does not matter; duplicates do, and are an error. Everything else that is not usable is
// skipped WITH A REASON.
func ResolveBaseline(current Observation, history []Observation, n int, p Policy) (BaselineResolution, error) {
	p, err := p.Normalized()
	if err != nil {
		return BaselineResolution{}, err
	}
	if n < 1 {
		return BaselineResolution{}, fmt.Errorf(
			"institutional: horizon %d is not a positive observation count", n)
	}
	curDate, err := parseDate(current.TradingDate)
	if err != nil {
		return BaselineResolution{}, err
	}

	res := BaselineResolution{
		Horizon:            n,
		CurrentTradingDate: current.TradingDate,
		ToleranceDays:      p.toleranceDays(n),
	}

	ordered, err := orderedHistory(current, curDate, history)
	if err != nil {
		return BaselineResolution{}, err
	}

	found := 0
	for _, h := range ordered {
		if reason, ok := skipReason(h.obs, p); ok {
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
		res.PreviousTradingDate = h.obs.TradingDate
		res.ObservationGap = found
		res.EffectiveObservations = found
		res.CalendarGapDays = calendarDays(h.date, curDate)
		res.BaselineStatus = h.obs.SourceStatus
		res.BaselinePositionLots = copyLots(h.obs.PositionNetLots)
		res.Status = StatusAvailable
		break
	}

	if res.Status != StatusAvailable {
		res.EffectiveObservations = found
		res.Status = StatusInsufficientHistory
		res.Reason = fmt.Sprintf(
			"%s %s needs %d valid observations before %s and the series has %d; a %d-observation "+
				"span must not be reported under a %d-observation label",
			current.Institution, current.Scope, n, current.TradingDate, found, found, n)
		return res, nil
	}
	if res.CalendarGapDays > res.ToleranceDays {
		res.Status = StatusStaleBaseline
		res.Reason = fmt.Sprintf(
			"the %d-observation baseline is %s, %d calendar days back, beyond the %d-day tolerance",
			n, res.PreviousTradingDate, res.CalendarGapDays, res.ToleranceDays)
	}
	return res, nil
}

type datedObservation struct {
	obs  Observation
	date time.Time
}

// orderedHistory validates the series and sorts it newest first.
func orderedHistory(current Observation, curDate time.Time, history []Observation) ([]datedObservation, error) {
	out := make([]datedObservation, 0, len(history))
	seen := map[string]bool{}
	for _, h := range history {
		if err := current.sameSeries(h); err != nil {
			return nil, err
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
			return nil, fmt.Errorf("%w: two observations for %s %s on %s",
				ErrDuplicateObservation, current.Institution, current.Scope, h.TradingDate)
		}
		seen[h.TradingDate] = true
		out = append(out, datedObservation{obs: h, date: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.After(out[j].date) })
	return out, nil
}

// skipReason is the ONE place that decides whether an earlier observation can anchor a CHANGE.
func skipReason(o Observation, p Policy) (string, bool) {
	switch o.SourceStatus {
	case StatusNoSession:
		return SkipNoSession, true
	case StatusNotPublished:
		return SkipNotPublished, true
	case StatusError:
		return SkipError, true
	case StatusMissing:
		return SkipMissing, true
	case StatusStale:
		// Not in §10.4's skip list, and skipped anyway: the sentence above it requires an
		// AVAILABLE baseline, and a figure the acquisition layer already flagged as older
		// than its own dataset expects is not one.
		return SkipStale, true
	case StatusNotAvailable:
		return SkipNotAvailable, true
	case StatusPartial:
		if !o.HasPosition() {
			return SkipPartialWithoutPosition, true
		}
		if !p.AcceptPartialWithPosition {
			return SkipPartialWithoutPosition, true
		}
		return "", false
	}
	if !o.HasPosition() {
		// AVAILABLE with no POSITION: the column was absent for this contract. It cannot
		// anchor a POSITION difference, and its FLOW must never stand in.
		return SkipPositionAbsent, true
	}
	return "", false
}

// HorizonChange is one ChangeN, with everything §10.4 requires it to carry.
type HorizonChange struct {
	// Horizon is N in VALID OBSERVATIONS, never calendar days.
	Horizon int `json:"horizon"`
	// Label is the name a report prints. The suffix is OBS, not D: §10.4 names "a multi-day
	// difference labelled CHANGE_1D" as the defect the whole baseline rule exists to prevent,
	// and a label that says D invites exactly that reading.
	Label                 string                  `json:"label"`
	Change                PositionChangeContracts `json:"change"`
	CurrentTradingDate    string                  `json:"current_trading_date"`
	PreviousTradingDate   string                  `json:"previous_trading_date,omitempty"`
	EffectiveObservations int                     `json:"effective_observations"`
	CalendarGapDays       int                     `json:"calendar_gap_days"`
	Status                MetricStatus            `json:"status"`
	Resolution            BaselineResolution      `json:"resolution"`
}

// HorizonLabel names a window in observations.
func HorizonLabel(n int) string { return fmt.Sprintf("CHANGE_%dOBS", n) }

// ChangeOver computes POSITION(D) − POSITION(N valid observations earlier).
//
// POSITION on both sides. Never FLOW on either: differencing FLOW produces a number on every
// day of the year and it is not a position change.
//
// With fewer than N valid observations the value is ABSENT and the status is
// INSUFFICIENT_HISTORY. The horizon is never shortened to fit, and the value is never 0.
func ChangeOver(current Observation, history []Observation, n int, p Policy) (HorizonChange, error) {
	res, err := ResolveBaseline(current, history, n, p)
	if err != nil {
		return HorizonChange{}, err
	}
	hc := HorizonChange{
		Horizon:               n,
		Label:                 HorizonLabel(n),
		CurrentTradingDate:    current.TradingDate,
		PreviousTradingDate:   res.PreviousTradingDate,
		EffectiveObservations: res.EffectiveObservations,
		CalendarGapDays:       res.CalendarGapDays,
		Resolution:            res,
	}

	prov := current.Prov()
	cur := current.Position()

	// The current side first: a CHANGE against a POSITION that does not exist is not a
	// CHANGE, whatever the history holds.
	if _, ok := cur.Lots(); !ok {
		hc.Change = changeOf(newAbsent(SemanticsChange, cur.Status,
			"current POSITION is absent ("+cur.Reason+")", prov))
		hc.Status = cur.Status
		hc.Resolution.Status = cur.Status
		hc.Resolution.Reason = hc.Change.Reason
		return hc, nil
	}

	switch res.Status {
	case StatusAvailable:
		curLots, _ := cur.Lots()
		hc.Change = changeOf(newObserved(SemanticsChange, curLots-*res.BaselinePositionLots, prov))
	default:
		hc.Change = changeOf(newAbsent(SemanticsChange, res.Status, res.Reason, prov))
	}
	hc.Status = hc.Change.Status
	return hc, nil
}

// Change is CHANGE: ChangeOver with N = 1, i.e. against the PREVIOUS VALID OBSERVATION.
//
// It is the same code path as every other horizon on purpose. Two implementations of "the
// previous one" is how one of them ends up meaning D − 1.
func Change(current Observation, history []Observation, p Policy) (PositionChangeContracts, BaselineResolution, error) {
	hc, err := ChangeOver(current, history, 1, p)
	if err != nil {
		return PositionChangeContracts{}, BaselineResolution{}, err
	}
	return hc.Change, hc.Resolution, nil
}

// ChangeHorizons computes every window in the policy, in ascending order.
func ChangeHorizons(current Observation, history []Observation, p Policy) ([]HorizonChange, error) {
	np, err := p.Normalized()
	if err != nil {
		return nil, err
	}
	out := make([]HorizonChange, 0, len(np.Horizons))
	for _, n := range np.Horizons {
		hc, err := ChangeOver(current, history, n, np)
		if err != nil {
			return nil, err
		}
		out = append(out, hc)
	}
	return out, nil
}
