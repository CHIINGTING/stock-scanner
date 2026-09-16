package entryplanbacktest

import (
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/r6backtest"
)

// fakeSessions is a SymbolSessions built from an explicit date list. It exists so the temporal
// contract is tested against sessions chosen to hit the awkward cases — a Friday, a holiday, a
// suspension — rather than against whatever the cache happens to contain.
type fakeSessions struct{ dates []string }

func (f fakeSessions) IndexOf(d string) (int, bool) {
	for i, x := range f.dates {
		if x == d {
			return i, true
		}
	}
	return -1, false
}

func (f fakeSessions) BarCount() int { return len(f.dates) }

// 2026-09-11 is a Friday and 2026-09-14 the following Monday (verified below by weekday, not
// by assertion). 2026-09-16 is deliberately absent: a mid-week session that did not trade.
var weekSessions = fakeSessions{dates: []string{
	"2026-09-10", // Thu
	"2026-09-11", // Fri
	"2026-09-14", // Mon
	"2026-09-15", // Tue
	"2026-09-17", // Thu — 09-16 is missing on purpose
}}

func TestTheFixtureIsAFridayFollowedByAMonday(t *testing.T) {
	fri, err := time.Parse(DateLayout, "2026-09-11")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	mon, err := time.Parse(DateLayout, "2026-09-14")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fri.Weekday() != time.Friday {
		t.Fatalf("2026-09-11 is %v, want Friday — the calendar fixture is wrong", fri.Weekday())
	}
	if mon.Weekday() != time.Monday {
		t.Fatalf("2026-09-14 is %v, want Monday", mon.Weekday())
	}
	if d := mon.Sub(fri).Hours() / 24; d != 3 {
		t.Fatalf("Friday to Monday is %v days, want 3", d)
	}
}

// ── The contract's fourth line ────────────────────────────────────────────────────────────

func TestTheSignalBarCanNeverBeFilled(t *testing.T) {
	for i, d := range weekSessions.dates {
		idx, el := EarliestExecutableIndex(weekSessions, d)
		if i == len(weekSessions.dates)-1 {
			if el != NoFollowingSession {
				t.Fatalf("%s is the newest bar: got %q, want %q", d, el, NoFollowingSession)
			}
			continue
		}
		if el != EligibleForExecution {
			t.Fatalf("%s: eligibility %q, want %q", d, el, EligibleForExecution)
		}
		if idx <= i {
			t.Fatalf("%s: earliest executable bar %d is not AFTER the signal bar %d — this is "+
				"the same-bar fill the contract forbids", d, idx, i)
		}
		if idx != i+1 {
			t.Fatalf("%s: earliest executable bar %d, want %d (T+1 session)", d, idx, i+1)
		}
	}
}

func TestTheExecutionWindowNeverIncludesTheSignalBar(t *testing.T) {
	for wait := 1; wait <= MaxWaitSessions; wait++ {
		lo, hi, el := ExecutionWindow(weekSessions, "2026-09-10", wait)
		if el != EligibleForExecution {
			t.Fatalf("wait=%d: eligibility %q", wait, el)
		}
		if lo != 1 {
			t.Fatalf("wait=%d: window starts at bar %d, want 1 (the signal is bar 0)", wait, lo)
		}
		if hi < lo {
			t.Fatalf("wait=%d: window [%d,%d] is empty", wait, lo, hi)
		}
		if maxBar := weekSessions.BarCount() - 1; hi > maxBar {
			t.Fatalf("wait=%d: window runs to bar %d past the last bar %d", wait, hi, maxBar)
		}
	}
}

func TestAZeroOrNegativeWaitWindowIsRefusedRatherThanClamped(t *testing.T) {
	for _, wait := range []int{0, -1} {
		lo, hi, el := ExecutionWindow(weekSessions, "2026-09-10", wait)
		if el == EligibleForExecution {
			t.Fatalf("wait=%d produced an eligible window [%d,%d] — 0 sessions after T is a "+
				"same-bar fill", wait, lo, hi)
		}
	}
}

// ── Friday, holidays and missing sessions ─────────────────────────────────────────────────

func TestAFridaySignalIsEligibleOnTheNextSessionNotOnSaturday(t *testing.T) {
	axis, err := NewSessionAxis(weekSessions.dates)
	if err != nil {
		t.Fatalf("axis: %v", err)
	}
	next, ok := axis.Next("2026-09-11")
	if !ok {
		t.Fatal("Friday 2026-09-11 has no next session on an axis that contains one")
	}
	if next != "2026-09-14" {
		t.Fatalf("next session after Friday is %q, want the following Monday 2026-09-14", next)
	}
	if _, present := axis.Index("2026-09-12"); present {
		t.Fatal("Saturday 2026-09-12 is on the session axis — the axis is calendar days, not sessions")
	}
	// And the same fact through the executable-session helper, which is what a study calls.
	idx, el := EarliestExecutableIndex(weekSessions, "2026-09-11")
	if el != EligibleForExecution {
		t.Fatalf("Friday signal: %q", el)
	}
	if got := weekSessions.dates[idx]; got != "2026-09-14" {
		t.Fatalf("Friday signal fills on %q, want 2026-09-14", got)
	}
}

func TestAMissingMidWeekSessionIsSkippedAndNotInvented(t *testing.T) {
	axis, err := NewSessionAxis(weekSessions.dates)
	if err != nil {
		t.Fatalf("axis: %v", err)
	}
	if _, present := axis.Index("2026-09-16"); present {
		t.Fatal("2026-09-16 is on the axis but was never given to it")
	}
	next, ok := axis.Next("2026-09-15")
	if !ok || next != "2026-09-17" {
		t.Fatalf("next session after 2026-09-15 is (%q,%v), want 2026-09-17 — the closure is "+
			"stepped over, not filled in", next, ok)
	}
}

func TestASessionThisSymbolNeverTradedIsUnavailableAndNeverSnappedToANeighbour(t *testing.T) {
	idx, el := EarliestExecutableIndex(weekSessions, "2026-09-16")
	if el != SessionUnavailable {
		t.Fatalf("a date with no bar reported %q, want %q — a guessed session is the one "+
			"failure this contract forbids outright", el, SessionUnavailable)
	}
	if idx != -1 {
		t.Fatalf("unavailable session returned bar index %d, want -1", idx)
	}
	// Belt and braces: the neighbours exist, so a snapping implementation would have found one.
	if _, ok := weekSessions.IndexOf("2026-09-15"); !ok {
		t.Fatal("fixture broken: 2026-09-15 should exist")
	}
	if _, ok := weekSessions.IndexOf("2026-09-17"); !ok {
		t.Fatal("fixture broken: 2026-09-17 should exist")
	}
}

func TestNilSessionsAreUnavailableRatherThanAPanic(t *testing.T) {
	if _, el := EarliestExecutableIndex(nil, "2026-09-10"); el != SessionUnavailable {
		t.Fatalf("nil SymbolSessions gave %q, want %q", el, SessionUnavailable)
	}
	var s StockSessions // zero value: nil *Stock
	if _, ok := s.IndexOf("2026-09-10"); ok {
		t.Fatal("a nil-backed StockSessions claims to know a session")
	}
	if n := s.BarCount(); n != 0 {
		t.Fatalf("a nil-backed StockSessions has %d bars, want 0", n)
	}
}

// ── The axis invariants ───────────────────────────────────────────────────────────────────

func TestTheSessionAxisRefusesWhatItCannotIndex(t *testing.T) {
	cases := []struct {
		name  string
		dates []string
		want  string
	}{
		{"empty", nil, "empty"},
		{"not a date", []string{"2026-09-10", "not-a-date"}, "not YYYY-MM-DD"},
		{"duplicate", []string{"2026-09-10", "2026-09-10"}, "not strictly ascending"},
		{"descending", []string{"2026-09-11", "2026-09-10"}, "not strictly ascending"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewSessionAxis(c.dates)
			if err == nil {
				t.Fatalf("accepted %v", c.dates)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestTheAxisIsACopyAndCannotBeMutatedThroughTheCaller(t *testing.T) {
	src := []string{"2026-09-10", "2026-09-11", "2026-09-14"}
	axis, err := NewSessionAxis(src)
	if err != nil {
		t.Fatalf("axis: %v", err)
	}
	src[1] = "1999-01-01"
	if d, _ := axis.At(1); d != "2026-09-11" {
		t.Fatalf("axis session 1 is %q after the caller mutated its slice, want 2026-09-11", d)
	}
}

func TestAxisFromUniverseReusesTheRepoSessionAxis(t *testing.T) {
	// Universe.Axis is the sorted unique set of dates on which SOME cached symbol printed a
	// bar (internal/r6backtest/engine.go:70-87). This asserts the reuse is real rather than a
	// comment: the axis this package produces IS that field's contents.
	u := &r6backtest.Universe{Axis: []string{"2026-09-14", "2026-09-10", "2026-09-11"}}
	axis, err := AxisFromUniverse(u)
	if err != nil {
		t.Fatalf("AxisFromUniverse: %v", err)
	}
	if axis.Len() != 3 {
		t.Fatalf("axis length %d, want 3", axis.Len())
	}
	for i, want := range []string{"2026-09-10", "2026-09-11", "2026-09-14"} {
		if got, _ := axis.At(i); got != want {
			t.Fatalf("session %d is %q, want %q", i, got, want)
		}
	}
	// The caller's slice is not reordered underneath it.
	if u.Axis[0] != "2026-09-14" {
		t.Fatalf("AxisFromUniverse sorted the universe's own slice in place")
	}
	if _, err := AxisFromUniverse(nil); err == nil {
		t.Fatal("a nil universe produced an axis")
	}
}

func TestStockSessionsDelegatesToTheLoadedStock(t *testing.T) {
	// *r6backtest.Stock's date map is unexported and LoadUniverse is its only constructor, so
	// a hand-built Stock knows no dates. What IS checkable here is that the adapter reports
	// the stock's real bar count and refuses dates the stock does not know, i.e. that it adds
	// no calendar of its own.
	st := &r6backtest.Stock{Symbol: "2330", Candles: []fetcher.Candle{{}, {}, {}}}
	a := StockSessions{S: st}
	if n := a.BarCount(); n != 3 {
		t.Fatalf("BarCount %d, want 3", n)
	}
	if i, ok := a.IndexOf("2026-09-10"); ok || i != -1 {
		t.Fatalf("IndexOf on a stock with no date map returned (%d,%v), want (-1,false)", i, ok)
	}
	if _, el := EarliestExecutableIndex(a, "2026-09-10"); el != SessionUnavailable {
		t.Fatalf("eligibility %q, want %q", el, SessionUnavailable)
	}
}

func TestEveryEligibilityValueIsDefined(t *testing.T) {
	for _, e := range []Eligibility{EligibleForExecution, SessionUnavailable, NoFollowingSession} {
		if !e.Valid() {
			t.Fatalf("%q is not Valid", e)
		}
		if e == "" {
			t.Fatal("an eligibility is the empty string")
		}
	}
	if Eligibility("").Valid() || Eligibility("MAYBE").Valid() {
		t.Fatal("an undefined eligibility passed Valid")
	}
}
