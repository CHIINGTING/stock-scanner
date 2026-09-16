package study

import (
	"fmt"
	"math"

	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
)

// ── EP-9 §24: integrity ───────────────────────────────────────────────────────────────────
//
// The rule these checks exist to enforce: A HIT IS A DEFECT TO REPORT AND FIX IN METHODOLOGY,
// NEVER A ROW TO CLEAN SILENTLY. Nothing here drops, repairs or rounds an observation. It
// records what it found, with the identity, and the run reports the counts whether they are
// zero or not — a run that prints no integrity section is indistinguishable from a run whose
// checks were removed.

// IntegrityCode names one class of defect.
type IntegrityCode string

const (
	IntegrityNonFinite         IntegrityCode = "NON_FINITE_VALUE"
	IntegrityNonPositiveFill   IntegrityCode = "NON_POSITIVE_FILL_PRICE"
	IntegrityImpossiblePrice   IntegrityCode = "IMPOSSIBLE_PRICE"
	IntegrityStopAboveEntry    IntegrityCode = "INVALIDATION_NOT_BELOW_ENTRY"
	IntegrityTargetBelowEntry  IntegrityCode = "TARGET_NOT_ABOVE_ENTRY"
	IntegrityZoneInverted      IntegrityCode = "ZONE_LOW_ABOVE_HIGH"
	IntegrityChaseBelowZone    IntegrityCode = "CHASE_BELOW_ZONE_HIGH"
	IntegrityFillOnSignalBar   IntegrityCode = "EXECUTABLE_FILL_ON_SIGNAL_BAR"
	IntegrityFillOutsideWindow IntegrityCode = "FILL_OUTSIDE_WAIT_WINDOW"
	IntegrityFutureEvidence    IntegrityCode = "FUTURE_DATED_EVIDENCE"
	IntegrityDuplicateIdentity IntegrityCode = "DUPLICATE_IDENTITY"
	IntegrityArmMixing         IntegrityCode = "REGIME_PROVENANCE_MIXED"
)

// AllIntegrityCodes is every check, in report order.
var AllIntegrityCodes = []IntegrityCode{
	IntegrityNonFinite, IntegrityNonPositiveFill, IntegrityImpossiblePrice,
	IntegrityStopAboveEntry, IntegrityTargetBelowEntry, IntegrityZoneInverted,
	IntegrityChaseBelowZone, IntegrityFillOnSignalBar, IntegrityFillOutsideWindow,
	IntegrityFutureEvidence, IntegrityDuplicateIdentity, IntegrityArmMixing,
}

// IntegrityFinding is one defect, with enough identity to find it again.
type IntegrityFinding struct {
	Code   IntegrityCode `json:"code"`
	Symbol string        `json:"symbol"`
	AsOf   string        `json:"as_of"`
	Arm    Arm           `json:"arm"`
	Wait   int           `json:"wait_sessions"`
	Detail string        `json:"detail"`
}

// IntegrityReport is the whole §24 result.
type IntegrityReport struct {
	Checked  int                   `json:"rows_checked"`
	ByCode   map[IntegrityCode]int `json:"by_code"`
	Findings []IntegrityFinding    `json:"findings,omitempty"`
	// MaxFindings caps the recorded examples; ByCode is never capped, so a truncated Findings
	// list can never make a count look smaller than it is.
	MaxFindings int `json:"max_findings"`
}

// NewIntegrityReport starts a report that records at most max examples per run.
func NewIntegrityReport(max int) *IntegrityReport {
	r := &IntegrityReport{ByCode: map[IntegrityCode]int{}, MaxFindings: max}
	for _, c := range AllIntegrityCodes {
		r.ByCode[c] = 0 // every code present at zero, so "no check ran" is visible
	}
	return r
}

func (r *IntegrityReport) add(code IntegrityCode, o *Observation, arm Arm, wait int, detail string) {
	r.ByCode[code]++
	if len(r.Findings) < r.MaxFindings {
		r.Findings = append(r.Findings, IntegrityFinding{Code: code, Symbol: o.Symbol,
			AsOf: o.SignalAsOf, Arm: arm, Wait: wait, Detail: detail})
	}
}

// Total is how many defects were found across every code.
func (r *IntegrityReport) Total() int {
	var n int
	for _, v := range r.ByCode {
		n += v
	}
	return n
}

// Check runs every §24 check over one row and records what it finds.
//
// ids is the run's identity registry; a duplicate is recorded as a defect AND rejected by the
// registry itself, so the same observation can never be counted twice into a metric.
func (r *IntegrityReport) Check(row *Row, ids *entryplanbacktest.ObservationSet,
	expectedProvenance entryplanbacktest.RegimeProvenance) {

	o := row.Obs
	r.Checked++

	// 1. Identity — duplicates fail loudly.
	id := o.ID(row.Arm, row.Wait)
	if err := ids.Add(id); err != nil {
		r.add(IntegrityDuplicateIdentity, o, row.Arm, row.Wait, err.Error())
	}

	// 2. Provenance — a row must carry the arm's own provenance, never the other one's.
	if o.RegimeProvenance != expectedProvenance {
		r.add(IntegrityArmMixing, o, row.Arm, row.Wait,
			fmt.Sprintf("row carries %s in a %s pass", o.RegimeProvenance, expectedProvenance))
	}

	// 3. Published prices — geometry the plan itself promises.
	if o.ZoneLow != nil && o.ZoneHigh != nil && *o.ZoneLow > *o.ZoneHigh {
		r.add(IntegrityZoneInverted, o, row.Arm, row.Wait,
			fmt.Sprintf("zone [%v, %v]", *o.ZoneLow, *o.ZoneHigh))
	}
	if o.MaxChase != nil && o.ZoneHigh != nil && *o.MaxChase < *o.ZoneHigh {
		r.add(IntegrityChaseBelowZone, o, row.Arm, row.Wait,
			fmt.Sprintf("chase %v < zone high %v", *o.MaxChase, *o.ZoneHigh))
	}
	if o.Invalidation != nil && o.ZoneHigh != nil && *o.Invalidation >= *o.ZoneHigh {
		r.add(IntegrityStopAboveEntry, o, row.Arm, row.Wait,
			fmt.Sprintf("invalidation %v >= zone high %v", *o.Invalidation, *o.ZoneHigh))
	}
	if o.Target1 != nil && o.ZoneHigh != nil && *o.Target1 <= *o.ZoneHigh {
		r.add(IntegrityTargetBelowEntry, o, row.Arm, row.Wait,
			fmt.Sprintf("target1 %v <= zone high %v", *o.Target1, *o.ZoneHigh))
	}
	for name, p := range map[string]*float64{"zone_low": o.ZoneLow, "zone_high": o.ZoneHigh,
		"max_chase": o.MaxChase, "invalidation": o.Invalidation,
		"target_1": o.Target1, "target_2": o.Target2} {
		if p == nil {
			continue
		}
		if math.IsNaN(*p) || math.IsInf(*p, 0) {
			r.add(IntegrityNonFinite, o, row.Arm, row.Wait, name)
		} else if *p <= 0 {
			r.add(IntegrityImpossiblePrice, o, row.Arm, row.Wait,
				fmt.Sprintf("%s = %v", name, *p))
		}
	}

	// 4. The fill — the temporal contract, checked on the RESULT and not only in the fill code.
	if row.Fill.Filled() {
		if !(row.Fill.Price > 0) || math.IsNaN(row.Fill.Price) || math.IsInf(row.Fill.Price, 0) {
			r.add(IntegrityNonPositiveFill, o, row.Arm, row.Wait,
				fmt.Sprintf("fill price %v", row.Fill.Price))
		}
		if row.Arm.Executable() {
			if row.Fill.BarIndex <= o.sigIdx {
				r.add(IntegrityFillOnSignalBar, o, row.Arm, row.Wait,
					fmt.Sprintf("fill bar %d <= signal bar %d", row.Fill.BarIndex, o.sigIdx))
			}
			if row.Fill.SessionsWaited < 1 || row.Fill.SessionsWaited > row.Wait {
				r.add(IntegrityFillOutsideWindow, o, row.Arm, row.Wait,
					fmt.Sprintf("waited %d sessions, window is 1..%d",
						row.Fill.SessionsWaited, row.Wait))
			}
		}
		// 5. Evidence dating — the fill must not be dated on or before the signal session for
		// an executable arm, and must never be dated in a session the axis does not hold.
		if row.Arm.Executable() && row.Fill.Date != "" && row.Fill.Date <= o.SignalAsOf {
			r.add(IntegrityFutureEvidence, o, row.Arm, row.Wait,
				fmt.Sprintf("fill dated %s on a signal dated %s", row.Fill.Date, o.SignalAsOf))
		}
	}

	// 6. Outcome values.
	for name, p := range map[string]*float64{"mfe_20": row.Out.MFE20, "mae_20": row.Out.MAE20} {
		if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0)) {
			r.add(IntegrityNonFinite, o, row.Arm, row.Wait, name)
		}
	}
	for h, v := range row.Out.Returns {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			r.add(IntegrityNonFinite, o, row.Arm, row.Wait, fmt.Sprintf("return@%d", h))
		}
	}
}
