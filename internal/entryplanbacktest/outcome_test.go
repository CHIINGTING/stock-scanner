package entryplanbacktest

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ── The horizon convention ────────────────────────────────────────────────────────────────

// TestTheHorizonConventionCountsSessionsAfterTheFill is the machine pin on the sentence
// "Return@5 is the close of the FIFTH session after the fill session". If somebody ever
// decides the fill session should count as session 1, this test is what they have to edit,
// and editing it after seeing performance is the thing HorizonCloseIndex's doc forbids.
func TestTheHorizonConventionCountsSessionsAfterTheFill(t *testing.T) {
	const fill = 7
	const barCount = 100
	cases := []struct {
		h    int
		want int
	}{
		{1, 8},
		{3, 10},
		{5, 12},
		{10, 17},
		{20, 27},
	}
	for _, c := range cases {
		got, ok := HorizonCloseIndex(fill, c.h, barCount)
		if !ok {
			t.Fatalf("h=%d unavailable inside a %d-bar series", c.h, barCount)
		}
		if got != c.want {
			t.Fatalf("Return@%d reads bar %d, want %d (fill bar %d + %d sessions). A result of "+
				"%d would mean the fill session is being counted as session 1.",
				c.h, got, c.want, fill, c.h, fill+c.h-1)
		}
	}
	// Stated as the property rather than only as a table.
	for h := 1; h <= ExcursionSessions; h++ {
		got, ok := HorizonCloseIndex(fill, h, barCount)
		if !ok || got-fill != h {
			t.Fatalf("Return@%d is %d sessions after the fill, want %d", h, got-fill, h)
		}
	}
}

func TestTheFillSessionsOwnCloseIsNotAForwardReturn(t *testing.T) {
	if i, ok := HorizonCloseIndex(7, 0, 100); ok {
		t.Fatalf("h=0 returned bar %d — the fill session's own close is a fill-quality "+
			"statistic, not Return@0", i)
	}
	if _, ok := HorizonCloseIndex(7, -1, 100); ok {
		t.Fatal("a negative horizon was accepted")
	}
	if _, ok := HorizonCloseIndex(-1, 5, 100); ok {
		t.Fatal("a negative fill index was accepted")
	}
}

func TestAHorizonPastTheDataIsUnavailableAndNotTruncated(t *testing.T) {
	// 10 bars, fill on bar 8: only h=1 exists. h=2 must be UNAVAILABLE, never the last bar.
	if i, ok := HorizonCloseIndex(8, 1, 10); !ok || i != 9 {
		t.Fatalf("h=1 gave (%d,%v), want (9,true)", i, ok)
	}
	for h := 2; h <= 5; h++ {
		if i, ok := HorizonCloseIndex(8, h, 10); ok {
			t.Fatalf("h=%d gave bar %d in a 10-bar series — a horizon that ran out of data "+
				"must not be silently answered with the newest bar", h, i)
		}
	}
}

// ── The excursion window ──────────────────────────────────────────────────────────────────

func TestTheExcursionWindowIsTwentySessionsAndExcludesTheFillSession(t *testing.T) {
	lo, hi, cov := ExcursionWindow(10, 200)
	if cov != WindowComplete {
		t.Fatalf("coverage %q, want %q", cov, WindowComplete)
	}
	if lo != 11 {
		t.Fatalf("window starts at bar %d, want 11 — the fill bar 10 must be excluded because "+
			"daily OHLC cannot say whether its High/Low happened before or after the fill", lo)
	}
	if hi != 30 {
		t.Fatalf("window ends at bar %d, want 30", hi)
	}
	if n := hi - lo + 1; n != ExcursionSessions {
		t.Fatalf("window holds %d sessions, want %d", n, ExcursionSessions)
	}
}

func TestATruncatedExcursionWindowIsPendingAndNotAnExclusion(t *testing.T) {
	lo, hi, cov := ExcursionWindow(10, 20) // bars 0..19; only 11..19 exist
	if cov != WindowTruncated {
		t.Fatalf("coverage %q, want %q", cov, WindowTruncated)
	}
	if lo != 11 || hi != 19 {
		t.Fatalf("truncated window [%d,%d], want [11,19]", lo, hi)
	}
	if cov == WindowUnavailable {
		t.Fatal("a real fill with a short window must not be UNAVAILABLE")
	}
}

func TestAFillOnTheNewestBarHasNoExcursionWindowAtAll(t *testing.T) {
	_, _, cov := ExcursionWindow(19, 20)
	if cov != WindowUnavailable {
		t.Fatalf("coverage %q, want %q — there is no session after the fill", cov, WindowUnavailable)
	}
	for _, c := range []WindowCoverage{WindowComplete, WindowTruncated, WindowUnavailable} {
		if !c.Valid() {
			t.Fatalf("%q is not Valid", c)
		}
	}
	if WindowCoverage("").Valid() || WindowCoverage("PARTIAL").Valid() {
		t.Fatal("an undefined coverage passed Valid")
	}
}

// ── Same-bar ambiguity ────────────────────────────────────────────────────────────────────

func TestBothLevelsInsideOneBarIsAmbiguousAndNeverResolved(t *testing.T) {
	bar := fetcher.Candle{Open: 100, High: 112, Low: 94, Close: 105}
	got := ClassifyTouch(bar, 95, 110)
	if got != TouchAmbiguous {
		t.Fatalf("stop 95 and target 110 both inside [%v,%v] classified %q, want %q — "+
			"resolving this either way fabricates an execution sequence daily OHLC does not "+
			"record", bar.Low, bar.High, got, TouchAmbiguous)
	}
	if got == TouchStopOnly || got == TouchTargetOnly {
		t.Fatal("the ambiguous bar was resolved to one side")
	}
}

func TestTheTouchClassificationTable(t *testing.T) {
	bar := fetcher.Candle{Open: 100, High: 110, Low: 95, Close: 104}
	cases := []struct {
		name         string
		stop, target float64
		want         TouchClass
	}{
		{"neither reached", 90, 120, TouchNone},
		{"target only", 90, 110, TouchTargetOnly},
		{"stop only", 95, 120, TouchStopOnly},
		{"both", 95, 110, TouchAmbiguous},
		{"target exactly at the high is a touch", 90, 110, TouchTargetOnly},
		{"stop exactly at the low is a touch", 95, 120, TouchStopOnly},
		{"target one tick above the high is not", 90, 110.1, TouchNone},
		{"stop one tick below the low is not", 94.9, 120, TouchNone},
		{"stop above target is malformed", 120, 90, TouchUnavailable},
		{"stop equal to target is malformed", 100, 100, TouchUnavailable},
		{"stop is not a price", 0, 110, TouchUnavailable},
		{"target is not a price", 95, 0, TouchUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyTouch(bar, c.stop, c.target); got != c.want {
				t.Fatalf("stop=%v target=%v classified %q, want %q", c.stop, c.target, got, c.want)
			}
		})
	}
}

func TestAnUnusableBarIsUnavailableAndNotNoTouch(t *testing.T) {
	cases := []struct {
		name string
		bar  fetcher.Candle
	}{
		{"no high", fetcher.Candle{High: 0, Low: 95}},
		{"no low", fetcher.Candle{High: 110, Low: 0}},
		{"negative low", fetcher.Candle{High: 110, Low: -1}},
		{"high below low", fetcher.Candle{High: 90, Low: 95}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyTouch(c.bar, 95, 110)
			if got != TouchUnavailable {
				t.Fatalf("classified %q, want %q — MISSING is not NO_TOUCH", got, TouchUnavailable)
			}
		})
	}
}

func TestEveryTouchClassIsDefined(t *testing.T) {
	for _, c := range []TouchClass{TouchNone, TouchTargetOnly, TouchStopOnly, TouchAmbiguous, TouchUnavailable} {
		if !c.Valid() || c == "" {
			t.Fatalf("%q is not a defined touch class", c)
		}
	}
	if TouchClass("").Valid() || TouchClass("STOP_FIRST").Valid() {
		t.Fatal("an undefined touch class passed Valid")
	}
	// The wire value is pinned: it is the string a reader of a Phase 2 CSV sees.
	if TouchAmbiguous != "AMBIGUOUS_STOP_TARGET_ORDER" {
		t.Fatalf("the ambiguity class serializes as %q", TouchAmbiguous)
	}
}
