package dailydata

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// step builds a stub that records how many times it ran.
func step(name string, calls *int, out func() SourceReport) Step {
	return Step{Name: name, Run: func(context.Context, Day) SourceReport {
		*calls++
		return out()
	}}
}

func ok(n int) SourceReport {
	return SourceReport{Status: StatusComplete, Expected: n, Available: n}
}
func fail(reason string) SourceReport {
	return SourceReport{Status: StatusFailed, Reason: reason}
}

func day() Day {
	return Day{Date: "2026-09-08", Now: at("2026-09-08 14:30"), Session: SessionClosed,
		Symbols: []string{"2330.TW"}}
}

// ── the rerun contract ────────────────────────────────────────────────────────────────

// The case the operator actually lives with: one source is late, the run reports PARTIAL, and
// running it again finishes the day WITHOUT redoing what already worked.
//
// PARTIAL and FAILED have to stay distinct for exactly this reason — PARTIAL means "run me
// again later", and an operator who cannot tell them apart will either abandon recoverable
// days or retry unrecoverable ones forever.
func TestPartialThenCompleteOnRerun(t *testing.T) {
	var priceCalls, valCalls, instCalls int
	instOK := false

	steps := []Step{
		step("price", &priceCalls, func() SourceReport { return ok(1) }),
		step("valuation", &valCalls, func() SourceReport { return ok(14) }),
		step("institution", &instCalls, func() SourceReport {
			if instOK {
				return ok(2)
			}
			return fail("交易所尚未公布")
		}),
	}
	p := &Pipeline{Steps: steps}

	// First run: institution is late.
	first := p.Run(context.Background(), day())
	if first.Status != StatusPartial {
		t.Fatalf("first run = %s, want PARTIAL", first.Status)
	}
	inst, _ := first.Source("institution")
	if inst.Status != StatusFailed || inst.Reason == "" {
		t.Fatalf("institution = %+v, want FAILED with a reason", inst)
	}
	for _, n := range []string{"price", "valuation"} {
		s, _ := first.Source(n)
		if s.Status != StatusComplete {
			t.Fatalf("%s = %s on the first run", n, s.Status)
		}
	}

	// Second run: the source has caught up.
	instOK = true
	second := p.Run(context.Background(), day())
	if second.Status != StatusComplete {
		t.Fatalf("second run = %s, want COMPLETE", second.Status)
	}
	if s, _ := second.Source("institution"); s.Status != StatusComplete {
		t.Fatalf("institution still %s", s.Status)
	}

	// Each step ran once per run — the pipeline does not multiply work on a rerun. Whether a
	// step's own writes are idempotent is the step's contract, tested where it lives.
	if priceCalls != 2 || valCalls != 2 || instCalls != 2 {
		t.Fatalf("call counts price=%d valuation=%d institution=%d, want 2 each",
			priceCalls, valCalls, instCalls)
	}
}

// A failing source must not abandon the ones after it. Sources are independent, and one
// throttled exchange turning into a whole missing day is the failure mode this prevents.
func TestOneFailureDoesNotStopTheRest(t *testing.T) {
	var a, b, c int
	p := &Pipeline{Steps: []Step{
		step("first", &a, func() SourceReport { return fail("down") }),
		step("second", &b, func() SourceReport { return ok(1) }),
		step("third", &c, func() SourceReport { return ok(1) }),
	}}
	rep := p.Run(context.Background(), day())
	if a != 1 || b != 1 || c != 1 {
		t.Fatalf("steps after a failure did not run: %d %d %d", a, b, c)
	}
	if rep.Status != StatusPartial {
		t.Fatalf("status = %s, want PARTIAL", rep.Status)
	}
}

// ── the verdict ───────────────────────────────────────────────────────────────────────

func TestOverallStatus(t *testing.T) {
	cases := []struct {
		name string
		in   []Status
		want Status
	}{
		{"all complete", []Status{StatusComplete, StatusComplete}, StatusComplete},
		{"one failed", []Status{StatusComplete, StatusFailed}, StatusPartial},
		{"one partial", []Status{StatusComplete, StatusPartial}, StatusPartial},
		{"all failed", []Status{StatusFailed, StatusFailed}, StatusFailed},
		{"all no session", []Status{StatusNoSession, StatusNoSession}, StatusNoSession},
		{"complete plus skipped", []Status{StatusComplete, StatusSkipped}, StatusComplete},
		{"skipped plus failed", []Status{StatusSkipped, StatusFailed}, StatusFailed},
		{"nothing at all", nil, StatusFailed},
	}
	for _, tc := range cases {
		var ss []SourceReport
		for _, s := range tc.in {
			ss = append(ss, SourceReport{Status: s})
		}
		if got := overall(ss); got != tc.want {
			t.Errorf("%s: %v → %s, want %s", tc.name, tc.in, got, tc.want)
		}
	}
}

// A run must never call itself COMPLETE while a source produced nothing. That single
// misreport is the whole reason this layer exists.
func TestPartialIsNeverReportedAsComplete(t *testing.T) {
	var n int
	p := &Pipeline{Steps: []Step{
		step("good", &n, func() SourceReport { return ok(14) }),
		step("empty", &n, func() SourceReport {
			return SourceReport{Status: StatusPartial, Expected: 14, Available: 3,
				Missing: []string{"a", "b"}}
		}),
	}}
	rep := p.Run(context.Background(), day())
	if rep.Status == StatusComplete {
		t.Fatalf("a run with a partial source reported COMPLETE")
	}
	s, _ := rep.Source("empty")
	if s.Available >= s.Expected {
		t.Fatalf("the partial source claims %d of %d", s.Available, s.Expected)
	}
	if len(s.Missing) == 0 {
		t.Fatalf("a partial source names nothing missing")
	}
}

// ── no session ────────────────────────────────────────────────────────────────────────

// A weekend acquires nothing and says so ONCE, as a successful no-op — not as a wall of
// failures nobody can act on. Every source is still listed so the report has the same shape
// every day.
func TestNonTradingDayIsASafeNoOp(t *testing.T) {
	var calls int
	p := &Pipeline{Steps: []Step{
		step("price", &calls, func() SourceReport { return ok(1) }),
		step("valuation", &calls, func() SourceReport { return ok(1) }),
	}}
	d := day()
	d.Session = SessionNone

	rep := p.Run(context.Background(), d)
	if calls != 0 {
		t.Fatalf("%d steps ran on a non-trading day", calls)
	}
	if rep.Status != StatusNoSession {
		t.Fatalf("status = %s, want NO_SESSION", rep.Status)
	}
	if len(rep.Sources) != 2 {
		t.Fatalf("sources = %d, want every source still listed", len(rep.Sources))
	}
	for _, s := range rep.Sources {
		if s.Status != StatusNoSession || s.Reason == "" {
			t.Fatalf("%s = %+v", s.Name, s)
		}
	}
}

// ── the report itself ─────────────────────────────────────────────────────────────────

// The report has to be machine-readable, because "exit 0" is not evidence that a day is
// complete — that is precisely the assumption this replaces.
func TestReportIsMachineReadable(t *testing.T) {
	var n int
	p := &Pipeline{Steps: []Step{
		step("price", &n, func() SourceReport { return ok(1) }),
		step("institution", &n, func() SourceReport {
			return SourceReport{Status: StatusPartial, Expected: 14, Available: 12,
				Missing: []string{"8069.TWO", "3055.TW"}, Reason: "來源尚未公布"}
		}),
	}}
	rep := p.Run(context.Background(), day())

	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("the report does not round-trip: %v", err)
	}
	if back.Date != "2026-09-08" || back.Status != StatusPartial {
		t.Fatalf("round-tripped as %+v", back)
	}
	inst, ok := back.Source("institution")
	if !ok {
		t.Fatal("the institution source did not survive")
	}
	if inst.Expected != 14 || inst.Available != 12 || len(inst.Missing) != 2 || inst.Reason == "" {
		t.Fatalf("counts or missing symbols lost: %+v", inst)
	}
	for _, want := range []string{`"market_session"`, `"expected"`, `"available"`,
		`"missing"`, `"elapsed_ms"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the JSON has no %s field: %s", want, b)
		}
	}
}

// Timing is recorded, so a source that is merely slow is visible before it times out.
func TestElapsedIsRecorded(t *testing.T) {
	p := &Pipeline{Steps: []Step{{Name: "slow", Run: func(context.Context, Day) SourceReport {
		time.Sleep(15 * time.Millisecond)
		return ok(1)
	}}}}
	rep := p.Run(context.Background(), day())
	s, _ := rep.Source("slow")
	if s.ElapsedMs < 10 {
		t.Fatalf("elapsed = %dms, want the real duration", s.ElapsedMs)
	}
}

// A cancelled context stops the remaining sources and says so, rather than reporting them as
// having produced nothing.
func TestCancellationIsReportedNotSilentlyEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var n int
	p := &Pipeline{Steps: []Step{step("price", &n, func() SourceReport { return ok(1) })}}
	rep := p.Run(ctx, day())
	if n != 0 {
		t.Fatal("a step ran on a cancelled context")
	}
	s, _ := rep.Source("price")
	if s.Status != StatusFailed || !strings.Contains(s.Reason, "中止") {
		t.Fatalf("cancellation reported as %+v", s)
	}
}

func TestMissingSymbols(t *testing.T) {
	want := []string{"2330.TW", "6488.TWO", "8069.TWO"}
	have := map[string]bool{"2330.TW": true}
	got := MissingSymbols(want, have)
	if len(got) != 2 || got[0] != "6488.TWO" || got[1] != "8069.TWO" {
		t.Fatalf("missing = %v", got)
	}
}

// A run that acquired nothing must never call itself COMPLETE.
//
// The arithmetic used to reach COMPLETE from nothing but SKIPPED, or SKIPPED plus
// NO_SESSION — the one verdict that must be unreachable without a source having produced
// something.
func TestNothingAcquiredIsNeverComplete(t *testing.T) {
	mk := func(ss ...Status) []SourceReport {
		var o []SourceReport
		for _, s := range ss {
			o = append(o, SourceReport{Status: s})
		}
		return o
	}
	for _, tc := range []struct {
		name string
		in   []Status
		want Status
	}{
		{"all skipped", []Status{StatusSkipped, StatusSkipped}, StatusPartial},
		{"skipped and no session", []Status{StatusSkipped, StatusNoSession}, StatusPartial},
		{"skipped and failed", []Status{StatusSkipped, StatusFailed}, StatusFailed},
		{"no session and failed", []Status{StatusNoSession, StatusFailed}, StatusFailed},
		// One real acquisition alongside skips is genuinely complete.
		{"one complete plus skips", []Status{StatusComplete, StatusSkipped}, StatusComplete},
		// PARTIAL means a source DID acquire something, so the run is worth rerunning
		// rather than declared a total failure.
		{"partial and failed", []Status{StatusPartial, StatusFailed}, StatusPartial},
		{"partial alone", []Status{StatusPartial}, StatusPartial},
		{"partial and skipped", []Status{StatusPartial, StatusSkipped}, StatusPartial},
	} {
		if got := overall(mk(tc.in...)); got != tc.want {
			t.Errorf("%s: %v → %s, want %s", tc.name, tc.in, got, tc.want)
		}
	}
}
