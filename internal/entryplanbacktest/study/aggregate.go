package study

import (
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
)

// Row is one observation under one arm and one wait window, with its fill and its outcome.
// It is the atom every metric and every segment is computed from.
type Row struct {
	Obs  *Observation
	Arm  Arm
	Wait int
	// ExecutableStatus is whether this row belongs to the EP-9 §18 HEADLINE population (see
	// ExecutableStatuses). It is carried on the row rather than re-read from the observation
	// so that a single accumulator can route a row without knowing what an Observation is.
	ExecutableStatus bool
	Fill             Fill
	Out              Outcome
	// FillBps is the fill against the signal close, in basis points; ok=false when either
	// price is missing.
	FillBps   float64
	FillBpsOK bool
}

// Evaluate runs one observation through one arm and wait window.
//
// THE ARM DECIDES ITS OWN LIMIT AND ITS OWN NOT_APPLICABLE, and each one is a different
// sentence:
//
//	B  limit = the published IdealEntry.High. No zone → NOT_APPLICABLE: the plan named no
//	   price to bid, so there was never an order to miss.
//	C  limit = the published MaxChasePrice. No ceiling → NOT_APPLICABLE: under SIDEWAYS the
//	   policy REFUSES to chase (policy.go:479-482), and a refusal is not a failure to fill.
//	A  no limit at all; it buys the signal close and is not an executable order.
//	D  not evaluated here — it is the complement of B and is derived in Aggregate.
func Evaluate(o *Observation, arm Arm, wait int) Row {
	r := Row{Obs: o, Arm: arm, Wait: wait, ExecutableStatus: o.ExecutableStatus()}
	switch arm {
	case ArmSignalCloseBaseline:
		r.Fill = SimulateSignalClose(o.bars, o.sessions, o.SignalAsOf)
	case ArmZoneLimit:
		if o.ZoneHigh == nil {
			r.Fill = Fill{Outcome: FillNotApplicable, BarIndex: -1}
		} else {
			r.Fill = SimulateLimit(o.bars, o.sessions, o.SignalAsOf, *o.ZoneHigh, wait)
		}
	case ArmChase:
		if o.MaxChase == nil {
			r.Fill = Fill{Outcome: FillNotApplicable, BarIndex: -1}
		} else {
			r.Fill = SimulateLimit(o.bars, o.sessions, o.SignalAsOf, *o.MaxChase, wait)
		}
	default:
		r.Fill = Fill{Outcome: FillNotApplicable, BarIndex: -1}
	}
	if r.Fill.Filled() {
		r.Out = Measure(o.bars, r.Fill, o.Invalidation, o.Target1, o.Target2)
		r.FillBps, r.FillBpsOK = FillVsSignalCloseBps(r.Fill.Price, o.SignalClose)
	} else {
		r.Out = Outcome{Coverage: entryplanbacktest.WindowUnavailable, FirstTouchBar: -1}
	}
	return r
}

// Acc is a STREAMING accumulator for one metric block.
//
// It exists for a measured reason. The first full-scale attempt retained every evaluated Row
// — three arms x three wait windows x ~700,000 observations — and peaked at 1.54 GB on a
// 60-session slice, which extrapolates past 9 GB on the real 355-session run. Nothing in a
// metric block needs the rows afterwards: counters are additive and the only thing a
// percentile needs is the SAMPLE, which exists only for FILLED observations and is therefore
// two orders of magnitude smaller. So rows are fed in and dropped.
//
// Aggregate is kept as a thin wrapper over this type so every existing test still drives the
// same arithmetic through the same code.
//
// EVERY DENOMINATOR IS BUILT FROM THE POPULATION THAT COULD HAVE PRODUCED THE NUMERATOR, and
// they are different populations:
//
//	FillRate             over NCandidate — observations this arm had an order for.
//	Return@h             over filled observations whose series REACHES h. A horizon the data
//	                     does not reach is absent, not zero, so each horizon has its own N.
//	InvalidationHitRate  over filled observations WHOSE PLAN PUBLISHED AN INVALIDATION.
//	Target1/2HitRate     likewise.
//	StopFirstRate        over filled observations with BOTH levels, which is the only
//	                     population in which "which came first" is a question.
//
// Using NFilled for the last three would inflate every one of them downward by including
// plans that published no level. That is the denominator-inflation defect EP-9 §24 asks for.
type Acc struct {
	// nonExecutable inverts the §18 population filter. A headline accumulator SKIPS rows whose
	// status is not executable and counts them in nExcludedByStatus; a non-executable
	// accumulator does the opposite. One flag, two populations, and no way to build an
	// accumulator that silently takes both.
	nonExecutable bool

	nCandidate, nFilled, nMissed, nUnavailable, nNotApplicable int
	nExcludedByStatus                                          int
	pendingTruncated                                           int

	ret       map[int][]float64
	mfe       []float64
	mae       []float64
	bps       []float64
	invHits   int
	invDen    int
	t1Hits    int
	t1Den     int
	t2Hits    int
	t2Den     int
	bothDen   int
	ftStop    int
	ftTarget  int
	ftAmbig   int
	ftNeither int
}

// NewAcc returns an empty HEADLINE accumulator: it takes only §18-executable statuses.
func NewAcc() *Acc { return &Acc{ret: map[int][]float64{}} }

// NewNonExecutableAcc returns an accumulator for the §18 EXCLUDED block: it takes only the
// rows a headline accumulator refuses. Nothing is dropped by the pair; every row lands in
// exactly one of the two, which is what makes the funnel reconcile.
func NewNonExecutableAcc() *Acc { return &Acc{ret: map[int][]float64{}, nonExecutable: true} }

// Add folds one row in and keeps nothing but the samples.
//
// A row on the wrong side of the §18 population filter is COUNTED and returned, never
// averaged: nExcludedByStatus is reported beside NCandidate so a reader can see the size of
// the population this block is not describing.
func (a *Acc) Add(r *Row) {
	if r.ExecutableStatus == a.nonExecutable {
		a.nExcludedByStatus++
		return
	}
	switch r.Fill.Outcome {
	case FillNotApplicable:
		a.nNotApplicable++
		return
	case FillUnavailable:
		a.nCandidate++
		a.nUnavailable++
		return
	case FillMissed:
		a.nCandidate++
		a.nMissed++
		return
	}
	a.nCandidate++
	a.nFilled++

	if r.Out.Coverage == entryplanbacktest.WindowTruncated {
		a.pendingTruncated++
	}
	for h, v := range r.Out.Returns {
		a.ret[h] = append(a.ret[h], v)
	}
	if r.Out.MFE20 != nil {
		a.mfe = append(a.mfe, *r.Out.MFE20)
	}
	if r.Out.MAE20 != nil {
		a.mae = append(a.mae, *r.Out.MAE20)
	}
	if r.FillBpsOK {
		a.bps = append(a.bps, r.FillBps)
	}
	if r.Out.InvalidationHit != nil {
		a.invDen++
		if *r.Out.InvalidationHit {
			a.invHits++
		}
	}
	if r.Out.Target1Hit != nil {
		a.t1Den++
		if *r.Out.Target1Hit {
			a.t1Hits++
		}
	}
	if r.Out.Target2Hit != nil {
		a.t2Den++
		if *r.Out.Target2Hit {
			a.t2Hits++
		}
	}
	switch r.Out.FirstTouch {
	case entryplanbacktest.TouchStopOnly:
		a.ftStop++
		a.bothDen++
	case entryplanbacktest.TouchTargetOnly:
		a.ftTarget++
		a.bothDen++
	case entryplanbacktest.TouchAmbiguous:
		a.ftAmbig++
		a.bothDen++
	case entryplanbacktest.TouchNone:
		a.ftNeither++
		a.bothDen++
	}
}

// AddCandidateOnly counts a row into the population WITHOUT taking any sample from it.
//
// It exists for arm D. Arm D's population is arm B's candidates and its event is a MISS, so a
// filled row belongs in its denominator and in nothing else. Feeding a filled row to Add()
// instead — which the first implementation did — made the D cell report arm B's returns,
// excursions and hit rates under the label MISSED. That is the worst available presentation
// error in this study: a table of filled-trade outcomes titled "missed".
func (a *Acc) AddCandidateOnly(r *Row) {
	if r.ExecutableStatus == a.nonExecutable {
		a.nExcludedByStatus++
		return
	}
	switch r.Fill.Outcome {
	case FillNotApplicable:
		a.nNotApplicable++
	case FillUnavailable:
		a.nCandidate++
		a.nUnavailable++
	case FillMissed:
		a.nCandidate++
		a.nMissed++
	default:
		a.nCandidate++
		a.nFilled++
	}
}

// Finish produces the metric block.
func (a *Acc) Finish(arm Arm, wait int, segment string) Metrics {
	m := Metrics{Arm: arm, Wait: wait, Segment: segment, Returns: map[int]Dist{},
		NCandidate: a.nCandidate, NFilled: a.nFilled, NMissed: a.nMissed,
		NUnavailable: a.nUnavailable, NNotApplicable: a.nNotApplicable,
		NExcludedByStatus: a.nExcludedByStatus, Population: a.populationName(),
		PendingTruncated: a.pendingTruncated,
		FirstTouchStop:   a.ftStop, FirstTouchTarget: a.ftTarget,
		FirstTouchAmbiguous: a.ftAmbig, FirstTouchNeither: a.ftNeither,
	}
	for _, h := range Horizons {
		m.Returns[h] = Summarise(a.ret[h])
	}
	m.MFE20 = Summarise(a.mfe)
	m.MAE20 = Summarise(a.mae)
	m.FillVsSignalCloseBps = Summarise(a.bps)

	m.FillRate = NewRate(m.NFilled, m.NCandidate, "observations this arm had an order for")
	m.MissedRate = NewRate(m.NMissed, m.NCandidate, "observations this arm had an order for")
	m.InvalidationHitRate = NewRate(a.invHits, a.invDen, "filled observations whose plan published an invalidation")
	m.Target1HitRate = NewRate(a.t1Hits, a.t1Den, "filled observations whose plan published Target1")
	m.Target2HitRate = NewRate(a.t2Hits, a.t2Den, "filled observations whose plan published Target2")
	m.StopFirstRateConservative = NewRate(a.ftStop+a.ftAmbig, a.bothDen,
		"filled observations with BOTH levels; every AMBIGUOUS bar counted as stop-first")
	m.StopFirstRateOptimistic = NewRate(a.ftStop, a.bothDen,
		"filled observations with BOTH levels; no AMBIGUOUS bar counted as stop-first")
	return m
}

// Aggregate folds rows into one Metrics block. A thin wrapper over Acc.
func Aggregate(arm Arm, wait int, segment string, rows []Row) Metrics {
	a := NewAcc()
	for i := range rows {
		a.Add(&rows[i])
	}
	return a.Finish(arm, wait, segment)
}

// populationName says, on the metric block itself, which of the two §18 populations it
// describes. A detached table that does not name its population is the defect this field
// exists to prevent.
func (a *Acc) populationName() string {
	if a.nonExecutable {
		return "NON-EXECUTABLE STATUS (EP-9 §18 EXCLUDED: INSUFFICIENT_DATA / NO_VALID_ENTRY " +
			"plans that nevertheless published a zone). NOT part of any headline metric."
	}
	return "EXECUTABLE (EP-9 §18 headline: BUY_NOW / WAIT_PULLBACK / WAIT_BREAKOUT / TOO_EXTENDED)"
}

// Filter returns the rows a predicate selects. Used for segments; it never mutates.
func Filter(rows []Row, keep func(*Row) bool) []Row {
	out := make([]Row, 0, len(rows))
	for i := range rows {
		if keep(&rows[i]) {
			out = append(out, rows[i])
		}
	}
	return out
}
