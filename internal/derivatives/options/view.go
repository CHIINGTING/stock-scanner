package options

import (
	"fmt"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
)

// The assembled M5 answer: one metric's ratio, its changes and its percentile, for one
// measurement — and the collection of them for a session.
//
// It is PURE. There is no report wiring, no config flag and no rendering here: M10 owns
// integration and R15 is default-off until §14's gate is met. What the type refuses to do
// matters as much as what it does — it publishes no direction, collapses nothing into a
// score, and carries no bare float64 anywhere.

// ViewVersion labels the assembly rules.
const ViewVersion = "R15-M5-view-v1"

// MetricView is everything M5 has to say about ONE measurement.
type MetricView struct {
	Key          DistributionKey       `json:"key"`
	TradingDate  string                `json:"trading_date"`
	AsOf         string                `json:"as_of"`
	PCR          PCR                   `json:"pcr"`
	Changes      []HorizonChange       `json:"changes"`
	Percentile   RatioPercentile       `json:"percentile"`
	Distribution ReferenceDistribution `json:"distribution"`
	Warnings     []Warning             `json:"warnings,omitempty"`
	RuleVersion  string                `json:"rule_version"`
}

// Change returns one horizon.
func (v MetricView) Change(n int) (HorizonChange, bool) {
	for _, c := range v.Changes {
		if c.Horizon == n {
			return c, true
		}
	}
	return HorizonChange{}, false
}

// BuildMetricView assembles one measurement's ratio, changes and percentile from ONE reading
// and ONE history.
//
// One history, resolved under one cutoff, because a change resolved under one cutoff and a
// percentile under another would be two answers wearing one label.
func BuildMetricView(m MetricType, current Observation, history []Observation,
	p institutional.Policy) (MetricView, error) {

	np, err := p.Normalized()
	if err != nil {
		return MetricView{}, err
	}
	pcr, err := current.PCR(m)
	if err != nil {
		return MetricView{}, err
	}
	changes, err := ChangeHorizons(m, current, history, np)
	if err != nil {
		return MetricView{}, err
	}
	rank, dist, err := Percentile(m, current, history, np)
	if err != nil {
		return MetricView{}, err
	}

	v := MetricView{
		Key:          DistributionKey{Metric: m, Key: current.Key},
		TradingDate:  current.TradingDate,
		AsOf:         current.AsOf,
		PCR:          pcr,
		Changes:      changes,
		Percentile:   rank,
		Distribution: dist,
		RuleVersion:  ViewVersion,
	}
	v.Warnings = append(v.Warnings, pcr.Warnings...)
	for _, c := range changes {
		v.Warnings = append(v.Warnings, c.Warnings...)
	}
	if rank.Status == PercentileInsufficientSample {
		v.Warnings = append(v.Warnings, Warning{
			Code: WarnInsufficientSample, Metric: m, Subject: "percentile",
			Message: rank.Reason,
		})
	}
	if !pcr.Coverage.SessionsComplete() {
		v.Warnings = append(v.Warnings, Warning{
			Code: WarnSessionAbsent, Metric: m,
			Message: fmt.Sprintf("no rows for %v, so this figure spans %v of the %v it names",
				pcr.Coverage.SessionsMissing, pcr.Coverage.SessionsPresent,
				pcr.Coverage.SessionsExpected),
		})
	}
	if pcr.Coverage.UnresolvedRows > 0 {
		v.Warnings = append(v.Warnings, Warning{
			Code: WarnUnresolvedExpiries, Metric: m,
			Message: fmt.Sprintf("%d rows carry an expiry code that parses as neither monthly "+
				"nor weekly", pcr.Coverage.UnresolvedRows),
		})
	}
	sortWarnings(v.Warnings)
	return v, nil
}

// OptionsStructureView is M5's whole answer for one session: every published measurement, and
// the institutional call/put readings beside them.
type OptionsStructureView struct {
	AsOf        string       `json:"as_of"`
	TradingDate string       `json:"trading_date"`
	Product     string       `json:"product"`
	Metrics     []MetricView `json:"metrics"`
	// Institutions are the call/put readings, one per institution. They are here rather than
	// merged into the metrics because they measure a different thing — three named
	// participants' positions, not the market's aggregate ratio.
	Institutions   []InstitutionalCallPut `json:"institutions,omitempty"`
	Warnings       []Warning              `json:"warnings,omitempty"`
	FeatureVersion string                 `json:"feature_version"`
	ViewVersion    string                 `json:"view_version"`
}

// Metric returns one measurement's view.
func (v OptionsStructureView) Metric(k DistributionKey) (MetricView, bool) {
	for _, m := range v.Metrics {
		if m.Key.Same(k) {
			return m, true
		}
	}
	return MetricView{}, false
}

// SortMetrics orders the views deterministically, so two runs over the same data serialise
// identically.
func SortMetrics(v *OptionsStructureView) {
	sort.SliceStable(v.Metrics, func(i, j int) bool {
		a, b := v.Metrics[i].Key, v.Metrics[j].Key
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Key.Family != b.Key.Family {
			return a.Key.Family < b.Key.Family
		}
		return a.Key.Session < b.Key.Session
	})
}
