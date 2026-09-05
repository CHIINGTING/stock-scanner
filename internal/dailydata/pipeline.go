// Package dailydata runs the once-a-day acquisition every other layer depends on.
//
// # Why it exists
//
// Two of this repo's sources are EXACT_DATE: a market snapshot and a news snapshot are named
// by the day they were observed, and there is no fallback to the day before. A day nobody ran
// anything is therefore a permanent hole — not "stale data", but a date that can never be
// answered. Valuation is worse in a quieter way: every skipped session is one fewer P/E
// sample, forever, and the distribution needs twenty before it says anything at all.
//
// So acquisition cannot be a side effect of somebody opening a dashboard. It is a scheduled
// job with its own entry point, and this package is that job.
//
// # What it is NOT allowed to do
//
//   - write the scanner's price cache (internal/fetcher writes what it fetches; an intraday
//     run sharing that directory puts a half-formed bar into the series the next scan reads)
//   - report success for a source that produced nothing
//   - write a zero where a source was unavailable
//   - grade an outcome from a session that has not closed
//
// # Rerunning is the normal case, not the exception
//
// A source can be late, throttled, or briefly down. Every step is therefore idempotent and
// the whole flow is designed to be run again: a second run fills what the first could not and
// leaves what already succeeded exactly as it was. That is why the report distinguishes
// PARTIAL from FAILED — PARTIAL means "run me again later", and an operator who cannot tell
// the two apart will either give up on recoverable days or retry unrecoverable ones forever.
package dailydata

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Status is one source's outcome, and the run's overall outcome.
type Status string

const (
	// StatusComplete — everything expected was acquired.
	StatusComplete Status = "COMPLETE"
	// StatusPartial — some of it was, and running again may finish the job.
	StatusPartial Status = "PARTIAL"
	// StatusFailed — nothing was acquired and rerunning is unlikely to help on its own.
	StatusFailed Status = "FAILED"
	// StatusNoSession — the target date is not a completed trading session. This is a
	// SUCCESSFUL no-op: a Saturday with no data is correct, and reporting it as missing
	// would fill the log with failures nobody can act on.
	StatusNoSession Status = "NO_SESSION"
	// StatusSkipped — the step was deliberately not run (disabled, or not applicable).
	StatusSkipped Status = "SKIPPED"
)

// Terminal reports whether a status means "do not bother running this again today".
func (s Status) Terminal() bool { return s == StatusComplete || s == StatusNoSession }

// SourceReport is one step's result, in the shape a completeness check needs.
//
// Expected and Available are counts of RECORDS, not of requests: "13 of 14 symbols" is what
// an operator can act on, where "3 HTTP calls" is not. Both are zero for a step that has no
// natural unit, and Missing names what is absent rather than only counting it — a source that
// is missing the same two symbols every day is a different problem from one missing a random
// two.
type SourceReport struct {
	Name      string   `json:"name"`
	Status    Status   `json:"status"`
	Expected  int      `json:"expected"`
	Available int      `json:"available"`
	Missing   []string `json:"missing,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	// Note carries something worth saying that is not a failure — "already archived", say.
	Note string `json:"note,omitempty"`
	// Elapsed is how long the step took, so a slow source is visible before it times out.
	Elapsed   time.Duration `json:"-"`
	ElapsedMs int64         `json:"elapsed_ms"`
}

// Report is the whole run.
type Report struct {
	// Date is the TRADING SESSION the run targeted, which is not necessarily today.
	Date          string         `json:"date"`
	MarketSession string         `json:"market_session"`
	Status        Status         `json:"status"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	Sources       []SourceReport `json:"sources"`
}

// Source returns one step's report by name.
func (r *Report) Source(name string) (SourceReport, bool) {
	for _, s := range r.Sources {
		if s.Name == name {
			return s, true
		}
	}
	return SourceReport{}, false
}

// Market session vocabulary.
const (
	SessionClosed  = "CLOSED"      // the session finished and its data should exist
	SessionOpen    = "OPEN"        // still trading; nothing is final
	SessionNone    = "NON_TRADING" // weekend or holiday
	SessionUnknown = "UNKNOWN"
)

// Step is one acquisition, named for the report.
type Step struct {
	Name string
	// Run acquires one source. It returns a report rather than an error: a step that failed
	// is a fact about the day, not a reason to abandon the remaining sources.
	Run func(ctx context.Context, d Day) SourceReport
}

// Day is everything a step needs to know about the target session.
type Day struct {
	// Date is the trading session being acquired, YYYY-MM-DD in Taipei.
	Date string
	// Now is the run's single clock reading, so every step derives dates consistently even
	// across a midnight boundary.
	Now time.Time
	// Session says whether that date's trading has finished.
	Session string
	// Symbols is the universe being acquired, as canonical symbols.
	Symbols []string
}

// Pipeline runs the steps in order.
//
// The steps are a FIELD rather than a hard-coded sequence so a test can replace one with a
// stub that fails, which is the only way to check that a partial run reports PARTIAL and that
// a rerun fills the gap without duplicating what succeeded.
type Pipeline struct {
	Steps []Step
	Logf  func(string, ...any)
}

func (p *Pipeline) logf(format string, args ...any) {
	if p == nil || p.Logf == nil {
		return
	}
	p.Logf(format, args...)
}

// Run executes every step and returns the completeness report.
//
// It does NOT stop at the first failure. Sources are independent — a throttled exchange has
// nothing to do with whether the news feed answered — and abandoning the rest would turn one
// slow source into a whole missing day.
func (p *Pipeline) Run(ctx context.Context, d Day) *Report {
	rep := &Report{
		Date: d.Date, MarketSession: d.Session, StartedAt: d.Now,
	}

	// A day with no session acquires nothing, and that is the correct answer rather than a
	// wall of failures. The steps are still listed, so the report has the same shape every
	// day and a reader is never left wondering whether a source was checked.
	if d.Session == SessionNone {
		for _, s := range p.Steps {
			rep.Sources = append(rep.Sources, SourceReport{
				Name: s.Name, Status: StatusNoSession,
				Reason: "非交易日，無資料可取",
			})
		}
		rep.Status = StatusNoSession
		rep.FinishedAt = time.Now()
		return rep
	}

	for _, s := range p.Steps {
		if err := ctx.Err(); err != nil {
			rep.Sources = append(rep.Sources, SourceReport{
				Name: s.Name, Status: StatusFailed, Reason: "中止：" + err.Error(),
			})
			continue
		}
		start := time.Now()
		out := s.Run(ctx, d)
		out.Name = s.Name
		out.Elapsed = time.Since(start)
		out.ElapsedMs = out.Elapsed.Milliseconds()
		rep.Sources = append(rep.Sources, out)
		p.logf("  %-12s %-10s %s", s.Name, out.Status, summarise(out))
	}
	rep.Status = overall(rep.Sources)
	rep.FinishedAt = time.Now()
	return rep
}

func summarise(s SourceReport) string {
	parts := []string{}
	if s.Expected > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", s.Available, s.Expected))
	}
	if len(s.Missing) > 0 {
		show := s.Missing
		if len(show) > 4 {
			show = append(append([]string{}, show[:4]...),
				fmt.Sprintf("…+%d", len(s.Missing)-4))
		}
		parts = append(parts, "缺 "+joinWith(show, ","))
	}
	if s.Note != "" {
		parts = append(parts, s.Note)
	}
	if s.Reason != "" {
		parts = append(parts, s.Reason)
	}
	return joinWith(parts, "  ")
}

func joinWith(in []string, sep string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// overall folds the sources into one verdict.
//
// The rule is deliberately pessimistic: the run is COMPLETE only when every source that could
// have produced something did. Anything less is PARTIAL, because the operator's next action —
// run it again later — is the same either way, and a run that called itself COMPLETE while a
// source was missing would be the one thing this report exists to prevent.
func overall(ss []SourceReport) Status {
	if len(ss) == 0 {
		return StatusFailed
	}
	var complete, partial, failed, noSession, skipped int
	for _, s := range ss {
		switch s.Status {
		case StatusComplete:
			complete++
		case StatusPartial:
			partial++
		case StatusFailed:
			failed++
		case StatusNoSession:
			noSession++
		case StatusSkipped:
			skipped++
		}
	}
	switch {
	case noSession == len(ss):
		return StatusNoSession
	case complete == 0 && partial == 0:
		// Nothing was acquired at all. Without this the arithmetic below called a run of
		// nothing but SKIPPED — or SKIPPED plus NO_SESSION — COMPLETE, which is the one
		// verdict that must never be reachable without a source having produced something.
		//
		// PARTIAL counts as "something": FAILED is documented as "nothing was acquired and
		// rerunning is unlikely to help", so a run holding one partial source and one failed
		// one is PARTIAL — run me again — not FAILED.
		if failed > 0 {
			return StatusFailed
		}
		return StatusPartial
	case complete+skipped+noSession == len(ss):
		return StatusComplete
	default:
		return StatusPartial
	}
}

// MissingSymbols returns the symbols in want that are not in have, sorted.
func MissingSymbols(want []string, have map[string]bool) []string {
	var out []string
	for _, s := range want {
		if !have[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
