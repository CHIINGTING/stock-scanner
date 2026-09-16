package history_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/derivatives/history"
	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// Blocking Gate 4: the point-in-time history reader.
//
// Everything in this file is one property in different costumes — EACH TRADING DATE RESOLVES TO
// THE REVISION VISIBLE AT THE CUTOFF (§7.3) — and the fixture is the case that separates it from
// every plausible shortcut:
//
//	09-01 revision 0, fetched 09-01, POSITION 1,000
//	09-04 revision 0, fetched 09-04, POSITION 1,500
//	09-01 revision 1, fetched 09-05, POSITION 1,200   ← a correction to an EARLIER date,
//	                                                    retrieved LATER
//
// asOf 09-04 must not see the correction; asOf 09-06 must; and the latest trading date is 09-04
// in BOTH cases. A reader that took each date's newest revision would answer 1,200 for a reading
// dated 09-04, and one that ordered on fetched_at would call 09-01 the current session.

const (
	source  = provider.SourceInstitutionalFutures
	dataset = "institutional_futures"
	session = institutional.SessionCombined
)

func ctx() context.Context { return context.Background() }

func openR15(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(store.Config{
		Path:   filepath.Join(t.TempDir(), "r15.db"),
		Schema: derivatives.Schema,
	})
	if err != nil {
		t.Fatalf("open r15 store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// at builds a UTC instant from a Taipei wall clock. Every boundary in §7.3 is only meaningful
// in Taipei, and the column stores UTC — so the tests express the intent and let the code do
// the conversion.
func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, dailydata.Taipei)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

func fptr(v float64) *float64 { return &v }

// reading is one institution's two net figures for one date.
type reading struct {
	institution      string
	flow, position   float64
	hasFlow, hasPos  bool
	skipFlowPosition bool
}

func rd(institution string, flow, position float64) reading {
	return reading{institution: institution, flow: flow, position: position,
		hasFlow: true, hasPos: true}
}

// rowsFor expands readings into the six rows per (product, institution) that M3 stores.
//
// LONG and SHORT are deliberately inconsistent with NET, so any implementation that derives NET
// from long − short produces a different number and fails. An MTX row is present for every
// institution too: the scope selection has to have something real to exclude, and 1 TX lot is
// not 1 MTX lot.
func rowsFor(date string, rs []reading) []provider.InstitutionalRow {
	var out []provider.InstitutionalRow
	add := func(product, institution, semantics, side string, lots *float64) {
		out = append(out, provider.InstitutionalRow{
			TradingDate: date, Product: product, Institution: institution,
			Semantics: semantics, Side: side, Lots: lots,
		})
	}
	for _, r := range rs {
		var flow, pos *float64
		if r.hasFlow {
			flow = fptr(r.flow)
		}
		if r.hasPos {
			pos = fptr(r.position)
		}
		add(institutional.ProductTX, r.institution, provider.SemanticsFlow, provider.SideLong, fptr(11111))
		add(institutional.ProductTX, r.institution, provider.SemanticsFlow, provider.SideShort, fptr(22222))
		add(institutional.ProductTX, r.institution, provider.SemanticsFlow, provider.SideNet, flow)
		add(institutional.ProductTX, r.institution, provider.SemanticsPosition, provider.SideLong, fptr(33333))
		add(institutional.ProductTX, r.institution, provider.SemanticsPosition, provider.SideShort, fptr(44444))
		add(institutional.ProductTX, r.institution, provider.SemanticsPosition, provider.SideNet, pos)
		// A different instrument in the same response.
		add(institutional.ProductMTX, r.institution, provider.SemanticsFlow, provider.SideNet, fptr(-777))
		add(institutional.ProductMTX, r.institution, provider.SemanticsPosition, provider.SideNet, fptr(-888))
	}
	return out
}

// putDay writes one revision of one session, with its rows.
func putDay(t *testing.T, st *store.Store, date string, fetched time.Time, rs []reading) derivatives.Snapshot {
	t.Helper()
	payload := fmt.Sprintf("%s|%v", date, rs)
	snap, err := derivatives.PutSnapshot(ctx(), st, derivatives.Snapshot{
		Source: source, TradingDate: date, TradingSession: session,
		DateSource: derivatives.DateObserved, Payload: payload, FetchedAt: fetched,
	})
	if err != nil {
		t.Fatalf("PutSnapshot(%s @ %s): %v", date, fetched, err)
	}
	if _, err := derivatives.PutInstitutional(ctx(), st, snap.ID, source, fetched,
		rowsFor(date, rs)); err != nil {
		t.Fatalf("PutInstitutional(%s): %v", date, err)
	}
	return snap
}

func putHealth(t *testing.T, st *store.Store, date string, status derivatives.Status,
	recorded time.Time, reason string) {
	t.Helper()
	if err := derivatives.PutDataHealth(ctx(), st, derivatives.DataHealth{
		Dataset: dataset, TradingDate: date, Session: session, Source: source,
		Status: status, AsOf: date, Reason: reason, RecordedAt: recorded,
	}); err != nil {
		t.Fatalf("PutDataHealth(%s, %s): %v", date, status, err)
	}
}

func threeInstitutions(flow, position float64) []reading {
	return []reading{
		rd(institutional.InstitutionDealer, flow, position),
		rd(institutional.InstitutionTrust, flow*2, position*2),
		rd(institutional.InstitutionForeign, flow*3, position*3),
	}
}

// theCorrectionFixture is the three-revision case, plus the two days between them that the
// exchange did not publish and the fetch could not read.
func theCorrectionFixture(t *testing.T) *store.Store {
	t.Helper()
	st := openR15(t)

	putDay(t, st, "2026-09-01", at(t, "2026-09-01 15:30:00"), threeInstitutions(10, 1000))
	putHealth(t, st, "2026-09-01", derivatives.StatusAvailable, at(t, "2026-09-01 15:30:00"), "")

	putHealth(t, st, "2026-09-02", derivatives.StatusNotPublished,
		at(t, "2026-09-02 15:10:00"), "the exchange has not released this dataset yet")
	putHealth(t, st, "2026-09-03", derivatives.StatusError,
		at(t, "2026-09-03 15:10:00"), "the envelope did not decode")

	putDay(t, st, "2026-09-04", at(t, "2026-09-04 15:30:00"), threeInstitutions(20, 1500))
	putHealth(t, st, "2026-09-04", derivatives.StatusAvailable, at(t, "2026-09-04 15:30:00"), "")

	// The correction: a NEW revision of 09-01, retrieved on 09-05.
	putDay(t, st, "2026-09-01", at(t, "2026-09-05 09:00:00"), threeInstitutions(10, 1200))
	return st
}

func query(asOf string, count int) history.Query {
	return history.Query{
		Dataset:      dataset,
		Source:       source,
		Session:      session,
		Scope:        institutional.ScopeTX,
		Institutions: institutional.RequiredInstitutions,
		AsOf:         asOf,
		Count:        count,
	}
}

func mustLoad(t *testing.T, st *store.Store, q history.Query) history.Result {
	t.Helper()
	res, err := history.Load(ctx(), st, q)
	if err != nil {
		t.Fatalf("Load(asOf=%s): %v", q.AsOf, err)
	}
	return res
}

func dealerSeries(t *testing.T, res history.Result) history.Series {
	t.Helper()
	s, ok := res.SeriesFor(institutional.InstitutionDealer)
	if !ok {
		t.Fatalf("no series for %s", institutional.InstitutionDealer)
	}
	return s
}

func positionOn(t *testing.T, s history.Series, date string) (float64, bool) {
	t.Helper()
	for _, o := range s.Observations {
		if o.TradingDate == date {
			return o.Position().Lots()
		}
	}
	t.Fatalf("no observation for %s in %v", date, s.Observations)
	return 0, false
}

// ── Blocking Gate 4 ───────────────────────────────────────────────────────────────────

// TestEachDateResolvesToTheRevisionVisibleAtTheCutoff is the required case, in full.
func TestEachDateResolvesToTheRevisionVisibleAtTheCutoff(t *testing.T) {
	st := theCorrectionFixture(t)

	t.Run("as of 09-04 the correction is invisible", func(t *testing.T) {
		res := mustLoad(t, st, query("2026-09-04", 10))
		s := dealerSeries(t, res)

		got, ok := positionOn(t, s, "2026-09-01")
		if !ok {
			t.Fatal("09-01 has no POSITION")
		}
		if got != 1000 {
			t.Errorf("09-01 POSITION as of 09-04 = %v, want 1000 — revision 1 was fetched on "+
				"09-05 and a reading dated 09-04 could not have had it", got)
		}
		r, ok := res.Resolution("2026-09-01")
		if !ok {
			t.Fatal("no resolution for 09-01")
		}
		if r.Revision != 0 {
			t.Errorf("09-01 resolved to revision %d as of 09-04, want 0", r.Revision)
		}
		if r.FetchedAt.After(res.Cutoff) {
			t.Errorf("09-01 resolved to a revision fetched at %s, after the cutoff %s",
				r.FetchedAt, res.Cutoff)
		}
	})

	t.Run("as of 09-06 the correction is visible", func(t *testing.T) {
		res := mustLoad(t, st, query("2026-09-06", 10))
		s := dealerSeries(t, res)

		got, ok := positionOn(t, s, "2026-09-01")
		if !ok {
			t.Fatal("09-01 has no POSITION")
		}
		if got != 1200 {
			t.Errorf("09-01 POSITION as of 09-06 = %v, want 1200 — the correction was fetched "+
				"on 09-05 and IS visible to a reading dated 09-06", got)
		}
		r, _ := res.Resolution("2026-09-01")
		if r.Revision != 1 {
			t.Errorf("09-01 resolved to revision %d as of 09-06, want 1", r.Revision)
		}
	})

	t.Run("the latest trading date is 09-04 under both cutoffs", func(t *testing.T) {
		for _, asOf := range []string{"2026-09-04", "2026-09-06"} {
			res := mustLoad(t, st, query(asOf, 10))
			if got := res.LatestTradingDate(); got != "2026-09-04" {
				t.Errorf("as of %s the latest trading date is %s, want 2026-09-04 — a "+
					"correction to an older session does not make that session current",
					asOf, got)
			}
			s := dealerSeries(t, res)
			latest, ok := s.Latest()
			if !ok {
				t.Fatalf("as of %s the series has no usable observation", asOf)
			}
			if latest.TradingDate != "2026-09-04" {
				t.Errorf("as of %s Series.Latest() is %s, want 2026-09-04", asOf, latest.TradingDate)
			}
			if res.Dates[0].TradingDate != "2026-09-04" {
				t.Errorf("as of %s the first resolution is %s; the walk must be ordered by "+
					"trading date descending", asOf, res.Dates[0].TradingDate)
			}
		}
	})
}

// TestACorrectionDoesNotPromoteAnOlderDateToCurrent is the same property stated as the failure
// it prevents, and it is the one mutation (d) targets.
//
// On 09-06 the newest FETCH in the table belongs to 09-01. Ordering the walk on fetched_at would
// therefore make 09-01 the current session, and every number in the view would be three sessions
// old under a current label.
func TestACorrectionDoesNotPromoteAnOlderDateToCurrent(t *testing.T) {
	st := theCorrectionFixture(t)
	res := mustLoad(t, st, query("2026-09-06", 10))

	if len(res.Dates) < 2 {
		t.Fatalf("expected at least two dates, got %+v", res.Dates)
	}
	// The most recently FETCHED row in the whole table is 09-01 revision 1.
	newestFetch := res.Dates[0]
	for _, d := range res.Dates {
		if d.FetchedAt.After(newestFetch.FetchedAt) {
			newestFetch = d
		}
	}
	if newestFetch.TradingDate != "2026-09-01" {
		t.Fatalf("the fixture is wrong: the newest fetch belongs to %s, not 09-01",
			newestFetch.TradingDate)
	}
	if res.Dates[0].TradingDate == newestFetch.TradingDate {
		t.Fatal("the walk put the most recently FETCHED date first; it must be ordered by " +
			"trading date, or a correction to an older session becomes the current one")
	}

	prev := ""
	for _, d := range res.Dates {
		if prev != "" && d.TradingDate >= prev {
			t.Fatalf("dates came back out of order: %s after %s", d.TradingDate, prev)
		}
		prev = d.TradingDate
	}
}

// TestHistoryNeverTakesTheNewestRevisionUnconditionally is mutation (a)'s target.
//
// Every date in a result must carry the revision the cutoff allowed, checked against the
// revision that actually exists in the table. 09-01 has two.
func TestHistoryNeverTakesTheNewestRevisionUnconditionally(t *testing.T) {
	st := theCorrectionFixture(t)

	// The table's newest revision of 09-01 is 1; the cutoff-visible one on 09-04 is 0.
	newest, err := derivatives.SnapshotForSession(ctx(), st, source, session, "2026-09-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if newest.RevisionNo != 1 {
		t.Fatalf("the fixture is wrong: 09-01's newest revision is %d", newest.RevisionNo)
	}

	res := mustLoad(t, st, query("2026-09-04", 10))
	for _, d := range res.Dates {
		if d.FetchedAt.IsZero() {
			continue
		}
		if d.FetchedAt.After(res.Cutoff) {
			t.Errorf("%s resolved to a revision fetched at %s, past the cutoff %s",
				d.TradingDate, d.FetchedAt, res.Cutoff)
		}
		if d.SnapshotID == newest.ID {
			t.Errorf("%s resolved to snapshot %d, which is 09-01 revision 1 — fetched on "+
				"09-05 and invisible to a reading dated 09-04", d.TradingDate, d.SnapshotID)
		}
	}
}

// TestSkippedDaysAreRecordedNotDropped: §10.4's "skipping is not silent".
//
// 09-02 was not published and 09-03 failed to decode. Neither is a valid observation, both are
// in the series so the baseline resolver can name them, and the CHANGE that steps over them
// says it stepped over three calendar days.
func TestSkippedDaysAreRecordedNotDropped(t *testing.T) {
	st := theCorrectionFixture(t)
	res := mustLoad(t, st, query("2026-09-04", 10))
	s := dealerSeries(t, res)

	if res.ValidObservations != 2 {
		t.Errorf("valid observations = %d, want 2 (09-01 and 09-04)", res.ValidObservations)
	}
	want := map[string]institutional.MetricStatus{
		"2026-09-02": institutional.StatusNotPublished,
		"2026-09-03": institutional.StatusError,
	}
	got := map[string]institutional.MetricStatus{}
	for _, g := range s.Gaps {
		got[g.TradingDate] = g.Status
		if g.Reason == "" {
			t.Errorf("the gap on %s has no reason", g.TradingDate)
		}
	}
	for date, status := range want {
		if got[date] != status {
			t.Errorf("gap on %s = %q, want %s", date, got[date], status)
		}
	}
	// NOT_PUBLISHED and ERROR are different facts and imply opposite actions (§6). A reader
	// that collapsed them would make a normal afternoon look broken.
	if got["2026-09-02"] == got["2026-09-03"] {
		t.Error("NOT_PUBLISHED and ERROR came back as the same status")
	}

	// The observations for those days exist, carry no number, and never render as 0.
	for date := range want {
		for _, o := range s.Observations {
			if o.TradingDate != date {
				continue
			}
			if _, ok := o.Position().Lots(); ok {
				t.Errorf("%s carries a POSITION on a day nothing was observed", date)
			}
			if d := o.Position().Display; d == "0" || d == "0 lots" {
				t.Errorf("%s POSITION renders as %q", date, d)
			}
		}
	}

	// And the CHANGE built from this series steps over them, visibly.
	latest, _ := s.Latest()
	hc, err := institutional.ChangeOver(latest, s.Before(latest.TradingDate), 1,
		institutional.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if hc.PreviousTradingDate != "2026-09-01" {
		t.Errorf("CHANGE baseline = %s, want 2026-09-01", hc.PreviousTradingDate)
	}
	if hc.CalendarGapDays != 3 {
		t.Errorf("calendar gap = %d, want 3 — the pair (1 observation, 3 days) is the fact "+
			"§10.4 exists to keep visible", hc.CalendarGapDays)
	}
	if len(hc.Resolution.SkippedDates) != 2 {
		t.Errorf("the resolution skipped %d dates, want 2: %+v",
			len(hc.Resolution.SkippedDates), hc.Resolution.SkippedDates)
	}
}

// TestAHistoricalChangeRecomputedAtACutoffReproducesThatDaysAnswer.
//
// This is the property a backtest rests on. The CHANGE computed on 09-04, from data available on
// 09-04, is +500. Recomputing it two days later AT THE SAME CUTOFF must still be +500 — even
// though a correction has since landed. Recomputing it at the LATER cutoff is a different, also
// correct, answer: +300. A reader that could not tell those two apart would have no way to know
// whether a strategy was evaluated on data it could have had.
func TestAHistoricalChangeRecomputedAtACutoffReproducesThatDaysAnswer(t *testing.T) {
	st := theCorrectionFixture(t)
	p := institutional.DefaultPolicy()

	changeAsOf := func(asOf string) institutional.HorizonChange {
		t.Helper()
		res := mustLoad(t, st, query(asOf, 10))
		s := dealerSeries(t, res)
		var cur institutional.Observation
		for _, o := range s.Observations {
			if o.TradingDate == "2026-09-04" {
				cur = o
			}
		}
		if cur.TradingDate == "" {
			t.Fatalf("as of %s there is no 09-04 observation", asOf)
		}
		hc, err := institutional.ChangeOver(cur, s.Before("2026-09-04"), 1, p)
		if err != nil {
			t.Fatal(err)
		}
		return hc
	}

	contemporaneous := changeAsOf("2026-09-04")
	v, ok := contemporaneous.Change.Lots()
	if !ok || v != 500 {
		t.Fatalf("CHANGE on 09-04 as of 09-04 = %v (ok=%v), want 500 (1,500 − 1,000)", v, ok)
	}

	// Recomputed at the same cutoff, after the correction was archived.
	replay := changeAsOf("2026-09-04")
	rv, ok := replay.Change.Lots()
	if !ok || rv != v {
		t.Errorf("recomputed at the 09-04 cutoff = %v (ok=%v), want %v — a later revision "+
			"reached a past evaluation", rv, ok, v)
	}
	if replay.Resolution.PreviousTradingDate != contemporaneous.Resolution.PreviousTradingDate {
		t.Errorf("the baseline moved: %s then %s",
			contemporaneous.Resolution.PreviousTradingDate, replay.Resolution.PreviousTradingDate)
	}

	// And the corrected view is a DIFFERENT number, which is the point of keeping both.
	corrected := changeAsOf("2026-09-06")
	cv, ok := corrected.Change.Lots()
	if !ok || cv != 300 {
		t.Fatalf("CHANGE on 09-04 as of 09-06 = %v (ok=%v), want 300 (1,500 − 1,200)", cv, ok)
	}
	if cv == v {
		t.Error("the corrected and the contemporaneous answers are identical; the fixture's " +
			"correction changed nothing and the test proves nothing")
	}
}

// ── the percentile reference set ──────────────────────────────────────────────────────

// longFixture writes `n` consecutive AVAILABLE sessions whose POSITION rises by 100 a day, and
// returns the store. Day i (1-based) is 2026-06-i with POSITION i*100.
func longFixture(t *testing.T, n int) *store.Store {
	t.Helper()
	st := openR15(t)
	for i := 1; i <= n; i++ {
		date := fmt.Sprintf("2026-06-%02d", i)
		fetched := at(t, date+" 15:30:00")
		putDay(t, st, date, fetched, threeInstitutions(float64(-i*100), float64(i*100)))
		putHealth(t, st, date, derivatives.StatusAvailable, fetched, "")
	}
	return st
}

// TestPercentileReferenceExcludesObservationsAfterTheAsOf is mutation (b)'s target.
//
// §10.4: "the distribution for a reading dated A contains only observations at or before A".
// The store holds 20 sessions; a read as of day 10 must rank day 10 at the TOP of its own
// distribution, because days 11–20 had not happened.
func TestPercentileReferenceExcludesObservationsAfterTheAsOf(t *testing.T) {
	st := longFixture(t, 20)
	res := mustLoad(t, st, query("2026-06-10", 60))
	s := dealerSeries(t, res)
	cur, ok := s.Latest()
	if !ok {
		t.Fatal("no usable observation")
	}
	if cur.TradingDate != "2026-06-10" {
		t.Fatalf("latest = %s, want 2026-06-10", cur.TradingDate)
	}

	set, err := institutional.Percentiles(cur, s.Before(cur.TradingDate),
		institutional.Policy{MinPercentileSample: 5})
	if err != nil {
		t.Fatal(err)
	}
	dist, _ := set.Distribution(institutional.SemanticsPosition)
	if len(dist.Values) != 10 {
		t.Fatalf("the reference distribution holds %d values as of day 10; days 11–20 had "+
			"not happened. Values: %v", len(dist.Values), dist.Values)
	}
	for _, v := range dist.Values {
		if v > 1000 {
			t.Errorf("a POSITION of %v is in the distribution; day 10 holds 1,000 and "+
				"everything larger is a future session", v)
		}
	}
	for _, d := range dist.Dates {
		if d > "2026-06-10" {
			t.Errorf("%s is in the reference set for a reading dated 2026-06-10", d)
		}
	}
	if r, ok := set.Position.Value(); !ok || r != 1 {
		t.Errorf("POSITION percentile = %v (ok=%v), want 1: day 10 is the largest reading that "+
			"had happened by day 10", r, ok)
	}
}

// TestPercentileReferenceExcludesRevisionsNotVisibleAtTheCutoff is mutation (c)'s target.
//
// The as-of bound has TWO halves and they are not the same bound (§7.3): one says the session
// had happened, the other says we had already fetched it. This fixture corrects day 3 upwards
// on day 20 — a value that would be the LARGEST in the distribution if the revision leaked in —
// and reads as of day 10.
func TestPercentileReferenceExcludesRevisionsNotVisibleAtTheCutoff(t *testing.T) {
	st := longFixture(t, 20)
	// A correction to day 3, retrieved on day 20: POSITION 99,000 instead of 300.
	putDay(t, st, "2026-06-03", at(t, "2026-06-20 09:00:00"), threeInstitutions(-99000, 99000))

	res := mustLoad(t, st, query("2026-06-10", 60))
	s := dealerSeries(t, res)
	cur, _ := s.Latest()

	set, err := institutional.Percentiles(cur, s.Before(cur.TradingDate),
		institutional.Policy{MinPercentileSample: 5})
	if err != nil {
		t.Fatal(err)
	}
	dist, _ := set.Distribution(institutional.SemanticsPosition)
	for _, v := range dist.Values {
		if v == 99000 {
			t.Fatalf("the day-3 correction, retrieved on day 20, reached the distribution for "+
				"a reading dated day 10. Values: %v", dist.Values)
		}
	}
	if got, ok := positionOn(t, s, "2026-06-03"); !ok || got != 300 {
		t.Errorf("day 3 POSITION as of day 10 = %v (ok=%v), want 300", got, ok)
	}
	if r, ok := set.Position.Value(); !ok || r != 1 {
		t.Errorf("POSITION percentile = %v (ok=%v), want 1; a leaked 99,000 would push day "+
			"10 down to 0.9", r, ok)
	}

	// And as of day 20 the same query DOES see it, so the exclusion above is a bound rather
	// than a fixture that never had the row.
	late := mustLoad(t, st, query("2026-06-20", 60))
	ls := dealerSeries(t, late)
	if got, ok := positionOn(t, ls, "2026-06-03"); !ok || got != 99000 {
		t.Errorf("day 3 POSITION as of day 20 = %v (ok=%v), want 99000 — the correction is "+
			"visible to a later reading", got, ok)
	}
}

// TestInsufficientSampleFromTheStore: the minimum applies to what the STORE holds, not to what
// a caller hoped it held.
func TestInsufficientSampleFromTheStore(t *testing.T) {
	st := longFixture(t, 6)
	res := mustLoad(t, st, query("2026-06-06", 60))
	s := dealerSeries(t, res)
	cur, _ := s.Latest()

	set, err := institutional.Percentiles(cur, s.Before(cur.TradingDate),
		institutional.Policy{MinPercentileSample: 20})
	if err != nil {
		t.Fatal(err)
	}
	if set.Position.Status != institutional.PercentileInsufficientSample {
		t.Errorf("status = %s, want INSUFFICIENT_SAMPLE", set.Position.Status)
	}
	if set.Position.Ratio != nil {
		t.Errorf("a percentile (%v) was published from %d observations",
			*set.Position.Ratio, set.Position.SampleSize)
	}
	if set.Position.SampleSize != 6 || set.Position.RequiredSampleSize != 20 {
		t.Errorf("sample = %d/%d, want 6/20", set.Position.SampleSize, set.Position.RequiredSampleSize)
	}
}

// ── health, visibility and the walk ───────────────────────────────────────────────────

// TestHealthRecordedAfterTheCutoffIsNotVisible.
//
// derivative_data_health is replace-on-write: it is the CURRENT answer to "what happened when
// we last tried". So a record rewritten after the cutoff is not evidence about the cutoff, and
// reading it unbounded would let a status established later decide what an earlier reading saw.
func TestHealthRecordedAfterTheCutoffIsNotVisible(t *testing.T) {
	st := openR15(t)
	putDay(t, st, "2026-09-01", at(t, "2026-09-01 15:30:00"), threeInstitutions(10, 1000))
	// The only health record for 09-01 was written on 09-05 — a backfill's rewrite.
	putHealth(t, st, "2026-09-01", derivatives.StatusPartial, at(t, "2026-09-05 09:00:00"),
		"two rows were rejected")

	early := mustLoad(t, st, query("2026-09-02", 5))
	r, ok := early.Resolution("2026-09-01")
	if !ok {
		t.Fatal("09-01 is not in the result")
	}
	if r.StatusSource != history.StatusFromSnapshot {
		t.Errorf("status source = %q, want %q: the only health record was written after the "+
			"cutoff", r.StatusSource, history.StatusFromSnapshot)
	}
	if r.Status != institutional.StatusAvailable {
		t.Errorf("status = %s, want AVAILABLE", r.Status)
	}

	late := mustLoad(t, st, query("2026-09-06", 5))
	lr, _ := late.Resolution("2026-09-01")
	if lr.StatusSource != history.StatusFromHealth {
		t.Errorf("as of 09-06 the status source is %q, want %q", lr.StatusSource,
			history.StatusFromHealth)
	}
	if lr.Status != institutional.StatusPartial {
		t.Errorf("as of 09-06 the status is %s, want PARTIAL", lr.Status)
	}
}

// TestAStaleHealthRecordIsNeverLaunderedIntoAnObservation.
//
// §10.4 rules STALE out and says so is not configurable. A snapshot may still be sitting there
// — the row was stored before the freshness contract failed — and preferring the rows whenever
// rows exist would launder the failure into every CHANGE computed from it. The health record
// wins, the rows are left unread, and the disagreement is recorded rather than dropped.
func TestAStaleHealthRecordIsNeverLaunderedIntoAnObservation(t *testing.T) {
	st := openR15(t)
	putDay(t, st, "2026-09-01", at(t, "2026-09-01 15:30:00"), threeInstitutions(10, 1000))
	putHealth(t, st, "2026-09-01", derivatives.StatusStale, at(t, "2026-09-01 16:00:00"),
		"older than this dataset expects")

	res := mustLoad(t, st, query("2026-09-02", 5))
	r, _ := res.Resolution("2026-09-01")
	if r.Status != institutional.StatusStale {
		t.Errorf("status = %s, want STALE", r.Status)
	}
	if r.Rows != 0 {
		t.Errorf("%d rows were read from a STALE day", r.Rows)
	}
	s := dealerSeries(t, res)
	if _, ok := positionOn(t, s, "2026-09-01"); ok {
		t.Error("a STALE day produced a POSITION; it would then be usable as a baseline")
	}
	if len(res.Inconsistencies) != 1 {
		t.Fatalf("the health/snapshot disagreement was not recorded: %+v", res.Inconsistencies)
	}
	if res.Inconsistencies[0].SnapshotID == 0 {
		t.Error("the inconsistency does not name the snapshot it left unread")
	}
}

// TestCountIsInValidObservations: N counts VALID trading dates, and the unusable ones in
// between come back as well without consuming a slot.
func TestCountIsInValidObservations(t *testing.T) {
	st := theCorrectionFixture(t)
	res := mustLoad(t, st, query("2026-09-04", 1))
	if res.ValidObservations != 1 {
		t.Errorf("valid observations = %d, want 1", res.ValidObservations)
	}
	if got := res.LatestTradingDate(); got != "2026-09-04" {
		t.Errorf("latest = %s, want 2026-09-04", got)
	}
	// The walk stopped at the first valid date, so 09-01 is not in the result at all.
	if _, ok := res.Resolution("2026-09-01"); ok {
		t.Error("a second valid observation came back for Count=1")
	}

	two := mustLoad(t, st, query("2026-09-04", 2))
	if two.ValidObservations != 2 {
		t.Errorf("valid observations = %d, want 2", two.ValidObservations)
	}
	if len(two.Dates) != 4 {
		t.Errorf("the walk covered %d dates, want 4 — the two unpublished/failed days do not "+
			"consume a slot but are still reported", len(two.Dates))
	}
}

// TestScanBoundIsReportedNotHidden: a bounded scan and a short history are different facts.
func TestScanBoundIsReportedNotHidden(t *testing.T) {
	st := longFixture(t, 20)
	res := mustLoad(t, st, history.Query{
		Dataset: dataset, Source: source, Session: session, Scope: institutional.ScopeTX,
		Institutions: []string{institutional.InstitutionDealer},
		AsOf:         "2026-06-20", Count: 15, MaxScanDates: 5,
	})
	if !res.Truncated {
		t.Fatal("the walk hit its scan bound and did not say so")
	}
	if res.TruncatedReason == "" {
		t.Error("Truncated with no reason")
	}
	if res.ValidObservations != 5 {
		t.Errorf("valid observations = %d, want 5", res.ValidObservations)
	}
}

// ── the assembled view ────────────────────────────────────────────────────────────────

// TestLoadViewAssemblesTheThreeInstitutionsAndTheTotal.
func TestLoadViewAssemblesTheThreeInstitutionsAndTheTotal(t *testing.T) {
	st := theCorrectionFixture(t)
	view, res, err := history.LoadView(ctx(), st, query("2026-09-04", 10),
		institutional.Policy{MinPercentileSample: 2})
	if err != nil {
		t.Fatalf("LoadView: %v", err)
	}
	if view.TradingDate != "2026-09-04" {
		t.Errorf("trading date = %s, want 2026-09-04", view.TradingDate)
	}
	if view.AsOf != "2026-09-04" {
		t.Errorf("as-of = %s, want 2026-09-04", view.AsOf)
	}
	if len(view.Institutions) != 3 {
		t.Fatalf("%d institutions, want 3", len(view.Institutions))
	}
	for i, name := range institutional.RequiredInstitutions {
		if view.Institutions[i].Institution != name {
			t.Errorf("institution %d = %s, want %s", i, view.Institutions[i].Institution, name)
		}
	}
	// POSITION totals 1,500 + 3,000 + 4,500.
	total, ok := view.Total.Position.Lots()
	if !ok {
		t.Fatalf("the total POSITION is absent: %s / %s", view.Total.Position.Status,
			view.Total.Reason)
	}
	if total != 9000 {
		t.Errorf("total POSITION = %v, want 9000", total)
	}
	// CHANGE totals 500 + 1,000 + 1,500 over a COMMON baseline.
	chg, ok := view.Total.Change.Lots()
	if !ok {
		t.Fatalf("the total CHANGE is absent: %s / %s", view.Total.Change.Status,
			view.Total.Change.Reason)
	}
	if chg != 3000 {
		t.Errorf("total CHANGE = %v, want 3000", chg)
	}
	if view.Total.ChangeBaselineDate != "2026-09-01" {
		t.Errorf("aggregate baseline = %s, want 2026-09-01", view.Total.ChangeBaselineDate)
	}
	if view.Status != institutional.StatusAvailable {
		t.Errorf("view status = %s, want AVAILABLE (reason %q)", view.Status, view.Reason)
	}
	if len(view.Evidence) == 0 {
		t.Error("the view carries no evidence")
	}
	if res.ValidObservations != 2 {
		t.Errorf("the returned result says %d valid observations, want 2", res.ValidObservations)
	}

	// The evidence carries the (1 observation, 3 calendar days) pair for CHANGE.
	found := false
	for _, e := range view.Evidence {
		if e.Institution == institutional.InstitutionDealer &&
			e.Label == institutional.HorizonLabel(1) {
			found = true
			if e.BaselineDate != "2026-09-01" || e.CalendarGapDays != 3 || e.ObservationGap != 1 {
				t.Errorf("CHANGE evidence = baseline %s, %d observations, %d days; want "+
					"2026-09-01, 1, 3", e.BaselineDate, e.ObservationGap, e.CalendarGapDays)
			}
		}
	}
	if !found {
		t.Error("no CHANGE evidence line for the dealer")
	}
	if !view.HasWarning(institutional.WarnObservationGap) {
		t.Error("a one-observation CHANGE spanning three calendar days raised no warning")
	}
}

// TestAMissingInstitutionMakesTheTotalPartialWithNoValue is §10.4: "外資 being present is not
// grounds for calling the total available."
func TestAMissingInstitutionMakesTheTotalPartialWithNoValue(t *testing.T) {
	st := openR15(t)
	// Only two institutions in the response.
	fetched := at(t, "2026-09-04 15:30:00")
	putDay(t, st, "2026-09-04", fetched, []reading{
		rd(institutional.InstitutionDealer, 20, 1500),
		rd(institutional.InstitutionForeign, 60, 4500),
	})
	putHealth(t, st, "2026-09-04", derivatives.StatusAvailable, fetched, "")

	view, _, err := history.LoadView(ctx(), st, query("2026-09-04", 5),
		institutional.Policy{MinPercentileSample: 1})
	if err != nil {
		t.Fatalf("LoadView: %v", err)
	}
	if view.Total.Status != institutional.StatusPartial {
		t.Errorf("total status = %s, want PARTIAL", view.Total.Status)
	}
	if view.Total.Position.Observed {
		t.Error("a total was published with one institution's POSITION absent")
	}
	if view.Status != institutional.StatusPartial {
		t.Errorf("view status = %s, want PARTIAL", view.Status)
	}
	// 投信 is present as a row-less observation rather than absent, because the snapshot was
	// AVAILABLE and simply carried nothing for it — so the warning is POSITION_ABSENT.
	if !view.HasWarning(institutional.WarnPositionAbsent) &&
		!view.HasWarning(institutional.WarnMissingInstitution) {
		t.Errorf("neither a missing institution nor an absent POSITION was warned about: %+v",
			view.Warnings)
	}
}

// TestScopesAreNeverMixed: an MTX read is a different series with different numbers, and the TX
// read never sees MTX lots.
func TestScopesAreNeverMixed(t *testing.T) {
	st := theCorrectionFixture(t)
	q := query("2026-09-04", 5)
	q.Scope = institutional.ScopeMTX
	res := mustLoad(t, st, q)
	s := dealerSeries(t, res)
	got, ok := positionOn(t, s, "2026-09-04")
	if !ok {
		t.Fatal("no MTX POSITION")
	}
	if got != -888 {
		t.Errorf("MTX POSITION = %v, want -888 — the TX figure is 1,500 and the two are not "+
			"the same instrument", got)
	}
	for _, o := range s.Observations {
		if o.Scope.ID != "MTX" {
			t.Errorf("observation for %s carries scope %s", o.TradingDate, o.Scope)
		}
	}
}

// TestQueryValidation: the mixtures that have no correct answer are refused rather than
// defaulted.
func TestQueryValidation(t *testing.T) {
	st := openR15(t)
	base := query("2026-09-04", 5)

	for _, tc := range []struct {
		name   string
		mutate func(history.Query) history.Query
	}{
		{"no dataset", func(q history.Query) history.Query { q.Dataset = ""; return q }},
		{"no source", func(q history.Query) history.Query { q.Source = ""; return q }},
		{"a session this feed does not have", func(q history.Query) history.Query {
			q.Session = derivatives.SessionDay
			return q
		}},
		{"no institutions", func(q history.Query) history.Query { q.Institutions = nil; return q }},
		{"a repeated institution", func(q history.Query) history.Query {
			q.Institutions = []string{institutional.InstitutionDealer, institutional.InstitutionDealer}
			return q
		}},
		{"an as-of that is not a date", func(q history.Query) history.Query {
			q.AsOf = "20260904"
			return q
		}},
		{"an unbuilt scope", func(q history.Query) history.Query {
			q.Scope = institutional.InstrumentScope{ID: "TX", Product: institutional.ProductTX}
			return q
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := history.Load(ctx(), st, tc.mutate(base)); err == nil {
				t.Errorf("%s was accepted", tc.name)
			}
		})
	}
}

// TestAnEmptyStoreIsNotAnObservation: nothing visible is ErrNoObservations, never a view full
// of zeros.
func TestAnEmptyStoreIsNotAnObservation(t *testing.T) {
	st := openR15(t)
	res, err := history.Load(ctx(), st, query("2026-09-04", 5))
	if err != nil {
		t.Fatalf("Load over an empty store: %v", err)
	}
	if len(res.Dates) != 0 || res.ValidObservations != 0 {
		t.Errorf("an empty store produced %d dates", len(res.Dates))
	}
	if _, _, err := history.LoadView(ctx(), st, query("2026-09-04", 5),
		institutional.DefaultPolicy()); !errors.Is(err, history.ErrNoObservations) {
		t.Errorf("LoadView over an empty store returned %v, want ErrNoObservations", err)
	}
}

// TestRequiredObservationsCoversTheWindowAndTheLongestHorizon.
func TestRequiredObservationsCoversTheWindowAndTheLongestHorizon(t *testing.T) {
	p := institutional.DefaultPolicy()
	got := history.RequiredObservations(p)
	want := p.PercentileLookbackObservations + 10
	if got != want {
		t.Errorf("RequiredObservations = %d, want %d (a %d-observation window plus the "+
			"longest horizon)", got, want, p.PercentileLookbackObservations)
	}
}

// bulkImportFixture is the shape that separates §7.3's TWO bounds.
//
// Every snapshot carries the SAME fetched_at — an import that recovered a run of sessions in one
// go, which is the ordinary shape of a backfill and of any archive replay. fetched_at is then
// useless as a proxy for "had this session happened yet": it is identical for a session six
// months old and one three days in the future of the reading being replayed. Only
// `trading_date <= as_of` keeps the later sessions out.
//
// §7.3 states it exactly: "Both bounds are load-bearing and they are not the same bound. The
// first says the session had happened; the second says we had already fetched it." A fixture in
// which every session was fetched on its own day cannot tell the two apart, and a reader that
// dropped the trading_date bound would pass against it.
func bulkImportFixture(t *testing.T, n int) *store.Store {
	t.Helper()
	st := openR15(t)
	imported := at(t, "2026-06-01 08:00:00")
	for i := 1; i <= n; i++ {
		date := fmt.Sprintf("2026-06-%02d", i)
		putDay(t, st, date, imported, threeInstitutions(float64(-i*100), float64(i*100)))
		putHealth(t, st, date, derivatives.StatusAvailable, imported, "")
	}
	return st
}

// TestTheTwoAsOfBoundsAreNotTheSameBound.
//
// Over a bulk import, a reading as of day 10 must still see exactly ten sessions. Days 11–20 are
// in the table and were "fetched" before the cutoff; what excludes them is that they had not
// happened.
func TestTheTwoAsOfBoundsAreNotTheSameBound(t *testing.T) {
	st := bulkImportFixture(t, 20)
	res := mustLoad(t, st, query("2026-06-10", 60))

	if got := res.LatestTradingDate(); got != "2026-06-10" {
		t.Errorf("latest trading date = %s, want 2026-06-10 — days 11–20 share the import's "+
			"fetched_at, so only the trading_date bound excludes them", got)
	}
	if res.ValidObservations != 10 {
		t.Errorf("valid observations = %d, want 10", res.ValidObservations)
	}
	for _, d := range res.Dates {
		if d.TradingDate > "2026-06-10" {
			t.Errorf("%s is in a result read as of 2026-06-10", d.TradingDate)
		}
	}

	s := dealerSeries(t, res)
	cur, ok := s.Latest()
	if !ok {
		t.Fatal("no usable observation")
	}
	if cur.TradingDate != "2026-06-10" {
		t.Fatalf("the reading is %s, want 2026-06-10", cur.TradingDate)
	}
	set, err := institutional.Percentiles(cur, s.Before(cur.TradingDate),
		institutional.Policy{MinPercentileSample: 5})
	if err != nil {
		t.Fatal(err)
	}
	dist, _ := set.Distribution(institutional.SemanticsPosition)
	if len(dist.Values) != 10 {
		t.Fatalf("the reference distribution holds %d values, want 10: %v",
			len(dist.Values), dist.Values)
	}
	for _, v := range dist.Values {
		if v > 1000 {
			t.Errorf("POSITION %v is in the reference set for a reading dated day 10; it "+
				"belongs to a session that had not happened", v)
		}
	}
	if r, ok := set.Position.Value(); !ok || r != 1 {
		t.Errorf("POSITION percentile = %v (ok=%v), want 1 — a leaked day 11–20 would push "+
			"day 10 down the distribution", r, ok)
	}
}

// TestTheHealthDateBoundIsAlsoAnAsOfBound: the same rule for the other half of the date
// universe. A NOT_PUBLISHED record for a session in the future of the reading must not make that
// session a date the walk knows about.
func TestTheHealthDateBoundIsAlsoAnAsOfBound(t *testing.T) {
	st := openR15(t)
	imported := at(t, "2026-06-01 08:00:00")
	putDay(t, st, "2026-06-02", imported, threeInstitutions(-100, 100))
	putHealth(t, st, "2026-06-02", derivatives.StatusAvailable, imported, "")
	// A health record for a later session, recorded at import time.
	putHealth(t, st, "2026-06-09", derivatives.StatusNotPublished, imported, "not out yet")

	res := mustLoad(t, st, query("2026-06-05", 10))
	for _, d := range res.Dates {
		if d.TradingDate == "2026-06-09" {
			t.Errorf("a health record for 2026-06-09 reached a result read as of 2026-06-05")
		}
	}
	if len(res.Dates) != 1 {
		t.Errorf("the walk covered %d dates, want 1", len(res.Dates))
	}
}

// The SQL ORDER BY and the Go re-sort are two different guarantees, and only one of them was
// pinned.
//
// `candidateDates` orders in SQL and then re-sorts in Go, so a reviewer mutating either one
// alone finds nothing: the Go sort fixes the SQL's order, and the SQL's LIMIT truncation is
// invisible whenever every candidate fits under MaxScanDates. Mutating BOTH is killed; mutating
// the SQL alone survived.
//
// What the SQL's ORDER BY actually decides is WHICH dates survive the LIMIT. Order on
// fetched_at over a bulk import and the truncation keeps an arbitrary set rather than the most
// recent ones — so the walk can miss the sessions it was asked for while still returning a
// correctly-sorted list of the wrong days.
//
// MaxScanDates below the available history is the shape that exposes it, and it is not
// artificial: DefaultMaxScanDates exists precisely because the walk must be bounded.
func TestTheScanLimitKeepsTheMostRecentDates(t *testing.T) {
	st := bulkImportFixture(t, 20)

	q := query("2026-06-20", 3)
	q.MaxScanDates = 5 // fewer than the 20 dates in the store

	res := mustLoad(t, st, q)
	if len(res.Series) == 0 {
		t.Fatal("no series returned")
	}
	obs := res.Series[0].Observations
	if len(obs) < 3 {
		t.Fatalf("got %d observations, want at least 3", len(obs))
	}

	// With a limit of 5 the walk must see 06-20 … 06-16 — the five most recent — and therefore
	// return the three most recent as its series.
	for i, want := range []string{"2026-06-20", "2026-06-19", "2026-06-18"} {
		if obs[i].TradingDate != want {
			t.Errorf("observation %d is %s, want %s — the scan limit kept a set of dates "+
				"chosen by fetch time rather than by session, so the walk truncated away the "+
				"days it was asked for", i, obs[i].TradingDate, want)
		}
	}
}
