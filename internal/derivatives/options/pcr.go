package options

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
)

// Errors that mean a caller combined things that do not combine.
var (
	// ErrKeyMismatch — two readings that are not the same measurement: a different product,
	// contract family, session or selection policy. Differencing them, or ranking one
	// against the other's history, produces a number about nothing.
	ErrKeyMismatch = errors.New("options: readings describe different measurements")
	// ErrMetricMismatch — a volume figure and an open-interest figure in one series.
	ErrMetricMismatch = errors.New("options: metric types differ")
	// ErrDuplicateObservation — two observations for one trading date in one series.
	ErrDuplicateObservation = errors.New("options: duplicate observation")
	// ErrLookAhead — a history entry dated at or after the reading being computed.
	ErrLookAhead = errors.New("options: history contains an observation at or after the current date")
	// ErrBadDate — a date that is not a YYYY-MM-DD calendar date.
	ErrBadDate = errors.New("options: date must be YYYY-MM-DD")
)

// ObservationKey is the identity of a measurement: everything that has to match before two
// figures may be differenced or ranked against one another.
//
// It does NOT include the metric type, because one selection of rows produces both metrics.
// DistributionKey adds it (§10.8: every combination of metric x family x session x selection
// policy gets its own distribution).
type ObservationKey struct {
	Product string          `json:"product"`
	Family  ContractFamily  `json:"family"`
	Session string          `json:"session"`
	Policy  SelectionPolicy `json:"selection_policy"`
}

// Same reports whether two readings are the same measurement.
func (k ObservationKey) Same(o ObservationKey) bool {
	return k.Product == o.Product && k.Family == o.Family &&
		k.Session == o.Session && k.Policy == o.Policy
}

// String renders the key for an error message.
func (k ObservationKey) String() string {
	return fmt.Sprintf("%s/%s/%s/%s", k.Product, k.Family, k.Session, k.Policy)
}

// Valid reports whether the key was built through a request.
func (k ObservationKey) Valid() error {
	if k.Product == "" {
		return errors.New("options: a measurement needs a product")
	}
	if !k.Family.Valid() {
		return fmt.Errorf("options: %q is not a contract family", k.Family)
	}
	if !ValidSession(k.Session) {
		return fmt.Errorf("options: %q is not one of the sessions %v", k.Session, Sessions)
	}
	if k.Policy != SelectProductFamilyExact {
		return fmt.Errorf("options: selection policy %q; v1 supports only %s",
			k.Policy, SelectProductFamilyExact)
	}
	return nil
}

// SnapshotRef is one archived response a figure was derived from.
//
// A slice of these rather than one id, because a COMBINED volume figure legitimately comes
// from TWO snapshots: §10.2b stores DailyMarketReportOpt as a DAY snapshot and an AFTER_HOURS
// snapshot, since options_oi_by_strike is keyed without a session column. A single
// snapshot_id field would have to drop one of them.
type SnapshotRef struct {
	Session    string                     `json:"session"`
	SnapshotID int64                      `json:"snapshot_id,omitempty"`
	Revision   int                        `json:"revision"`
	FetchedAt  time.Time                  `json:"fetched_at,omitempty"`
	Status     institutional.MetricStatus `json:"status,omitempty"`
	Rows       int                        `json:"rows,omitempty"`
	Rejected   int                        `json:"rejected_rows,omitempty"`
}

// Provenance is where a figure came from, in enough detail to re-derive it.
type Provenance struct {
	// AsOf is the point-in-time cutoff the reading was resolved under. Equal to the trading
	// date for a reading of its own session; later when a past session is re-read.
	AsOf        string        `json:"as_of"`
	TradingDate string        `json:"trading_date"`
	Session     string        `json:"session"`
	Source      string        `json:"source"`
	Snapshots   []SnapshotRef `json:"snapshots,omitempty"`
}

// Revision is the highest revision among the contributing snapshots, and whether they agreed.
//
// Two sessions of one date can legitimately sit at different revisions — the night rows are
// published later and can be corrected on their own — so the pair is reported rather than one
// number being chosen.
func (p Provenance) Revision() (int, bool) {
	if len(p.Snapshots) == 0 {
		return 0, false
	}
	rev := p.Snapshots[0].Revision
	agreed := true
	for _, s := range p.Snapshots[1:] {
		if s.Revision != rev {
			agreed = false
		}
		if s.Revision > rev {
			rev = s.Revision
		}
	}
	return rev, agreed
}

// FetchedAt is the LATEST retrieval among the contributing snapshots: the instant by which
// every byte behind this figure was in hand.
func (p Provenance) FetchedAt() time.Time {
	var out time.Time
	for _, s := range p.Snapshots {
		if s.FetchedAt.After(out) {
			out = s.FetchedAt
		}
	}
	return out
}

// metricProvenance is the single-snapshot Provenance M4's ObservedMetric carries.
//
// SnapshotID is set only when exactly one snapshot contributed. With two it stays 0 — never
// invented, and never silently the first of them — and the full list is on PCR.Provenance.
func (p Provenance) metricProvenance() institutional.Provenance {
	out := institutional.Provenance{
		AsOf:      p.TradingDate,
		Session:   p.Session,
		Source:    p.Source,
		FetchedAt: p.FetchedAt(),
	}
	if len(p.Snapshots) == 1 {
		out.SnapshotID = p.Snapshots[0].SnapshotID
		out.Revision = p.Snapshots[0].Revision
	} else if rev, agreed := p.Revision(); agreed {
		out.Revision = rev
	}
	return out
}

// ── the request ───────────────────────────────────────────────────────────────────────

// ObservationRequest is everything about a reading that does NOT come from the rows.
type ObservationRequest struct {
	TradingDate string
	// AsOf is the cutoff the reading was resolved under. Empty means "its own session".
	AsOf    string
	Product string
	Family  ContractFamily
	Session string
	Source  string
	// SourceStatus is §6's status of the snapshot(s) these rows came from. It is an input:
	// NOT_PUBLISHED and NO_SESSION both arrive as zero rows and only the acquisition layer
	// knows which happened.
	SourceStatus institutional.MetricStatus
	Snapshots    []SnapshotRef
	// RejectedRows is how many rows the parser rejected (§6.2).
	RejectedRows int
	// SettlementRead says whether FinalSettlementPriceIndexOptions was successfully read
	// FOR THIS TRADING DATE. Without it the open-interest half is MISSING (§3.1) — never
	// silently computed over a settled expiry, which was a 2.31x error on 2026-09-04.
	SettlementRead bool
	// SettlingExpiry is the expiry whose final settlement day IS this trading date, or ""
	// on a non-settlement day. It comes from the settlement snapshot ARCHIVED FOR D,
	// compared against D — never against "today" and never against the live feed.
	SettlingExpiry string
	// SettlementDates maps expiry code to settlement date, for the days-to-expiry figures.
	// Optional; an expiry with no entry reports no distance rather than a guessed one.
	SettlementDates map[string]string
}

func (r ObservationRequest) key() ObservationKey {
	return ObservationKey{
		Product: r.Product, Family: r.Family, Session: r.Session,
		Policy: SelectProductFamilyExact,
	}
}

func (r ObservationRequest) validate() error {
	if _, err := time.Parse(institutional.DateLayout, r.TradingDate); err != nil {
		return fmt.Errorf("%w: trading date %q", ErrBadDate, r.TradingDate)
	}
	if r.AsOf != "" {
		if _, err := time.Parse(institutional.DateLayout, r.AsOf); err != nil {
			return fmt.Errorf("%w: as-of %q", ErrBadDate, r.AsOf)
		}
		if r.AsOf < r.TradingDate {
			return fmt.Errorf("%w: as-of %s is earlier than the session %s it reads",
				ErrLookAhead, r.AsOf, r.TradingDate)
		}
	}
	if err := r.key().Valid(); err != nil {
		return err
	}
	if r.Source == "" {
		return errors.New("options: an observation needs a source endpoint")
	}
	if !r.SourceStatus.IsSourceStatus() {
		return fmt.Errorf(
			"options: source status %q is not one of %v — a derived status describes a "+
				"computation and cannot be a snapshot's fetch outcome (§6.1)",
			r.SourceStatus, institutional.SourceStatuses)
	}
	if r.RejectedRows < 0 {
		return fmt.Errorf("options: negative rejected-row count %d", r.RejectedRows)
	}
	return nil
}

// ── the two PCR types ─────────────────────────────────────────────────────────────────

// PCR is one put/call ratio with everything §10.8 requires it to carry: numerator,
// denominator, ratio, observed state, session, contract family, included and excluded
// contracts, trading date, as-of, snapshot id, revision, fetched-at, coverage and status.
//
// Never the ratio alone. A ratio with no numerator cannot be checked, and a 1.05 built from
// 21/20 is not the same evidence as one built from 42,000/40,000.
type PCR struct {
	Metric      MetricType     `json:"metric"`
	Key         ObservationKey `json:"key"`
	TradingDate string         `json:"trading_date"`
	AsOf        string         `json:"as_of"`

	// Numerator is Σ put and Denominator is Σ call, as M4 ObservedMetrics: lots, with the
	// FLOW/POSITION semantics of this metric attached, so a sum that reaches a reader
	// through any path still says whether it is traded or held.
	Numerator   institutional.ObservedMetric `json:"numerator"`
	Denominator institutional.ObservedMetric `json:"denominator"`
	Ratio       ObservedRatio                `json:"ratio"`
	Status      RatioStatus                  `json:"status"`
	Reason      string                       `json:"reason,omitempty"`

	Identity       ContractIdentity `json:"identity"`
	Structure      Structure        `json:"structure"`
	Coverage       Coverage         `json:"coverage"`
	Provenance     Provenance       `json:"provenance"`
	Reconciliation Reconciliation   `json:"reconciliation"`
	Warnings       []Warning        `json:"warnings,omitempty"`

	FeatureVersion string `json:"feature_version"`
}

// Value returns the ratio and whether there is one.
func (p PCR) Value() (float64, bool) { return p.Ratio.Value() }

// VolumePCR is Σ put TradingVolume / Σ call TradingVolume: what was TRADED this session.
//
// A distinct type rather than a field, so `oi = vol` does not compile. The two are different
// measurements of different things and neither is ever called just "PCR".
type VolumePCR struct{ PCR }

// OIPCR is Σ put OpenInterest / Σ call OpenInterest: what is HELD at the close.
type OIPCR struct{ PCR }

// Valid reports whether the wrapper and the stored metric agree — the check for a value that
// arrived by deserialisation, where the type is gone and only the field survives.
func (v VolumePCR) Valid() error { return checkMetric(v.Metric, MetricVolumePCR) }

// Valid reports whether the wrapper and the stored metric agree.
func (o OIPCR) Valid() error { return checkMetric(o.Metric, MetricOIPCR) }

func checkMetric(got, want MetricType) error {
	if got != want {
		return fmt.Errorf("options: value carries metric %q inside a %s container", got, want)
	}
	return nil
}

// ── the observation ───────────────────────────────────────────────────────────────────

// Observation is one session's selection of rows, with BOTH ratios computed from it.
//
// Both, from ONE selection, because the alternative is two selections that can disagree about
// which rows were in scope. Which of the two may be PUBLISHED for this session is a separate
// question and is answered by SessionPolicy.
type Observation struct {
	Key          ObservationKey             `json:"key"`
	TradingDate  string                     `json:"trading_date"`
	AsOf         string                     `json:"as_of"`
	SourceStatus institutional.MetricStatus `json:"source_status"`
	Volume       VolumePCR                  `json:"volume_pcr"`
	OI           OIPCR                      `json:"oi_pcr"`
	Provenance   Provenance                 `json:"provenance"`
}

// PCR returns one metric's ratio.
func (o Observation) PCR(m MetricType) (PCR, error) {
	switch m {
	case MetricVolumePCR:
		return o.Volume.PCR, nil
	case MetricOIPCR:
		return o.OI.PCR, nil
	}
	return PCR{}, fmt.Errorf("options: %q is not a metric type", m)
}

// Usable reports whether the snapshot behind this observation carried real rows.
func (o Observation) Usable() bool { return o.SourceStatus.Usable() }

// NewObservation selects the rows for one (product, family, session) and computes both PCRs.
func NewObservation(req ObservationRequest, rows []provider.OptionStrikeRow) (Observation, error) {
	if err := req.validate(); err != nil {
		return Observation{}, err
	}
	if req.AsOf == "" {
		req.AsOf = req.TradingDate
	}

	sel, err := selectRows(req, rows)
	if err != nil {
		return Observation{}, err
	}

	// A status saying nothing was observed, over rows that plainly were, is a contradiction
	// rather than a preference — M4's rule, and for the same reason: trusting either half
	// silently produces a NO_SESSION day carrying a ratio.
	if !req.SourceStatus.Usable() && len(sel.rows) > 0 {
		return Observation{}, fmt.Errorf(
			"options: source status %s carries %d in-scope rows for %s on %s — a status saying "+
				"nothing was observed cannot travel with an observation",
			req.SourceStatus, len(sel.rows), req.key(), req.TradingDate)
	}

	prov := Provenance{
		AsOf:        req.AsOf,
		TradingDate: req.TradingDate,
		Session:     req.Session,
		Source:      req.Source,
		Snapshots:   append([]SnapshotRef{}, req.Snapshots...),
	}

	obs := Observation{
		Key:          req.key(),
		TradingDate:  req.TradingDate,
		AsOf:         req.AsOf,
		SourceStatus: req.SourceStatus,
		Provenance:   prov,
	}
	vol, err := computePCR(MetricVolumePCR, req, sel, prov)
	if err != nil {
		return Observation{}, err
	}
	oi, err := computePCR(MetricOIPCR, req, sel, prov)
	if err != nil {
		return Observation{}, err
	}
	obs.Volume = VolumePCR{vol}
	obs.OI = OIPCR{oi}
	return obs, nil
}

// ComputeVolumePCR is the volume half on its own, for a caller that wants only it.
func ComputeVolumePCR(req ObservationRequest, rows []provider.OptionStrikeRow) (VolumePCR, error) {
	obs, err := NewObservation(req, rows)
	if err != nil {
		return VolumePCR{}, err
	}
	return obs.Volume, nil
}

// ComputeOIPCR is the open-interest half on its own.
func ComputeOIPCR(req ObservationRequest, rows []provider.OptionStrikeRow) (OIPCR, error) {
	obs, err := NewObservation(req, rows)
	if err != nil {
		return OIPCR{}, err
	}
	return obs.OI, nil
}

// ── selection ─────────────────────────────────────────────────────────────────────────

// selection is the in-scope rows plus the metric-independent half of the coverage record.
type selection struct {
	rows     []provider.OptionStrikeRow
	kinds    []string // classify(rows[i]), aligned with rows
	coverage Coverage
}

// selectRows keeps the rows belonging to one product, one session set and one contract family,
// and records every rejection with a reason.
//
// The settling-expiry exclusion is NOT here: it applies to open interest only, so it belongs
// to the per-metric sum. Everything above it is shared, so the two metrics can never disagree
// about which rows were in scope.
func selectRows(req ObservationRequest, rows []provider.OptionStrikeRow) (selection, error) {
	want := map[string]bool{}
	for _, s := range componentSessions(req.Session) {
		want[s] = true
	}

	cov := Coverage{
		Product:          req.Product,
		Family:           req.Family,
		Session:          req.Session,
		RowsSeen:         len(rows),
		RejectedRows:     req.RejectedRows,
		SessionsExpected: componentSessions(req.Session),
	}
	ex := newExcludeTally()
	sessionsPresent := map[string]bool{}
	expiries := map[string]string{} // code -> kind, in scope
	var out selection

	for _, r := range rows {
		if r.Product != req.Product {
			ex.note(ExcludedOtherProduct, r.Product)
			continue
		}
		if r.TradingDate != "" && r.TradingDate != req.TradingDate {
			ex.note(ExcludedOtherTradingDate, r.TradingDate)
			continue
		}
		kind, err := classify(r)
		if err != nil {
			return selection{}, err
		}
		switch kind {
		case provider.ExpiryCombination:
			cov.CombinationRows++
		case provider.ExpiryUnresolved:
			cov.UnresolvedRows++
		default:
			cov.ClassifiedRows++
		}
		if !want[r.Session] {
			ex.note(ExcludedOtherSession, r.Session)
			continue
		}
		if ok, reason := req.Family.admits(kind); !ok {
			ex.note(reason, r.ExpiryCode)
			continue
		}
		if r.CallPut != provider.CallSide && r.CallPut != provider.PutSide {
			ex.note(ExcludedUnknownSide, r.CallPut)
			continue
		}
		sessionsPresent[r.Session] = true
		expiries[r.ExpiryCode] = kind
		out.rows = append(out.rows, r)
		out.kinds = append(out.kinds, kind)
		if r.CallPut == provider.CallSide {
			cov.CallRows++
		} else {
			cov.PutRows++
		}
	}

	cov.RowsInScope = len(out.rows)
	cov.RowsExcluded = ex.total
	cov.Excluded = ex.list()
	cov.ClassificationComplete = cov.UnresolvedRows == 0
	for code := range expiries {
		cov.ExpectedContracts = append(cov.ExpectedContracts, code)
	}
	sort.Strings(cov.ExpectedContracts)
	for _, s := range cov.SessionsExpected {
		if sessionsPresent[s] {
			cov.SessionsPresent = append(cov.SessionsPresent, s)
		} else {
			cov.SessionsMissing = append(cov.SessionsMissing, s)
		}
	}
	out.coverage = cov
	return out, nil
}

// ── aggregation ───────────────────────────────────────────────────────────────────────

// sideSum is one side of one metric: the lots, and everything needed to tell an observed zero
// from an absence.
type sideSum struct {
	lots         float64
	rows         int // rows in scope for this side and metric
	contributing int // of those, the ones whose column carried a number
	expiries     map[string]bool
}

// observed reports whether the side has a number.
//
// TRUE requires at least one row to have CARRIED a value. Zero contributing rows is ABSENT,
// not zero: on the fixture every 盤後 row carries OpenInterest "-", so a zero-fill would turn
// the night session's non-existent open interest into an observed 0 and then into a fabricated
// 0/0 ratio. §3.1's MISSING ≠ ZERO, at the aggregate level.
func (s sideSum) observed() bool { return s.contributing > 0 }

// aggregateSide sums one side of one metric over the selected rows.
func aggregateSide(m MetricType, req ObservationRequest, sel selection, side string,
	cov *Coverage) sideSum {

	out := sideSum{expiries: map[string]bool{}}
	for _, r := range sel.rows {
		if r.CallPut != side {
			continue
		}
		// Open interest only: the expiry settling at this session's close is still printed
		// and is no longer live exposure. Volume sums every expiry — the rule is asymmetric.
		if m.ExcludesSettlingExpiry() && req.SettlingExpiry != "" && r.ExpiryCode == req.SettlingExpiry {
			continue
		}
		out.rows++
		v := m.valueOf(r)
		if v == nil {
			cov.AbsentValues++
			continue
		}
		cov.ObservedValues++
		out.contributing++
		out.lots += *v
		out.expiries[r.ExpiryCode] = true
	}
	return out
}

// computePCR is the whole of one metric: select, sum, decide, describe.
func computePCR(m MetricType, req ObservationRequest, sel selection, prov Provenance) (PCR, error) {
	cov := sel.coverage
	cov.Metric = m
	cov.ExpectedContracts = append([]string{}, sel.coverage.ExpectedContracts...)
	cov.Excluded = append([]ExcludedRows{}, sel.coverage.Excluded...)
	cov.ObservedValues, cov.AbsentValues = 0, 0

	// The settling expiry leaves the OPEN-INTEREST scope entirely: it is not a contract
	// whose value happened to be absent, it is a contract that is no longer live exposure.
	if m.ExcludesSettlingExpiry() && req.SettlingExpiry != "" {
		var kept []string
		removed := 0
		for _, code := range cov.ExpectedContracts {
			if code == req.SettlingExpiry {
				continue
			}
			kept = append(kept, code)
		}
		for _, r := range sel.rows {
			if r.ExpiryCode == req.SettlingExpiry {
				removed++
			}
		}
		if removed > 0 {
			cov.ExpectedContracts = kept
			cov.RowsInScope -= removed
			cov.RowsExcluded += removed
			cov.Excluded = appendExclusion(cov.Excluded, ExcludedSettlingExpiry, removed,
				req.SettlingExpiry)
		}
	}

	put := aggregateSide(m, req, sel, provider.PutSide, &cov)
	call := aggregateSide(m, req, sel, provider.CallSide, &cov)
	cov.PutContributing, cov.CallContributing = put.contributing, call.contributing
	fillPairCoverage(m, req, sel, &cov)
	cov.ActualContracts = mergedExpiries(put, call)
	cov.MissingContracts = missing(cov.ExpectedContracts, cov.ActualContracts)

	mp := prov.metricProvenance()
	sem := m.Semantics()
	p := PCR{
		Metric:         m,
		Key:            req.key(),
		TradingDate:    req.TradingDate,
		AsOf:           req.AsOf,
		Coverage:       cov,
		Provenance:     prov,
		Reconciliation: unreconciled(m, req.Family),
		FeatureVersion: FeatureVersion,
		Identity: newIdentity(req, m, cov.ExpectedContracts, sel,
			collectExcludedExpiries(m, req)),
	}

	// The numerator and the denominator are published whatever happens to the ratio: §10.8
	// forbids "the ratio alone", and the sums are the only thing that makes a ratio
	// checkable.
	p.Numerator = sideMetric(sem, put, m, provider.PutSide, req, mp)
	p.Denominator = sideMetric(sem, call, m, provider.CallSide, req, mp)

	p.Ratio, p.Reason, p.Warnings = decideRatio(m, req, cov, put, call)
	p.Status = p.Ratio.Status
	p.Structure = describeStructure(m, p.Ratio)
	if !SessionInPolicy(m, req.Session) {
		p.Warnings = append(p.Warnings, Warning{
			Code:   WarnSessionOutsidePolicy,
			Metric: m,
			Message: fmt.Sprintf(
				"%s is published for %v only; this figure is for %s and is evidence about the "+
					"data, not a headline reading", m, SessionPolicy(m), req.Session),
		})
	}
	sortWarnings(p.Warnings)
	return p, nil
}

// sideMetric turns one side's sum into an M4 ObservedMetric, deciding the absence REASON.
func sideMetric(sem institutional.ValueSemantics, s sideSum, m MetricType, side string,
	req ObservationRequest, mp institutional.Provenance) institutional.ObservedMetric {

	switch {
	case !req.SourceStatus.Usable():
		return institutional.NewAbsentMetric(sem, req.SourceStatus,
			"snapshot status "+string(req.SourceStatus), mp)
	case s.observed():
		return institutional.NewObservedMetric(sem, s.lots, mp)
	case s.rows == 0:
		return institutional.NewAbsentMetric(sem, institutional.StatusPartial,
			fmt.Sprintf("no %s rows for %s %s on %s", side, req.Product, req.Family,
				req.TradingDate), mp)
	default:
		// The rows are there and not one of them carried the column. §3.1's ABSENT, at the
		// aggregate level: an observation of absence, never a zero.
		return institutional.NewAbsentMetric(sem, institutional.StatusPartial,
			fmt.Sprintf("all %d %s rows carry no %s for %s %s on %s", s.rows, side,
				m.Column(), req.Product, req.Family, req.TradingDate), mp)
	}
}

// decideRatio is §10.8's zero/absent table, in order, and the ONLY place a PCR is divided.
//
//	call denominator observed > 0        → ratio available
//	call denominator observed = 0        → ZERO_DENOMINATOR, ratio absent
//	call denominator absent              → INSUFFICIENT_DATA, ratio absent
//	put numerator observed = 0, call > 0 → the ratio is an OBSERVED ZERO
//	put numerator absent                 → ratio absent
//
// with three gates in front of it, each of which forbids a number that would otherwise look
// entirely plausible.
func decideRatio(m MetricType, req ObservationRequest, cov Coverage, put, call sideSum) (
	ObservedRatio, string, []Warning) {

	var warns []Warning
	absent := func(status RatioStatus, reason string) (ObservedRatio, string, []Warning) {
		return newAbsentRatio(m, status, reason), reason, warns
	}

	// Gate 1: the snapshot itself. NO_SESSION, NOT_PUBLISHED, MISSING, ERROR, STALE and
	// NOT_AVAILABLE travel through unchanged — collapsing them is what makes a normal
	// afternoon before the file is out indistinguishable from a gap.
	if !req.SourceStatus.Usable() {
		status, err := FromSourceStatus(req.SourceStatus)
		if err != nil {
			status = RatioError
		}
		return absent(status, "snapshot status "+string(req.SourceStatus))
	}

	// Gate 2: open interest without a settlement read. §3.1 — the aggregate would silently
	// include a settled expiry and publish it as AVAILABLE, which was 223,014 against an
	// official 96,420 on 2026-09-04.
	if m.RequiresSettlementFeed() && !req.SettlementRead {
		warns = append(warns, Warning{Code: WarnSettlementFeedMissing, Metric: m,
			Message: "the settlement feed for this session was not read, so the open-interest " +
				"aggregate cannot know whether an expiry stopped being live exposure"})
		return absent(RatioMissing,
			"open interest requires a successful FinalSettlementPriceIndexOptions read for "+
				req.TradingDate+" and there was none")
	}

	// Gate 3: a PARTIAL snapshot. §6.2 — an aggregate whose correctness depends on
	// completeness may not be published from one, and a PCR over most of the rows would
	// reconcile against nothing.
	if req.SourceStatus == institutional.StatusPartial || cov.RejectedRows > 0 {
		warns = append(warns, Warning{Code: WarnPartialSnapshot, Metric: m,
			Message: fmt.Sprintf("%d rows were rejected from this response; §6.2 forbids "+
				"publishing an aggregate that depends on completeness from a PARTIAL snapshot",
				cov.RejectedRows)})
		return absent(RatioPartial, fmt.Sprintf(
			"the snapshot is %s with %d rejected rows; a PCR computed over most of the rows "+
				"is not a PCR", req.SourceStatus, cov.RejectedRows))
	}

	// Neither side has rows at all: nothing was selected, which is a different fact from a
	// side that is present and empty.
	if put.rows == 0 && call.rows == 0 {
		return absent(RatioInsufficientData, fmt.Sprintf(
			"no %s %s rows in scope for %s on %s", req.Product, req.Family, req.Session,
			req.TradingDate))
	}

	// Only one side present. §10.8: PARTIAL with the ratio absent; the missing side is NEVER
	// inferred from the present one.
	if put.rows == 0 || call.rows == 0 {
		side := provider.PutSide
		if call.rows == 0 {
			side = provider.CallSide
		}
		warns = append(warns, Warning{Code: WarnOneSideOnly, Metric: m,
			Message: "only one side of the ratio is present; the other is not inferred from it"})
		return absent(RatioPartial, fmt.Sprintf(
			"no %s rows in scope for %s %s %s on %s, so there is one side and no ratio",
			side, req.Product, req.Family, req.Session, req.TradingDate))
	}

	if !call.observed() {
		return absent(RatioInsufficientData, fmt.Sprintf(
			"the call %s is absent for %s %s %s on %s across all %d rows — absent, not zero",
			m.Column(), req.Product, req.Family, req.Session, req.TradingDate, call.rows))
	}
	if !put.observed() {
		return absent(RatioInsufficientData, fmt.Sprintf(
			"the put %s is absent for %s %s %s on %s across all %d rows — absent, not zero",
			m.Column(), req.Product, req.Family, req.Session, req.TradingDate, put.rows))
	}
	if call.lots == 0 {
		warns = append(warns, Warning{Code: WarnZeroDenominator, Metric: m,
			Message: "the call side was observed and is zero; the ratio is absent rather " +
				"than infinite"})
		return absent(RatioZeroDenominator, fmt.Sprintf(
			"the call %s for %s %s %s on %s was observed and is 0", m.Column(), req.Product,
			req.Family, req.Session, req.TradingDate))
	}
	return newObservedRatio(m, put.lots/call.lots), "", warns
}

// ── small helpers ─────────────────────────────────────────────────────────────────────

func appendExclusion(list []ExcludedRows, reason string, rows int, detail string) []ExcludedRows {
	for i := range list {
		if list[i].Reason == reason {
			list[i].Rows += rows
			list[i].Detail = append(list[i].Detail, detail)
			sort.Strings(list[i].Detail)
			return list
		}
	}
	list = append(list, ExcludedRows{Reason: reason, Rows: rows, Detail: []string{detail}})
	sort.Slice(list, func(i, j int) bool { return list[i].Reason < list[j].Reason })
	return list
}

func mergedExpiries(put, call sideSum) []string {
	seen := map[string]bool{}
	for c := range put.expiries {
		seen[c] = true
	}
	for c := range call.expiries {
		seen[c] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func missing(expected, actual []string) []string {
	have := map[string]bool{}
	for _, a := range actual {
		have[a] = true
	}
	var out []string
	for _, e := range expected {
		if !have[e] {
			out = append(out, e)
		}
	}
	return out
}

// fillPairCoverage counts the (expiry, strike) points and how many carried BOTH sides.
func fillPairCoverage(m MetricType, req ObservationRequest, sel selection, cov *Coverage) {
	type point struct {
		expiry string
		strike float64
	}
	seen := map[point]bool{}
	hasCall := map[point]bool{}
	hasPut := map[point]bool{}
	for _, r := range sel.rows {
		if m.ExcludesSettlingExpiry() && req.SettlingExpiry != "" && r.ExpiryCode == req.SettlingExpiry {
			continue
		}
		k := point{r.ExpiryCode, r.Strike}
		seen[k] = true
		if m.valueOf(r) == nil {
			continue
		}
		if r.CallPut == provider.CallSide {
			hasCall[k] = true
		} else {
			hasPut[k] = true
		}
	}
	cov.ExpectedPairs = len(seen)
	for k := range seen {
		switch {
		case hasCall[k] && hasPut[k]:
			cov.ActualPairs++
		case hasCall[k]:
			cov.CallOnlyPoints++
		case hasPut[k]:
			cov.PutOnlyPoints++
		}
	}
}

func collectExcludedExpiries(m MetricType, req ObservationRequest) []string {
	if m.ExcludesSettlingExpiry() && req.SettlingExpiry != "" {
		return []string{req.SettlingExpiry}
	}
	return nil
}

func sortWarnings(w []Warning) {
	sort.SliceStable(w, func(i, j int) bool { return w[i].Code < w[j].Code })
}
