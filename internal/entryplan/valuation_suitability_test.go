package entryplan_test

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

// The Target2 ceiling rule is EP-4's ValuationSuitability.PermitsCeiling (input.go): a KNOWN
// suitability code other than UNSUITABLE. It is NOT "SUITABLE only" — CONDITIONAL, WEAK and
// INSUFFICIENT_DATA also cap (valuation.ClassifySuitability yields CONDITIONAL for an unbroken
// P/E over a sufficient window with no filed earnings basis). This pins every state both at the predicate and end to end through ComputePlan on
// riskBase, where a 110 ceiling is below the structural 117 Target2 and so bites when permitted.
func TestTheValuationCeilingIsPermittedForEveryKnownVerdictExceptUnsuitable(t *testing.T) {
	cases := []struct {
		s    entryplan.ValuationSuitability
		caps bool
	}{
		{entryplan.SuitabilitySuitable, true},
		{entryplan.SuitabilityConditional, true},
		{entryplan.SuitabilityWeak, true},
		{entryplan.SuitabilityInsufficientData, true},
		{entryplan.SuitabilityUnsuitable, false},
		{"", false},
		{entryplan.ValuationSuitability("SOME_NEWER_FU11_CODE"), false},
	}
	ran, capped := 0, 0
	for _, c := range cases {
		if got := c.s.PermitsCeiling(); got != c.caps {
			t.Errorf("%q.PermitsCeiling() = %v, want %v", c.s, got, c.caps)
		}
		in := riskBase()
		in.Valuation = valuation(110, c.s)
		p := entryplan.ComputePlan(in)
		ran++
		if p.Target2 == nil || p.EntryTrace == nil {
			t.Fatalf("%q: fixture lost Target2 or trace: %+v", c.s, p)
		}
		wantT2, wantOutcome := 117.0, entryplan.ValuationCeilingModelUnsuitable
		if c.caps {
			wantT2, wantOutcome = 110.0, entryplan.ValuationCeilingApplied
			capped++
		}
		if p.Target2.Price != wantT2 || p.EntryTrace.Targets.ValuationCeiling != wantOutcome {
			t.Errorf("%q: Target2 %v / ceiling %q, want %v / %q", c.s, p.Target2.Price,
				p.EntryTrace.Targets.ValuationCeiling, wantT2, wantOutcome)
		}
	}
	if ran != 7 || capped != 4 {
		t.Fatalf("ran %d (want 7), capping states %d (want 4)", ran, capped)
	}
}
