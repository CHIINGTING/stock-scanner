package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/valuation"
)

// The TPEx skip, counted rather than inferred.
//
// TPEx is fetched per DATE for the whole market, so the per-symbol-month resume that covers
// TWSE does nothing for it: a rerun re-requested every date even when the stocks were already
// complete. The cost was six minutes of requests per run, and the only way to hold the fix is
// to count the requests it avoids.

func withRows(dates []string, pe []*float64) *result {
	r := &result{}
	for i, d := range dates {
		var p *float64
		if i < len(pe) {
			p = pe[i]
		}
		r.rows = append(r.rows, valuation.Ratios{Date: d, PERatio: p})
	}
	return r
}

func fp(v float64) *float64 { return &v }

// Every target already holds the session → no request.
func TestSessionAlreadyHeldByAllTargetsIsSkipped(t *testing.T) {
	ts := []target{{code: "6488", market: "TWO"}, {code: "6274", market: "TWO"}}
	res := map[string]*result{
		"6488.TWO": withRows([]string{"2026-09-01", "2026-09-02"}, []*float64{fp(30), fp(31)}),
		"6274.TWO": withRows([]string{"2026-09-01", "2026-09-02"}, []*float64{fp(50), fp(51)}),
	}
	if !allHaveSession(ts, res, "2026-09-01") {
		t.Fatal("a session every target holds was not recognised as held")
	}
	if allHaveSession(ts, res, "2026-09-03") {
		t.Fatal("a session nobody holds was reported as held")
	}
}

// ONE target missing the session → the whole-market request is still needed, exactly once.
func TestOneMissingTargetForcesTheFetch(t *testing.T) {
	ts := []target{{code: "6488", market: "TWO"}, {code: "6274", market: "TWO"}}
	res := map[string]*result{
		"6488.TWO": withRows([]string{"2026-09-01"}, []*float64{fp(30)}),
		"6274.TWO": withRows(nil, nil), // has nothing
	}
	if allHaveSession(ts, res, "2026-09-01") {
		t.Fatal("a session one target is missing was treated as held")
	}
}

// ── the distinction that matters most ─────────────────────────────────────────────────

// A row with NO P/E still means the session was acquired.
//
// The exchange publishes no ratio for a loss-making company, so an archived row with an absent
// P/E is a COMPLETE answer — "we asked, there was none" — not a gap. Testing P/E availability
// instead of row presence would re-fetch those sessions on every run, forever, and would treat
// a fact about the company as missing data. 3055 蔚華科 has 225 such sessions and not one
// usable P/E: under the wrong test, every run would re-request all 225.
func TestSessionWithNoPERatioStillCountsAsAcquired(t *testing.T) {
	ts := []target{{code: "3055", market: "TWO"}}
	res := map[string]*result{
		// Every row archived, every P/E absent — the 3055 shape.
		"3055.TWO": withRows(
			[]string{"2026-09-01", "2026-09-02", "2026-09-03"},
			[]*float64{nil, nil, nil}),
	}
	for _, d := range []string{"2026-09-01", "2026-09-02", "2026-09-03"} {
		if !allHaveSession(ts, res, d) {
			t.Fatalf("session %s has an archived row with no P/E and was reported as missing "+
				"— every run would re-fetch it forever", d)
		}
	}
	if allHaveSession(ts, res, "2026-09-04") {
		t.Fatal("a genuinely absent session was reported as held")
	}
}

// hasSession is about the row, not the ratio.
func TestHasSessionIgnoresPEAvailability(t *testing.T) {
	r := withRows([]string{"2026-09-01"}, []*float64{nil})
	if !r.hasSession("2026-09-01") {
		t.Fatal("a row with no P/E was not counted as a session")
	}
	withPE := withRows([]string{"2026-09-01"}, []*float64{fp(20)})
	if !withPE.hasSession("2026-09-01") {
		t.Fatal("a row with a P/E was not counted as a session")
	}
}

// ── request counting, end to end through the loop ─────────────────────────────────────

// countingFetcher records how many whole-market requests the REAL loop issues, and answers
// with whatever the test says the exchange holds.
//
// Counting through the production function is the only way to hold the skip. The first
// version of these tests re-implemented the condition in the test helper: it proved the
// predicate and not the saving, and would have kept passing after the skip was deleted from
// the loop it exists to guard.
type countingFetcher struct {
	calls *int
	rows  map[string]map[string]valuation.Ratios // date → code → ratios
}

func (c countingFetcher) TPEXSession(_ context.Context, day time.Time) (map[string]valuation.Ratios, error) {
	*c.calls++
	return c.rows[day.Format("2006-01-02")], nil
}

// runTPEX drives the production fetchTPEX and returns the request count.
func runTPEX(t *testing.T, ts []target, res map[string]*result, days []string, refetch bool) int {
	t.Helper()
	calls := 0
	rows := map[string]map[string]valuation.Ratios{}
	for _, d := range days {
		m := map[string]valuation.Ratios{}
		for _, tt := range ts {
			m[tt.code] = valuation.Ratios{Symbol: tt.code, Date: d, PERatio: fp(20)}
		}
		rows[d] = m
	}
	first, err := time.Parse("2006-01-02", days[0])
	if err != nil {
		t.Fatal(err)
	}
	monthStart := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.Local)
	last, err := time.Parse("2006-01-02", days[len(days)-1])
	if err != nil {
		t.Fatal(err)
	}
	fetchTPEX(context.Background(), countingFetcher{calls: &calls, rows: rows}, ts,
		[]time.Time{monthStart}, last, res, refetch)
	return calls
}

// weekdays returns n consecutive weekdays starting at the given date.
func weekdays(t *testing.T, start string, n int) []string {
	t.Helper()
	d, err := time.Parse("2006-01-02", start)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for len(out) < n {
		switch d.Weekday() {
		case time.Saturday, time.Sunday:
		default:
			out = append(out, d.Format("2006-01-02"))
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// ── request counts, through the real loop ─────────────────────────────────────────────

// Every session already held → NO request at all.
func TestRequestCountWhenEverythingIsAlreadyHeld(t *testing.T) {
	ts := []target{{code: "6488", market: "TWO"}}
	days := weekdays(t, "2026-09-01", 3)
	res := map[string]*result{"6488.TWO": withRows(days, []*float64{fp(1), fp(2), fp(3)})}

	if n := runTPEX(t, ts, res, days, false); n != 0 {
		t.Fatalf("%d requests for sessions already held, want 0", n)
	}
}

// One session missing for one target → exactly ONE whole-market request.
func TestRequestCountWhenOneSessionIsMissing(t *testing.T) {
	ts := []target{{code: "6488", market: "TWO"}}
	days := weekdays(t, "2026-09-01", 3)
	held := []string{days[0], days[2]} // the middle session is absent
	res := map[string]*result{"6488.TWO": withRows(held, []*float64{fp(1), fp(3)})}

	if n := runTPEX(t, ts, res, days, false); n != 1 {
		t.Fatalf("%d requests for one missing session, want exactly 1", n)
	}
	if !res["6488.TWO"].hasSession(days[1]) {
		t.Fatal("the fetched session was not merged into the result")
	}
}

// A session archived with NO P/E must not be re-requested. 3055 蔚華科 has 225 of them, so
// the wrong test here would mean re-fetching all 225 on every single run, forever.
func TestNoPERatioSessionsAreNotRefetched(t *testing.T) {
	ts := []target{{code: "3055", market: "TWO"}}
	days := weekdays(t, "2026-09-01", 3)
	res := map[string]*result{"3055.TWO": withRows(days, []*float64{nil, nil, nil})}

	if n := runTPEX(t, ts, res, days, false); n != 0 {
		t.Fatalf("%d requests for sessions whose P/E is legitimately absent, want 0", n)
	}
}

// -refetch bypasses the optimisation entirely and re-requests the source.
func TestRefetchBypassesTheSkip(t *testing.T) {
	ts := []target{{code: "6488", market: "TWO"}}
	days := weekdays(t, "2026-09-01", 3)
	res := map[string]*result{"6488.TWO": withRows(days, []*float64{fp(1), fp(2), fp(3)})}

	if n := runTPEX(t, ts, res, days, true); n != len(days) {
		t.Fatalf("-refetch issued %d requests, want all %d", n, len(days))
	}
}

// ── -refetch, at the resume boundary ──────────────────────────────────────────────────
//
// Everything downstream skips work by consulting rows loadResume put into the results: the
// per-symbol-month res.has in fetchTWSE, the per-session allHaveSession in fetchTPEX. So
// -refetch is not implemented by any of those — it is implemented by loadResume declining to
// seed them. If that guard ever breaks, -refetch keeps its TPEx effect (fetchTPEX carries its
// own check) and silently loses its TWSE one, which is exactly the shape of a flag that looks
// like it worked.

// seedArchive writes one archived snapshot the way a previous pass would have left it.
func seedArchive(t *testing.T, dir, archiveDate string, tg target, dates []string) {
	t.Helper()
	var h []valuation.Ratios
	for _, d := range dates {
		h = append(h, valuation.Ratios{Date: d, PERatio: fp(20), Backfilled: true,
			Source: valuation.SourceTWSEHistorical})
	}
	observed, err := time.Parse("2006-01-02", archiveDate)
	if err != nil {
		t.Fatal(err)
	}
	// SaveSnapshot derives the archive directory from ObservedAt, which is what makes the
	// fixture land on the same path readExisting will look at.
	s := &valuation.Snapshot{Symbol: tg.symbol(), ObservedAt: observed, History: h}
	if _, err := valuation.SaveSnapshot(dir, s); err != nil {
		t.Fatalf("seeding the archive failed: %v", err)
	}
}

func resumeFixture(t *testing.T) (string, string, []target, map[string]*result) {
	t.Helper()
	dir, archiveDate := t.TempDir(), "2026-09-06"
	ts := []target{{code: "2330", market: "TW"}}
	seedArchive(t, dir, archiveDate, ts[0], []string{"2026-08-03", "2026-08-04", "2026-09-01"})
	return dir, archiveDate, ts, map[string]*result{"2330.TW": {target: ts[0]}}
}

// Without -refetch the archived months are loaded, and the month skip therefore fires.
func TestResumeSeedsTheArchivedMonths(t *testing.T) {
	dir, date, ts, res := resumeFixture(t)

	if n := loadResume(dir, date, ts, res, false); n != 1 {
		t.Fatalf("seeded %d targets, want 1", n)
	}
	if got := len(res["2330.TW"].rows); got != 3 {
		t.Fatalf("loaded %d rows, want 3", got)
	}
	if res["2330.TW"].resumed != 3 {
		t.Fatalf("resumed count = %d, want 3", res["2330.TW"].resumed)
	}
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if !res["2330.TW"].has(aug) {
		t.Fatal("August was archived but the month skip would not fire — resume did nothing")
	}
	if !res["2330.TW"].hasSession("2026-09-01") {
		t.Fatal("an archived session was not seen, so the TPEx skip would not fire either")
	}
}

// With -refetch nothing is seeded, so every downstream skip decides against an empty result
// and the work is genuinely redone.
func TestRefetchSeedsNothingSoEveryMonthIsRefetched(t *testing.T) {
	dir, date, ts, res := resumeFixture(t)

	if n := loadResume(dir, date, ts, res, true); n != 0 {
		t.Fatalf("-refetch seeded %d targets; it must seed none", n)
	}
	if got := len(res["2330.TW"].rows); got != 0 {
		t.Fatalf("-refetch loaded %d archived rows; the archive must not be consulted", got)
	}
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if res["2330.TW"].has(aug) {
		t.Fatal("-refetch left the month skip armed: fetchTWSE would skip August")
	}
	if allHaveSession(ts, res, "2026-09-01") {
		t.Fatal("-refetch left the session skip armed: fetchTPEX would skip 2026-09-01")
	}
}

// ── a throttled TPEx run must say it was throttled ────────────────────────────────────
//
// "no rows" and "the exchange refused us" produce the same empty result, and the report is
// the only place the difference can still be seen. A run that was rate-limited is worth
// repeating in ten minutes; a run that genuinely found nothing is not, and a reader who
// cannot tell them apart will retry the wrong one.

// failingFetcher answers every session with the same error.
type failingFetcher struct {
	calls *int
	err   error
}

func (f failingFetcher) TPEXSession(context.Context, time.Time) (map[string]valuation.Ratios, error) {
	*f.calls++
	return nil, f.err
}

func runFailingTPEX(t *testing.T, err error) *result {
	t.Helper()
	ts := []target{{code: "6488", market: "TWO"}}
	res := map[string]*result{"6488.TWO": {target: ts[0]}}
	day, perr := time.Parse("2006-01-02", "2026-09-01")
	if perr != nil {
		t.Fatal(perr)
	}
	calls := 0
	fetchTPEX(context.Background(), failingFetcher{calls: &calls, err: err}, ts,
		[]time.Time{time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)}, day, res, false)
	if calls == 0 {
		t.Fatal("no request was attempted, so the error path never ran")
	}
	return res["6488.TWO"]
}

func TestTPEXRateLimitIsReportedAsThrottled(t *testing.T) {
	r := runFailingTPEX(t, valuation.RateLimitedError(http.StatusTemporaryRedirect))

	if !r.limited {
		t.Fatal("a rate-limited TPEx run was not marked throttled; the report would tell the " +
			"reader to give up rather than to come back later")
	}
	if len(r.errs) == 0 {
		t.Fatal("the failure left no error on the result")
	}
}

// The converse: an ordinary failure must NOT claim a rate limit, or "come back later" becomes
// the advice for every broken run.
func TestOrdinaryTPEXFailureIsNotReportedAsThrottled(t *testing.T) {
	r := runFailingTPEX(t, errors.New("unexpected end of JSON input"))

	if r.limited {
		t.Fatal("a parse failure was reported as a rate limit")
	}
	if len(r.errs) == 0 {
		t.Fatal("the failure left no error on the result")
	}
}
