package institutional

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// InstitutionalDerivativesView: the three institutions, the three-institution total, the
// evidence and the warnings, for one trading date, one session and one instrument scope.
//
// It is M4's whole answer and it is PURE — assembled from observations that a caller has
// already read point-in-time. There is no report wiring, no config flag and no rendering here:
// M10 owns integration, and a view that reached for a template would make the domain layer
// untestable without one.
//
// What the type refuses to do is as important as what it does. It never publishes a total from
// a partial set (§10.4), never collapses the per-institution alignments into a verdict of its
// own, and never carries a bare float64 — every quantity on it is an ObservedMetric or a
// PercentileRank, both of which can say "no number, and here is which kind of no".

// InstitutionalViewVersion labels the assembly rules.
const InstitutionalViewVersion = "R15-M4-view-v1"

// ErrNoSeries — a view was asked for with no institutions at all.
var ErrNoSeries = errors.New("institutional: a derivatives view needs at least one institution")

// InstitutionSeries is one institution's point-in-time input: the reading being viewed and the
// history it is computed against.
//
// The two arrive together because they must have been read together, under ONE as-of cutoff.
// History that was resolved at a different cutoff would give a CHANGE one revision and a
// percentile another, and nothing downstream could tell.
type InstitutionSeries struct {
	// Current is the reading. Its trading date, session, dataset and scope are the view's.
	Current Observation
	// History is every earlier observation the reader returned, INCLUDING the days that
	// were not usable: a NOT_PUBLISHED day skipped in silence is the difference between a
	// CHANGE_1OBS that spans one session and one that spans four.
	History []Observation
}

// Warning codes. A closed set, because a warning whose code is not one of these is a condition
// nobody wrote a rule for, and a caller cannot switch on prose.
const (
	// WarnAggregatePartial — the three-institution total has no value. §10.4: 外資 being
	// present is not grounds for calling the total available.
	WarnAggregatePartial = "AGGREGATE_PARTIAL"
	// WarnMissingInstitution — one of the three did not contribute at all.
	WarnMissingInstitution = "MISSING_INSTITUTION"
	// WarnSourceNotUsable — an institution's snapshot status was not AVAILABLE or PARTIAL,
	// so it has readings for nothing.
	WarnSourceNotUsable = "SOURCE_NOT_USABLE"
	// WarnPartialSnapshot — the reading came from a PARTIAL snapshot. §6.2: the stored rows
	// are usable, an aggregate that depends on completeness is not.
	WarnPartialSnapshot = "PARTIAL_SNAPSHOT"
	// WarnScopeProductAbsent — the fetch succeeded and the scope's product was not in the
	// response. A fetch that worked and an observation that did not.
	WarnScopeProductAbsent = "SCOPE_PRODUCT_ABSENT"
	// WarnPositionAbsent — POSITION has no number for this institution, so CHANGE has no
	// current side. Never filled from FLOW (§10.4).
	WarnPositionAbsent = "POSITION_ABSENT"
	// WarnStaleBaseline — a horizon resolved to a baseline beyond the tolerance.
	WarnStaleBaseline = "STALE_BASELINE"
	// WarnInsufficientHistory — a horizon has fewer than N valid observations behind it.
	WarnInsufficientHistory = "INSUFFICIENT_HISTORY"
	// WarnObservationGap — CHANGE_1OBS spans more calendar days than a single session. The
	// number is correct; a reader who assumes "yesterday" is not.
	WarnObservationGap = "OBSERVATION_GAP"
	// WarnInsufficientSample — a percentile could not be published from the available
	// reference observations.
	WarnInsufficientSample = "INSUFFICIENT_SAMPLE"
	// WarnBaselinesDiffer — the institutions' CHANGE baselines are not the same date, so the
	// aggregate CHANGE is not the difference of the sums.
	WarnBaselinesDiffer = "AGGREGATE_BASELINES_DIFFER"
)

// AggregateLabel is the "institution" an evidence line for the three-institution total carries.
//
// A label rather than an empty string: an evidence list in which the total's lines have no
// subject is one filter away from being rendered as a fourth institution.
const AggregateLabel = "TOTAL"

// SingleSessionCalendarDays is the calendar gap a CHANGE_1OBS has when the previous valid
// observation really was the previous session, and nothing was recorded in between.
//
// It is 3 and not 1 because a Friday-to-Monday CHANGE is an ordinary one-session difference that
// spans three calendar days, and warning on every Monday would train a reader to ignore the
// warning that matters. It is the BACKSTOP for a gap this repository has no record of; the
// primary trigger is skippedSessions, which counts the days it does.
const SingleSessionCalendarDays = 3

// skippedSessions counts the dates a baseline walked past that were SESSIONS — the exchange
// traded and this repository does not have the reading.
//
// NO_SESSION is deliberately not counted. A weekend is not a gap in the data; it is the calendar,
// and acquisition writes a NO_SESSION health record for every one of them. Counting those would
// raise a warning on every Monday, which is exactly how a warning stops being read.
func skippedSessions(res BaselineResolution) []string {
	var out []string
	for _, s := range res.SkippedDates {
		if s.Reason == SkipNoSession {
			continue
		}
		out = append(out, s.TradingDate)
	}
	return out
}

// Warning is one condition a reader must see before acting on the numbers.
type Warning struct {
	Code string `json:"code"`
	// Institution is empty for a warning about the view or the total.
	Institution string `json:"institution,omitempty"`
	// Subject names what inside the institution the warning is about: a semantics, a horizon
	// label, or empty.
	Subject string `json:"subject,omitempty"`
	Message string `json:"message"`
}

// EvidenceLine is one number as a reader sees it: what it is, what it says, how unusual it is,
// and what it was measured against.
//
// Evidence rather than a rendering: it holds the pre-formatted Display that ObservedMetric
// already guarantees is never "0" for an absent value, and every field a caller would
// otherwise recompute — which is where the recomputation would disagree.
type EvidenceLine struct {
	Institution string         `json:"institution"`
	Label       string         `json:"label"`
	Semantics   ValueSemantics `json:"semantics"`
	Display     string         `json:"display"`
	Status      MetricStatus   `json:"status"`
	Unit        string         `json:"unit"`
	Reason      string         `json:"reason,omitempty"`

	PercentileDisplay string           `json:"percentile_display,omitempty"`
	PercentileStatus  PercentileStatus `json:"percentile_status,omitempty"`
	PercentileSample  int              `json:"percentile_sample,omitempty"`

	// The baseline fields are populated for CHANGE lines only, and they are the pair §10.4
	// exists to keep visible: (ObservationGap 1, CalendarGapDays 4) is a one-session
	// difference across a long weekend, and a label saying "1D" would be a lie.
	BaselineDate    string `json:"baseline_date,omitempty"`
	ObservationGap  int    `json:"observation_gap,omitempty"`
	CalendarGapDays int    `json:"calendar_gap_days,omitempty"`

	Provenance
}

// InstitutionalDerivativesView is the assembled answer.
type InstitutionalDerivativesView struct {
	// AsOf is the point-in-time cutoff the observations were read under, as supplied by the
	// reader. It is NOT the trading date: reading 09-04 as of 09-06 is a legitimate,
	// different question from reading it as of 09-04, and a view that recorded only one of
	// the two could not say which it answered.
	AsOf        string          `json:"as_of"`
	TradingDate string          `json:"trading_date"`
	Session     string          `json:"session"`
	Dataset     string          `json:"dataset"`
	Scope       InstrumentScope `json:"scope"`
	// SessionPolicy records WHY the session is what it is (§10.4): this feed has no session
	// dimension, so COMBINED is fixed by the data contract and never read off a clock.
	SessionPolicy string `json:"session_policy"`

	// Institutions are the per-institution views, in the exchange's own order; Missing names
	// the ones with no series at all.
	Institutions []InstitutionView `json:"institutions"`
	Missing      []string          `json:"missing,omitempty"`

	// Total is the three-institution aggregate, PARTIAL with the value absent whenever any
	// component is missing.
	Total InstitutionalTotal `json:"total"`

	Evidence []EvidenceLine `json:"evidence"`
	Warnings []Warning      `json:"warnings,omitempty"`

	// Status is AVAILABLE only when all three institutions contributed and the total has its
	// three values. PARTIAL otherwise — never a number, and never NEUTRAL.
	Status MetricStatus `json:"status"`
	Reason string       `json:"reason,omitempty"`

	Policy         Policy `json:"policy"`
	FeatureVersion string `json:"feature_version"`
	ViewVersion    string `json:"view_version"`
	RuleVersions   struct {
		Alignment  string `json:"alignment"`
		Percentile string `json:"percentile"`
	} `json:"rule_versions"`
}

// Institution returns one institution's view by name.
func (v InstitutionalDerivativesView) Institution(name string) (InstitutionView, bool) {
	for _, iv := range v.Institutions {
		if iv.Institution == name {
			return iv, true
		}
	}
	return InstitutionView{}, false
}

// WarningsFor returns the warnings carrying one code.
func (v InstitutionalDerivativesView) WarningsFor(code string) []Warning {
	var out []Warning
	for _, w := range v.Warnings {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

// HasWarning reports whether any warning carries the code.
func (v InstitutionalDerivativesView) HasWarning(code string) bool {
	return len(v.WarningsFor(code)) > 0
}

// BuildInstitutionalDerivativesView assembles the whole M4 answer from already point-in-time
// series.
//
// asOf is the cutoff the caller read under, recorded rather than derived: this package has no
// clock and no database, and inferring the cutoff from the newest trading date present would
// make a view read as of 09-06 indistinguishable from one read as of 09-04.
//
// A missing institution is a data fact and produces PARTIAL with the absentee named. A MIXED
// IDENTITY — two trading dates, two scopes, two revisions, the same institution twice — is a
// caller bug and is an error, because no number would have been correct (§10.4).
func BuildInstitutionalDerivativesView(asOf string, series []InstitutionSeries, p Policy) (
	InstitutionalDerivativesView, error) {

	np, err := p.Normalized()
	if err != nil {
		return InstitutionalDerivativesView{}, err
	}
	if len(series) == 0 {
		return InstitutionalDerivativesView{}, ErrNoSeries
	}
	if _, err := parseDate(asOf); err != nil {
		return InstitutionalDerivativesView{}, fmt.Errorf("%w: as-of %q", ErrBadDate, asOf)
	}
	asOfDate, _ := parseDate(asOf)

	first := series[0].Current
	views := make([]InstitutionView, 0, len(series))
	seen := map[string]bool{}
	for _, s := range series {
		if seen[s.Current.Institution] {
			return InstitutionalDerivativesView{}, fmt.Errorf(
				"%w: two series for %s on %s — a total that counted one institution twice "+
					"would be wrong by exactly that institution",
				ErrDuplicateObservation, s.Current.Institution, s.Current.TradingDate)
		}
		seen[s.Current.Institution] = true
		if err := crossSeries(first, s.Current); err != nil {
			return InstitutionalDerivativesView{}, err
		}
		// A reading dated after the cutoff is a look-ahead however it got here, and it is
		// the one error this function must not tolerate quietly: the whole point of the
		// as-of contract is that a later observation cannot reach an earlier reading.
		curDate, err := parseDate(s.Current.TradingDate)
		if err != nil {
			return InstitutionalDerivativesView{}, err
		}
		if curDate.After(asOfDate) {
			return InstitutionalDerivativesView{}, fmt.Errorf(
				"%w: %s is dated after the as-of cutoff %s",
				ErrLookAhead, s.Current.TradingDate, asOf)
		}
		v, err := BuildInstitutionView(s.Current, s.History, np)
		if err != nil {
			return InstitutionalDerivativesView{}, err
		}
		views = append(views, v)
	}

	sort.SliceStable(views, func(i, j int) bool {
		return institutionOrder(views[i].Institution) < institutionOrder(views[j].Institution)
	})

	out := InstitutionalDerivativesView{
		AsOf:           asOf,
		TradingDate:    first.TradingDate,
		Session:        first.Session,
		Dataset:        first.Dataset,
		Scope:          first.Scope,
		SessionPolicy:  SessionPolicyFixedByContract,
		Institutions:   views,
		Policy:         np,
		FeatureVersion: FeatureVersion,
		ViewVersion:    InstitutionalViewVersion,
	}
	out.RuleVersions.Alignment = AlignmentRuleVersion
	out.RuleVersions.Percentile = PercentileRuleVersion

	for _, name := range RequiredInstitutions {
		if !seen[name] {
			out.Missing = append(out.Missing, name)
		}
	}

	// The total goes through AggregateInstitutions unchanged. It is the one place that knows
	// a three-institution total needs a COMMON BASELINE as well as three components, and a
	// second summation here would be a second answer to the same question.
	out.Total, err = AggregateInstitutions(views)
	if err != nil {
		return InstitutionalDerivativesView{}, err
	}

	out.Evidence = evidenceOf(views, out.Total)
	out.Warnings = warningsOf(views, out.Total, out.Missing)

	out.Status = StatusAvailable
	switch {
	case len(out.Missing) > 0:
		out.Status = StatusPartial
		out.Reason = fmt.Sprintf(
			"%v did not contribute; §10.4 does not publish a three-institution total from a "+
				"partial set", out.Missing)
	case out.Total.Status != StatusAvailable:
		out.Status = StatusPartial
		out.Reason = out.Total.Reason
	}
	return out, nil
}

// crossSeries rejects the mixtures §10.4 calls caller errors. Same shape as
// Observation.sameSeries, minus the institution — which is exactly what differs here.
func crossSeries(a, b Observation) error {
	if a.TradingDate != b.TradingDate {
		return fmt.Errorf("%w: %s and %s", ErrDateMismatch, a.TradingDate, b.TradingDate)
	}
	if a.Session != b.Session {
		return fmt.Errorf("%w: %q and %q", ErrSessionMismatch, a.Session, b.Session)
	}
	if a.Dataset != b.Dataset {
		return fmt.Errorf("%w: %q and %q", ErrDatasetMismatch, a.Dataset, b.Dataset)
	}
	if !a.Scope.Same(b.Scope) {
		return fmt.Errorf("%w: %s and %s — a view over two contract sizes has no unit",
			ErrScopeMismatch, a.Scope, b.Scope)
	}
	return nil
}

func institutionOrder(name string) int {
	for i, k := range RequiredInstitutions {
		if k == name {
			return i
		}
	}
	return len(RequiredInstitutions)
}

// evidenceOf flattens the views into the lines a reader sees, in a fixed order so two runs over
// the same data produce byte-identical output.
func evidenceOf(views []InstitutionView, total InstitutionalTotal) []EvidenceLine {
	var out []EvidenceLine
	for _, v := range views {
		out = append(out,
			line(v.Institution, ComponentFlow, v.Flow.ObservedMetric, v.Percentiles.Flow,
				BaselineResolution{}),
			line(v.Institution, ComponentPosition, v.Position.ObservedMetric,
				v.Percentiles.Position, BaselineResolution{}),
		)
		for _, h := range v.Horizons {
			pct := PercentileRank{}
			if h.Horizon == 1 {
				// Only CHANGE_1OBS has a reference distribution in v1: a percentile for
				// Change10 needs a Change10 distribution, and ranking it against the
				// one-step one would be the same pooling error as FLOW against POSITION.
				pct = v.Percentiles.Change
			}
			out = append(out, line(v.Institution, h.Label, h.Change.ObservedMetric, pct,
				h.Resolution))
		}
	}
	out = append(out,
		line(AggregateLabel, ComponentFlow, total.Flow.ObservedMetric, PercentileRank{},
			BaselineResolution{}),
		line(AggregateLabel, ComponentPosition, total.Position.ObservedMetric, PercentileRank{},
			BaselineResolution{}),
		line(AggregateLabel, HorizonLabel(1), total.Change.ObservedMetric, PercentileRank{},
			BaselineResolution{PreviousTradingDate: total.ChangeBaselineDate}),
	)
	return out
}

func line(institution, label string, m ObservedMetric, pct PercentileRank,
	res BaselineResolution) EvidenceLine {

	l := EvidenceLine{
		Institution: institution,
		Label:       label,
		Semantics:   m.Semantics,
		Display:     m.Display,
		Status:      m.Status,
		Unit:        m.Unit,
		Reason:      m.Reason,
		Provenance:  m.Provenance,
	}
	if pct.Status != "" {
		l.PercentileDisplay = pct.Display
		l.PercentileStatus = pct.Status
		l.PercentileSample = pct.SampleSize
	}
	if m.Semantics == SemanticsChange {
		l.BaselineDate = res.PreviousTradingDate
		l.ObservationGap = res.ObservationGap
		l.CalendarGapDays = res.CalendarGapDays
	}
	return l
}

// warningsOf is every condition a reader must see before acting on the numbers, in a fixed
// order: per institution in the exchange's order, then the view-level ones.
func warningsOf(views []InstitutionView, total InstitutionalTotal, missing []string) []Warning {
	var out []Warning
	add := func(code, inst, subject, msg string) {
		out = append(out, Warning{Code: code, Institution: inst, Subject: subject, Message: msg})
	}

	for _, v := range views {
		if !v.SourceStatus.Usable() {
			add(WarnSourceNotUsable, v.Institution, "",
				fmt.Sprintf("the %s snapshot for %s is %s, so nothing was read for this "+
					"institution", v.Dataset, v.TradingDate, v.SourceStatus))
		}
		if v.SourceStatus == StatusPartial {
			add(WarnPartialSnapshot, v.Institution, "",
				fmt.Sprintf("%s came from a PARTIAL snapshot: the stored rows are usable, an "+
					"aggregate whose correctness depends on completeness is not (§6.2)",
					v.TradingDate))
		}
		if v.SourceStatus.Usable() && !v.Coverage.FoundScopeProduct() {
			add(WarnScopeProductAbsent, v.Institution, v.Scope.ID,
				fmt.Sprintf("%s did not appear in the %s response for %s, although %d other "+
					"products did — the fetch succeeded and the observation did not",
					v.Scope.Product, v.Dataset, v.TradingDate, len(v.Coverage.ProductsSeen)))
		}
		if v.SourceStatus.Usable() && !v.Position.Observed {
			add(WarnPositionAbsent, v.Institution, string(SemanticsPosition),
				fmt.Sprintf("POSITION is %s for %s; it is never filled from FLOW (§10.4)",
					v.Position.Status, v.TradingDate))
		}
		for _, h := range v.Horizons {
			switch h.Status {
			case StatusStaleBaseline:
				add(WarnStaleBaseline, v.Institution, h.Label, h.Change.Reason)
			case StatusInsufficientHistory:
				add(WarnInsufficientHistory, v.Institution, h.Label, h.Change.Reason)
			}
			if h.Horizon == 1 && h.Status == StatusAvailable {
				missed := skippedSessions(h.Resolution)
				switch {
				case len(missed) > 0:
					add(WarnObservationGap, v.Institution, h.Label, fmt.Sprintf(
						"%s is one valid observation back (%s → %s, %d calendar days) and "+
							"stepped over %d session(s) this repository does not have: %v — "+
							"it is not a one-day change",
						h.Label, h.PreviousTradingDate, h.CurrentTradingDate,
						h.CalendarGapDays, len(missed), missed))
				case h.CalendarGapDays > SingleSessionCalendarDays:
					add(WarnObservationGap, v.Institution, h.Label, fmt.Sprintf(
						"%s is one valid observation back but %d calendar days back (%s → %s), "+
							"and nothing was recorded in between; it is not a one-day change",
						h.Label, h.CalendarGapDays, h.PreviousTradingDate, h.CurrentTradingDate))
				}
			}
		}
		for _, pct := range []PercentileRank{
			v.Percentiles.Flow, v.Percentiles.Position, v.Percentiles.Change,
		} {
			if pct.Status == PercentileInsufficientSample {
				add(WarnInsufficientSample, v.Institution, string(pct.Semantics), pct.Reason)
			}
		}
	}

	for _, name := range missing {
		add(WarnMissingInstitution, name, "",
			fmt.Sprintf("%s has no series for this date, session and scope; a three-institution "+
				"total is not published from a partial set (§10.4)", name))
	}
	if total.Status != StatusAvailable {
		add(WarnAggregatePartial, "", "", total.Reason)
	}
	if total.Change.Status == StatusPartial && total.ChangeBaselineDate == "" &&
		len(missing) == 0 {
		add(WarnBaselinesDiffer, "", string(SemanticsChange), total.Change.Reason)
	}
	return out
}

// FetchedAtOf is the newest retrieval time across the institutions, for a caller that wants one
// timestamp on a panel. It is a MAXIMUM rather than a single value because the three
// institutions come from one snapshot in practice and need not in principle.
func FetchedAtOf(v InstitutionalDerivativesView) time.Time {
	var newest time.Time
	for _, iv := range v.Institutions {
		if iv.FetchedAt.After(newest) {
			newest = iv.FetchedAt
		}
	}
	return newest
}
