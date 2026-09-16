package provider

import (
	"math"
	"testing"
)

// The sentinel rule, stated the way §3.1 states it: normalise first, then anything that still
// does not parse is ABSENT — a nil, not a zero, and not an error.
//
// Three drafts of that section enumerated the sentinels they had seen and were wrong each
// time, so the table below checks the RULE (what parses, what does not) rather than a list.
func TestDecodeNumAppliesTheAbsenceRule(t *testing.T) {
	for _, tc := range []struct {
		name    string
		token   string
		want    *float64
		known   bool // recognised: parsed, or a known absence sentinel
		percent bool
	}{
		{name: `"-" is absent, not zero`, token: "-", want: nil, known: true},
		{name: `"" is absent`, token: "", want: nil, known: true},
		{name: `"NULL" is absent`, token: "NULL", want: nil, known: true},
		{name: `"0" is an OBSERVED ZERO, not a sentinel`, token: "0", want: f(0), known: true},
		{name: `"0.00" is an observed zero`, token: "0.00", want: f(0), known: true},
		{name: `thousands separators are stripped`, token: "1,234", want: f(1234), known: true},
		{name: `"999" parses without help`, token: "999", want: f(999), known: true},
		{name: `negatives survive`, token: "-1,856", want: f(-1856), known: true},
		{name: `surrounding whitespace is trimmed`, token: "  42 ", want: f(42), known: true},
		{name: `a percentage keeps its value`, token: "9.98%", want: f(9.98), known: true, percent: true},
		{name: `a zero percentage is an observed zero`, token: "0.00%", want: f(0), known: true, percent: true},
		{name: `a negative percentage survives`, token: "-1.20%", want: f(-1.2), known: true, percent: true},
		{name: `"-" in a percentage column is still absent`, token: "-", want: nil, known: true, percent: true},
		{name: `an unknown token is ABSENT and reported`, token: "n/a", want: nil, known: false},
		{name: `Chinese text is absent and reported`, token: "無", want: nil, known: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *float64
			var ok bool
			if tc.percent {
				got, ok = decodePercent(tc.token)
			} else {
				got, ok = decodeNum(tc.token)
			}
			if tc.want == nil && got != nil {
				t.Fatalf("token %q: want ABSENT, got %v", tc.token, *got)
			}
			if tc.want != nil {
				if got == nil {
					t.Fatalf("token %q: want %v, got ABSENT", tc.token, *tc.want)
				}
				if math.Abs(*got-*tc.want) > 1e-9 {
					t.Fatalf("token %q: want %v, got %v", tc.token, *tc.want, *got)
				}
			}
			if ok != tc.known {
				t.Fatalf("token %q: recognised = %v, want %v", tc.token, ok, tc.known)
			}
		})
	}
}

// Without the percent strip, DailyMarketReportFut's 727 genuine "%" values would every one of
// them decode to ABSENT *and* be logged unknown — 783 lines of daily noise for 727 dropped
// observations. This pins the asymmetry: the plain path does NOT strip it.
func TestPercentIsStrippedOnlyWherePercentIsDeclared(t *testing.T) {
	if v, ok := decodeNum("9.98%"); v != nil || ok {
		t.Fatalf("plain decode of %q must be an UNKNOWN absence, got v=%v ok=%v", "9.98%", v, ok)
	}
	v, ok := decodePercent("9.98%")
	if v == nil || *v != 9.98 || !ok {
		t.Fatalf("percent decode of %q: got v=%v ok=%v, want 9.98/true", "9.98%", v, ok)
	}
}

// A NOT NULL column has nowhere to put an absence, which is where §6.2's rejected row starts.
func TestDecodeRequiredNumRefusesWhatDecodeNumAbsorbs(t *testing.T) {
	if _, err := decodeRequiredNum("46000"); err != nil {
		t.Fatalf("good strike: %v", err)
	}
	for _, bad := range []string{"-", "", "NULL", "四萬六"} {
		if _, err := decodeRequiredNum(bad); err == nil {
			t.Fatalf("required column accepted %q — a keyless row would be stored", bad)
		}
	}
}

func f(v float64) *float64 { return &v }
