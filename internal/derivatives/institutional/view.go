package institutional

import "time"

// InstitutionView is one institution's complete M4 answer for one date, session and instrument
// scope: the three semantics, the four horizons, the alignment, and the coverage that says what
// it was computed over.
//
// Everything on it is either an ObservedMetric (which can say "no number, and here is which
// kind of no") or a record of how it was produced. There is no bare float64 on this struct, and
// no field called `net`.
type InstitutionView struct {
	Institution string          `json:"institution"`
	TradingDate string          `json:"trading_date"`
	Session     string          `json:"session"`
	Dataset     string          `json:"dataset"`
	Scope       InstrumentScope `json:"scope"`
	SnapshotID  int64           `json:"snapshot_id,omitempty"`
	Revision    int             `json:"revision"`
	FetchedAt   time.Time       `json:"fetched_at,omitempty"`
	// SourceStatus is the snapshot's own fetch outcome (§6), carried unchanged.
	SourceStatus MetricStatus  `json:"source_status"`
	Coverage     ScopeCoverage `json:"coverage"`

	// Flow is what was TRADED this session; Position is what is HELD. They are different
	// types because §10.4 forbids substituting either for the other, and on 2026-09-04 投信
	// traded +1,785 while holding +76,174.
	Flow     TradingFlowContracts          `json:"flow"`
	Position OpenInterestPositionContracts `json:"position"`
	// Change is POSITION(D) − POSITION(previous valid observation): horizon 1, the same code
	// path as the rest.
	Change PositionChangeContracts `json:"change"`
	// ChangeBaseline is Change's resolution — previous trading date, observation gap,
	// calendar gap and every date skipped to get there.
	ChangeBaseline BaselineResolution `json:"change_baseline"`
	Horizons       []HorizonChange    `json:"horizons"`
	Alignment      Alignment          `json:"alignment"`
	// Percentiles are the three SEPARATE reference distributions (percentile.go). They are
	// computed here rather than offered as an optional extra so that no caller can rank a
	// reading against a distribution it assembled itself — which is where pooling FLOW and
	// POSITION would come back.
	Percentiles PercentileSet `json:"percentiles"`

	// SessionPolicy records WHY the session is what it is. The institutional futures feed
	// carries no session dimension, so COMBINED is fixed by the data contract and is never
	// chosen by reading a clock (§10.4).
	SessionPolicy  string `json:"session_policy"`
	Policy         Policy `json:"policy"`
	FeatureVersion string `json:"feature_version"`
}

// SessionPolicyFixedByContract is the only session policy this feed can have, and the sentence
// saying why travels with it.
const SessionPolicyFixedByContract = "COMBINED_FIXED_BY_DATA_CONTRACT"

// Horizon returns the ChangeN with the given N.
func (v InstitutionView) Horizon(n int) (HorizonChange, bool) {
	for _, h := range v.Horizons {
		if h.Horizon == n {
			return h, true
		}
	}
	return HorizonChange{}, false
}

// BuildInstitutionView assembles the whole M4 answer for one institution from one current
// observation and its already point-in-time-filtered history.
//
// history must be the SAME series: same institution, scope, session and dataset, every entry
// strictly earlier than current. Anything else is an error, not a filter — a view quietly
// computed over a mixture is the failure this package is built to make impossible.
func BuildInstitutionView(current Observation, history []Observation, p Policy) (InstitutionView, error) {
	np, err := p.Normalized()
	if err != nil {
		return InstitutionView{}, err
	}
	if err := current.Scope.Valid(); err != nil {
		return InstitutionView{}, err
	}
	if _, err := parseDate(current.TradingDate); err != nil {
		return InstitutionView{}, err
	}

	horizons, err := ChangeHorizons(current, history, np)
	if err != nil {
		return InstitutionView{}, err
	}

	v := InstitutionView{
		Institution:    current.Institution,
		TradingDate:    current.TradingDate,
		Session:        current.Session,
		Dataset:        current.Dataset,
		Scope:          current.Scope,
		SnapshotID:     current.SnapshotID,
		Revision:       current.Revision,
		FetchedAt:      current.FetchedAt,
		SourceStatus:   current.SourceStatus,
		Coverage:       current.Coverage,
		Flow:           current.Flow(),
		Position:       current.Position(),
		Horizons:       horizons,
		SessionPolicy:  SessionPolicyFixedByContract,
		Policy:         np,
		FeatureVersion: FeatureVersion,
	}
	if h, ok := v.Horizon(1); ok {
		v.Change = h.Change
		v.ChangeBaseline = h.Resolution
	}
	v.Alignment, err = Align(v.Flow, v.Position, horizons, np)
	if err != nil {
		return InstitutionView{}, err
	}
	v.Percentiles, err = Percentiles(current, history, np)
	if err != nil {
		return InstitutionView{}, err
	}
	return v, nil
}
