package institutional

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// The three-institution total.
//
// §10.4: "A three-institution total requires all three observed, on the same trading date,
// session, dataset, instrument scope, revision view and value semantics. Any missing component
// makes the aggregate PARTIAL with the value ABSENT. 外資 being present is not grounds for
// calling the total available."
//
// Two failure modes are handled differently on purpose. A MISSING institution is a data fact,
// and the answer is PARTIAL with no number. A MIXED identity — two dates, two scopes, two
// revisions, the same institution twice — is a CALLER BUG, and the answer is an error: there is
// no number that would have been correct, so returning one with a caveat attached is worse than
// refusing.

// ErrRevisionMismatch — the views were read under different revisions of the same session, so
// they are not one point-in-time view (§7.3).
var ErrRevisionMismatch = errors.New("institutional: revision views differ")

// ErrDateMismatch — the views describe different trading dates.
var ErrDateMismatch = errors.New("institutional: trading dates differ")

// ErrUnknownInstitution — a view for something outside the closed three-institution set.
var ErrUnknownInstitution = errors.New("institutional: not one of the three reported institutions")

// InstitutionalTotal is the sum across the three institutions, or the recorded reason there
// isn't one.
type InstitutionalTotal struct {
	TradingDate string          `json:"trading_date"`
	Session     string          `json:"session"`
	Dataset     string          `json:"dataset"`
	Scope       InstrumentScope `json:"scope"`
	Revision    int             `json:"revision"`
	FetchedAt   time.Time       `json:"fetched_at,omitempty"`

	// Institutions are the ones that contributed, in the exchange's order; Missing are the
	// ones that did not, named rather than counted.
	Institutions []string `json:"institutions"`
	Missing      []string `json:"missing,omitempty"`

	Flow     TradingFlowContracts          `json:"flow"`
	Position OpenInterestPositionContracts `json:"position"`
	Change   PositionChangeContracts       `json:"change"`

	// ChangeBaselineDate is the baseline every contributing institution's CHANGE was
	// measured against. A total of three differences taken over three different windows is
	// not a difference of the total, so when the baselines disagree Change is PARTIAL.
	ChangeBaselineDate string `json:"change_baseline_date,omitempty"`

	// Status is AVAILABLE only when all three institutions contributed AND all three
	// semantics were observed for each of them. Otherwise PARTIAL.
	Status         MetricStatus `json:"status"`
	Reason         string       `json:"reason,omitempty"`
	FeatureVersion string       `json:"feature_version"`
}

// AggregateInstitutions sums the three institutions' FLOW, POSITION and CHANGE.
//
// Every semantics is summed separately and gets its own status: 外資 alone is never grounds for
// calling a total available, and a total whose POSITION is complete but whose CHANGE has one
// institution short says so in two places rather than picking one.
func AggregateInstitutions(views []InstitutionView) (InstitutionalTotal, error) {
	if len(views) == 0 {
		return InstitutionalTotal{}, errors.New(
			"institutional: a three-institution total needs views; none were supplied")
	}

	first := views[0]
	byInstitution := map[string]InstitutionView{}
	for _, v := range views {
		if !isRequiredInstitution(v.Institution) {
			return InstitutionalTotal{}, fmt.Errorf("%w: %q is not in %v",
				ErrUnknownInstitution, v.Institution, RequiredInstitutions)
		}
		if _, dup := byInstitution[v.Institution]; dup {
			return InstitutionalTotal{}, fmt.Errorf(
				"%w: two views for %s on %s — summing them would double-count that institution",
				ErrDuplicateObservation, v.Institution, v.TradingDate)
		}
		if v.TradingDate != first.TradingDate {
			return InstitutionalTotal{}, fmt.Errorf("%w: %s and %s",
				ErrDateMismatch, first.TradingDate, v.TradingDate)
		}
		if v.Session != first.Session {
			return InstitutionalTotal{}, fmt.Errorf("%w: %q and %q",
				ErrSessionMismatch, first.Session, v.Session)
		}
		if v.Dataset != first.Dataset {
			return InstitutionalTotal{}, fmt.Errorf("%w: %q and %q",
				ErrDatasetMismatch, first.Dataset, v.Dataset)
		}
		if !v.Scope.Same(first.Scope) {
			return InstitutionalTotal{}, fmt.Errorf(
				"%w: %s and %s — a total over two contract sizes has no unit",
				ErrScopeMismatch, first.Scope, v.Scope)
		}
		if v.Revision != first.Revision {
			return InstitutionalTotal{}, fmt.Errorf("%w: revision %d and revision %d for %s",
				ErrRevisionMismatch, first.Revision, v.Revision, first.TradingDate)
		}
		byInstitution[v.Institution] = v
	}

	total := InstitutionalTotal{
		TradingDate:    first.TradingDate,
		Session:        first.Session,
		Dataset:        first.Dataset,
		Scope:          first.Scope,
		Revision:       first.Revision,
		FetchedAt:      first.FetchedAt,
		FeatureVersion: FeatureVersion,
	}
	for _, name := range RequiredInstitutions {
		if _, ok := byInstitution[name]; ok {
			total.Institutions = append(total.Institutions, name)
		} else {
			total.Missing = append(total.Missing, name)
		}
	}

	prov := Provenance{
		AsOf:       first.TradingDate,
		Session:    first.Session,
		Source:     first.Dataset,
		SnapshotID: first.SnapshotID,
		Revision:   first.Revision,
		FetchedAt:  first.FetchedAt,
	}

	if len(total.Missing) > 0 {
		reason := fmt.Sprintf(
			"%v did not contribute; a three-institution total needs all three (§10.4)", total.Missing)
		total.Status = StatusPartial
		total.Reason = reason
		total.Flow = flowOf(newAbsent(SemanticsFlow, StatusPartial, reason, prov))
		total.Position = positionOf(newAbsent(SemanticsPosition, StatusPartial, reason, prov))
		total.Change = changeOf(newAbsent(SemanticsChange, StatusPartial, reason, prov))
		return total, nil
	}

	total.Status = StatusAvailable

	flowSum, flowMissing := sumOver(byInstitution, func(v InstitutionView) ObservedMetric {
		return v.Flow.ObservedMetric
	})
	posSum, posMissing := sumOver(byInstitution, func(v InstitutionView) ObservedMetric {
		return v.Position.ObservedMetric
	})
	chgSum, chgMissing := sumOver(byInstitution, func(v InstitutionView) ObservedMetric {
		return v.Change.ObservedMetric
	})

	total.Flow = flowOf(componentTotal(SemanticsFlow, flowSum, flowMissing, prov))
	total.Position = positionOf(componentTotal(SemanticsPosition, posSum, posMissing, prov))

	// The CHANGE total additionally requires one shared baseline. Three differences taken
	// over three different windows do not add up to a difference of the total, and nothing
	// downstream could detect that they had been added.
	baselines := map[string]bool{}
	for _, name := range total.Institutions {
		baselines[byInstitution[name].ChangeBaseline.PreviousTradingDate] = true
	}
	switch {
	case len(chgMissing) > 0:
		total.Change = changeOf(componentTotal(SemanticsChange, chgSum, chgMissing, prov))
	case len(baselines) > 1:
		dates := make([]string, 0, len(baselines))
		for d := range baselines {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		reason := fmt.Sprintf(
			"the three institutions' CHANGE baselines differ (%v); a sum of differences taken "+
				"over different windows is not the difference of the sum", dates)
		total.Change = changeOf(newAbsent(SemanticsChange, StatusPartial, reason, prov))
	default:
		for d := range baselines {
			total.ChangeBaselineDate = d
		}
		total.Change = changeOf(newObserved(SemanticsChange, chgSum, prov))
	}

	for _, m := range []ObservedMetric{
		total.Flow.ObservedMetric, total.Position.ObservedMetric, total.Change.ObservedMetric,
	} {
		if m.Status != StatusAvailable {
			total.Status = StatusPartial
			if total.Reason == "" {
				total.Reason = m.Reason
			}
		}
	}
	return total, nil
}

// sumOver adds one semantics across the institutions, naming the ones with nothing to add.
func sumOver(views map[string]InstitutionView, pick func(InstitutionView) ObservedMetric) (float64, []string) {
	var sum float64
	var missing []string
	for _, name := range RequiredInstitutions {
		v, ok := views[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		lots, has := pick(v).Lots()
		if !has {
			missing = append(missing, name)
			continue
		}
		sum += lots
	}
	return sum, missing
}

func componentTotal(sem ValueSemantics, sum float64, missing []string, prov Provenance) ObservedMetric {
	if len(missing) > 0 {
		return newAbsent(sem, StatusPartial, fmt.Sprintf(
			"%s is absent for %v; the total is not published from a partial set (§10.4)",
			sem, missing), prov)
	}
	return newObserved(sem, sum, prov)
}

func isRequiredInstitution(name string) bool {
	for _, k := range RequiredInstitutions {
		if k == name {
			return true
		}
	}
	return false
}
