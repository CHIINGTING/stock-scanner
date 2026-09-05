package healthcheck

import (
	"context"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// outcomeFixture stores one health check and returns the grader plus the snapshot id.
//
// The price series runs well past the check date so horizons can be filled; each test caps
// what is visible with OutcomeConfig.AsOf rather than by reshaping the series, which is how
// the real thing behaves — the bars exist, the question is which ones may be used.
func outcomeFixture(t *testing.T, checkDate string, forwardDays int, rise float64) (*Outcomes, *History, int64) {
	t.Helper()
	h := openTestHistory(t)

	// A check on checkDate with a known close.
	end := day(checkDate)
	s, root := valService(t, checkDate)
	writeSessions(t, root, 25, checkDate, 18)
	hc := check(t, s, Request{Symbol: "2330", AsOf: checkDate})
	id, err := h.Save(context.Background(), hc)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	base, ok := hc.Price.Close.Float()
	if !ok {
		t.Fatal("the fixture produced no basis price")
	}

	// Forward bars from base, rising by `rise` a day.
	bars := make([]fetcher.Candle, 0, forwardDays)
	for i := 1; i <= forwardDays; i++ {
		p := base + rise*float64(i)
		bars = append(bars, fetcher.Candle{
			Date: end.AddDate(0, 0, i), Open: p, High: p, Low: p, Close: p,
			AdjClose: p, Volume: 1000,
		})
	}
	// The snapshot's own session has to be in the series too, or nothing anchors it.
	bars = append([]fetcher.Candle{{
		Date: end, Open: base, High: base, Low: base, Close: base, AdjClose: base, Volume: 1000,
	}}, bars...)

	o := &Outcomes{
		History: h,
		Prices:  stubPrices{data: map[string]fetcher.StockData{"2330": {Symbol: "2330", Candles: bars}}},
		Now:     func() time.Time { return end.AddDate(0, 0, forwardDays).Add(15 * time.Hour) },
	}
	return o, h, id
}

// ── it grades only its own rows ───────────────────────────────────────────────────────

// PendingHorizon returns every source_tab. Writing outcomes for the scanner's snapshots
// would be this sidecar grading judgements it did not make.
func TestOnlyHealthCheckSnapshotsAreGraded(t *testing.T) {
	ctx := context.Background()
	o, h, healthID := outcomeFixture(t, "2026-09-04", 25, 1)

	// A scanner snapshot on the same day, in the same database.
	run, err := h.store.Scans().StartRun(ctx, store.ScanRun{
		RunUID: "scan-1", TradingDate: "2026-09-04", StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	base := 700.0
	scanSnap, err := h.store.Scans().SaveSnapshot(ctx, store.StockSnapshot{
		ScanRunID: run.ID, Symbol: "2330", Name: "台積電", TradingDate: "2026-09-04",
		SourceTab: store.TabMarket, Close: &base, ScannerAction: "BUY",
	})
	if err != nil {
		t.Fatal(err)
	}
	// It has to be pending for the filter to be doing any work.
	if _, err := h.store.Outcomes().Ensure(ctx, scanSnap.ID, base); err != nil {
		t.Fatal(err)
	}

	rep, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Examined != 1 {
		t.Fatalf("examined %d snapshots, want only the health check", rep.Examined)
	}

	// The scanner's row must be untouched.
	got, err := h.store.Outcomes().BySnapshot(ctx, scanSnap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Return1D != nil || got.Return5D != nil || got.BarsObserved != 0 {
		t.Fatalf("the scanner's outcome row was written by the dashboard: %+v", got)
	}
	// And the health check's row was.
	mine, err := h.store.Outcomes().BySnapshot(ctx, healthID)
	if err != nil {
		t.Fatal(err)
	}
	if mine.Return5D == nil {
		t.Fatalf("the health check was not graded: %+v", mine)
	}
}

// ── one call, one vote ────────────────────────────────────────────────────────────────

// Three checks of one stock on one day are three snapshots. Grading all of them counts one
// opinion three times in every aggregate.
func TestRepeatedChecksAreGradedOnce(t *testing.T) {
	ctx := context.Background()
	o, h, first := outcomeFixture(t, "2026-09-04", 25, 1)

	// Two more checks of the same stock, same day.
	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)
	hc := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	second, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}
	third, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}
	// Save opens the outcome row for each, so all three are pending and the dedupe has
	// something to do.
	for _, id := range []int64{first, second, third} {
		if _, err := h.store.Outcomes().BySnapshot(ctx, id); err != nil {
			t.Fatalf("snapshot %d has no outcome row, so it is invisible to the grader: %v", id, err)
		}
	}

	rep, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Examined != 1 {
		t.Fatalf("examined %d, want 1 — one opinion must not be counted three times", rep.Examined)
	}
	if rep.SkippedDuplicate != 2 {
		t.Fatalf("SkippedDuplicate = %d, want 2", rep.SkippedDuplicate)
	}

	// The LAST check is the graded one; the earlier rows survive ungraded.
	last, err := h.store.Outcomes().BySnapshot(ctx, third)
	if err != nil {
		t.Fatal(err)
	}
	if last.Return5D == nil {
		t.Fatalf("the latest check was not graded: %+v", last)
	}
	for _, id := range []int64{first, second} {
		got, err := h.store.Outcomes().BySnapshot(ctx, id)
		if err != nil {
			t.Fatalf("the earlier snapshot was deleted, not just left ungraded: %v", err)
		}
		if got.Return5D != nil {
			t.Fatalf("an earlier duplicate was graded too: %+v", got)
		}
	}
}

// ── no future returns ─────────────────────────────────────────────────────────────────

// A horizon is filled only when the sessions to support it exist. Nil because the future has
// not happened is a different fact from nil because the data is missing, and BarsObserved is
// what tells them apart.
func TestHorizonsAreFilledOnlyWhenTheBarsExist(t *testing.T) {
	ctx := context.Background()
	o, h, id := outcomeFixture(t, "2026-09-04", 30, 1)

	// Only three sessions have passed.
	rep, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-09-07"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.BarsObserved != 3 {
		t.Fatalf("BarsObserved = %d, want 3", got.BarsObserved)
	}
	if got.Return1D == nil || got.Return3D == nil {
		t.Fatalf("1d/3d should be known after three sessions: %+v", got)
	}
	if got.Return5D != nil || got.Return20D != nil {
		t.Fatalf("a horizon was filled before its sessions existed: %+v", got)
	}
	if got.MaxGain20D != nil {
		t.Fatalf("20-day extremes were computed from three sessions: %+v", got)
	}
	if rep.Filled[1] != 1 || rep.Filled[5] != 0 {
		t.Fatalf("report disagrees with the rows: %+v", rep.Filled)
	}

	// Later, with more sessions, the rest fill in.
	rep2, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	got2, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Return20D == nil {
		t.Fatalf("20d still unfilled with 30 sessions available: %+v", got2)
	}
	// The already-known horizons were left exactly as first written.
	if *got2.Return1D != *got.Return1D {
		t.Fatalf("a recorded return was rewritten: %v → %v", *got.Return1D, *got2.Return1D)
	}
	if rep2.AlreadyKnown == 0 {
		t.Fatalf("the rerun reported nothing as already known: %+v", rep2)
	}
}

// ── the arithmetic ────────────────────────────────────────────────────────────────────

// Returns are measured from the snapshot's own close, over the sessions AFTER it.
func TestReturnsAreMeasuredFromTheSnapshotClose(t *testing.T) {
	ctx := context.Background()
	// base 759, rising 1/day → +1 after one session, +5 after five.
	o, h, id := outcomeFixture(t, "2026-09-04", 30, 1)
	if _, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"}); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseClose != 759 {
		t.Fatalf("base = %v, want the snapshot's own 759", got.BaseClose)
	}
	want1 := 1.0 / 759 * 100
	if got.Return1D == nil || abs(*got.Return1D-want1) > 1e-9 {
		t.Fatalf("1d = %v, want %v", got.Return1D, want1)
	}
	want5 := 5.0 / 759 * 100
	if got.Return5D == nil || abs(*got.Return5D-want5) > 1e-9 {
		t.Fatalf("5d = %v, want %v", got.Return5D, want5)
	}
	// A rising series has a positive max gain and no drawdown.
	if got.MaxGain20D == nil || *got.MaxGain20D <= 0 {
		t.Fatalf("max gain = %v", got.MaxGain20D)
	}
	if got.MaxDrawdown20D == nil || *got.MaxDrawdown20D != 0 {
		t.Fatalf("max drawdown = %v, want 0 for a monotonically rising series", got.MaxDrawdown20D)
	}
}

// A falling series produces negative returns. A grader that only ever reported gains would
// be useless in exactly the case it matters.
func TestNegativeReturnsAreRecorded(t *testing.T) {
	ctx := context.Background()
	o, h, id := outcomeFixture(t, "2026-09-04", 30, -2)
	if _, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"}); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Return5D == nil || *got.Return5D >= 0 {
		t.Fatalf("5d = %v, want a loss", got.Return5D)
	}
	if got.MaxDrawdown20D == nil || *got.MaxDrawdown20D >= 0 {
		t.Fatalf("max drawdown = %v, want negative", got.MaxDrawdown20D)
	}
}

// A snapshot with no basis price can never be graded, and is reported as such rather than
// being given a zero return.
func TestSnapshotWithNoCloseIsSkipped(t *testing.T) {
	ctx := context.Background()
	h := openTestHistory(t)
	run, err := h.store.Scans().StartRun(ctx, store.ScanRun{
		RunUID: "hc-1", TradingDate: "2026-09-04", StartedAt: time.Now().UTC(),
		ScannerVersion: HistoryVersion, Note: HistoryNote,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := h.store.Scans().SaveSnapshot(ctx, store.StockSnapshot{
		ScanRunID: run.ID, Symbol: "9999", TradingDate: "2026-09-04",
		SourceTab: TabHealthCheck, Close: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	// No outcome row is opened for it, because there is no basis to measure from. It is
	// therefore invisible to the grader — which is the correct outcome, not an oversight:
	// a row with a zero basis would be divided by.
	if _, err := h.store.Outcomes().BySnapshot(ctx, snap.ID); err == nil {
		t.Fatal("an outcome row was opened for a snapshot with no basis price")
	}
	o := &Outcomes{History: h, Prices: stubPrices{}}
	rep, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Examined != 0 {
		t.Fatalf("a snapshot with no basis price was examined: %+v", rep)
	}
}

// Saving a check opens its outcome row, so the grader can find it. Without this the grader
// finds nothing forever, and the silence looks like "no results yet".
func TestSaveOpensTheOutcomeRow(t *testing.T) {
	ctx := context.Background()
	h := openTestHistory(t)
	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)
	hc := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})

	id, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatalf("Save did not open an outcome row: %v", err)
	}
	base, _ := hc.Price.Close.Float()
	if got.BaseClose != base {
		t.Fatalf("base = %v, want the check's own close %v", got.BaseClose, base)
	}
	if got.Return1D != nil || got.BarsObserved != 0 {
		t.Fatalf("the row was opened pre-filled: %+v", got)
	}
	// A re-save must not move the basis of a measurement already under way.
	hc.Price.Close = Num(9999, "TWD", 2, "test")
	if _, err := h.Save(ctx, hc); err != nil {
		t.Fatal(err)
	}
	again, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if again.BaseClose != base {
		t.Fatalf("the basis moved to %v; a stored measurement was redefined", again.BaseClose)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// ── the cross-pass dedupe ─────────────────────────────────────────────────────────────

// The duplicate must stay ungraded across REPEATED passes, which is how this job is used.
//
// Deciding the winner from the pending set alone is correct on the first pass and wrong on
// the second: once the winner has filled every horizon it stops being pending, and the
// earlier duplicate becomes the only candidate — so it is promoted to "the latest" and
// graded too. Both rows then carry the same returns and one opinion counts twice, permanently,
// because SetReturn is write-once. Every duplicated symbol-day reaches that state eventually.
func TestDuplicateStaysUngradedAcrossRepeatedPasses(t *testing.T) {
	ctx := context.Background()
	o, h, first := outcomeFixture(t, "2026-09-04", 30, 1)

	s, root := valService(t, "2026-09-04")
	writeSessions(t, root, 25, "2026-09-04", 18)
	hc := check(t, s, Request{Symbol: "2330", AsOf: "2026-09-04"})
	second, err := h.Save(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}

	// Pass 1: the later snapshot wins and fills every horizon.
	rep1, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Examined != 1 || rep1.SkippedDuplicate != 1 {
		t.Fatalf("pass 1: examined %d, skipped %d; want 1 and 1",
			rep1.Examined, rep1.SkippedDuplicate)
	}

	// Pass 2: the winner is no longer pending. The earlier row must NOT be promoted.
	rep2, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Examined != 0 {
		t.Fatalf("pass 2 examined %d snapshots; the duplicate was promoted once the winner "+
			"stopped being pending", rep2.Examined)
	}
	for _, n := range rep2.Filled {
		if n != 0 {
			t.Fatalf("pass 2 filled horizons it should not have: %+v", rep2.Filled)
		}
	}

	// And on the rows: the earlier one is still ungraded.
	early, err := h.store.Outcomes().BySnapshot(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if early.Return1D != nil || early.Return20D != nil || early.BarsObserved != 0 {
		t.Fatalf("the earlier duplicate was graded on a later pass: %+v", early)
	}
	late, err := h.store.Outcomes().BySnapshot(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if late.Return20D == nil {
		t.Fatalf("the canonical snapshot lost its grades: %+v", late)
	}

	// A third pass changes nothing either.
	if _, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"}); err != nil {
		t.Fatal(err)
	}
	again, err := h.store.Outcomes().BySnapshot(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if again.Return1D != nil {
		t.Fatalf("a third pass graded the duplicate: %+v", again)
	}
}

// ── the unclosed session ──────────────────────────────────────────────────────────────

// A session that has not closed must never be written into a write-once column.
//
// internal/fetcher backfills the current session from the quote meta, so a bar dated today
// exists from the opening bell carrying a mid-session price. Grading at 10:00 would fix that
// price as a completed session permanently, with nothing on the row to say it was intraday.
func TestUnclosedSessionIsNeverGraded(t *testing.T) {
	ctx := context.Background()
	o, h, id := outcomeFixture(t, "2026-09-04", 3, 10)

	// 10:00 on the third forward session — that day is still trading.
	midSession := day("2026-09-07").Add(10 * time.Hour)
	o.Now = func() time.Time { return midSession }

	if _, err := o.Run(ctx, OutcomeConfig{}); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.BarsObserved != 2 {
		t.Fatalf("BarsObserved = %d, want 2 — the running session was counted", got.BarsObserved)
	}
	if got.Return3D != nil {
		t.Fatalf("the 3-day return was fixed from an unclosed session: %v", *got.Return3D)
	}

	// After the close it is graded, and from the real closing price.
	o.Now = func() time.Time { return day("2026-09-07").Add(16 * time.Hour) }
	if _, err := o.Run(ctx, OutcomeConfig{}); err != nil {
		t.Fatal(err)
	}
	after, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Return3D == nil {
		t.Fatalf("the closed session was still not graded: %+v", after)
	}
	if after.BarsObserved != 3 {
		t.Fatalf("BarsObserved = %d after the close, want 3", after.BarsObserved)
	}
}

// An explicit as-of date must not reopen the same hole.
func TestExplicitAsOfCannotReachAnUnclosedSession(t *testing.T) {
	ctx := context.Background()
	o, h, id := outcomeFixture(t, "2026-09-04", 3, 10)
	o.Now = func() time.Time { return day("2026-09-07").Add(10 * time.Hour) }

	// Asking for today by name, mid-session.
	if _, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-09-07"}); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Return3D != nil {
		t.Fatalf("an explicit as-of reached the running session: %v", *got.Return3D)
	}
	if got.BarsObserved != 2 {
		t.Fatalf("BarsObserved = %d, want 2", got.BarsObserved)
	}
}

// The default as-of is the last CLOSED session, not simply today.
func TestDefaultAsOfIsTheLastClosedSession(t *testing.T) {
	o := &Outcomes{Now: func() time.Time { return day("2026-09-07").Add(10 * time.Hour) }}
	if got := o.defaultAsOf(); got != "2026-09-06" {
		t.Errorf("mid-session default = %s, want the previous day", got)
	}
	o.Now = func() time.Time { return day("2026-09-07").Add(16 * time.Hour) }
	if got := o.defaultAsOf(); got != "2026-09-07" {
		t.Errorf("after the close default = %s, want today", got)
	}
	// The boundary: 13:30 close plus the settle margin.
	o.Now = func() time.Time { return day("2026-09-07").Add(13*time.Hour + 29*time.Minute) }
	if got := o.defaultAsOf(); got == "2026-09-07" {
		t.Errorf("13:29 counted as closed")
	}
	o.Now = func() time.Time { return day("2026-09-07").Add(14 * time.Hour) }
	if got := o.defaultAsOf(); got != "2026-09-07" {
		t.Errorf("14:00 not counted as closed: %s", got)
	}
}

// A later check that recorded no price must not silence the day.
//
// A failed price fetch still saves a snapshot — the honest record of an attempt — but it
// carries no basis close and can never be graded. If it won on ID alone, the one gradeable
// check of that symbol-day would be set aside as its duplicate and the outcome would never
// appear at all. "The last check of the day" therefore means the last one with a price.
func TestAPricelessLaterCheckDoesNotSilenceTheDay(t *testing.T) {
	ctx := context.Background()
	o, h, graded := outcomeFixture(t, "2026-09-04", 30, 1)

	// A later snapshot for the same symbol-day with no close, as a failed fetch leaves.
	run, err := h.store.Scans().StartRun(ctx, store.ScanRun{
		RunUID: "hc-priceless", TradingDate: "2026-09-04", StartedAt: time.Now().UTC(),
		ScannerVersion: HistoryVersion, Note: HistoryNote,
	})
	if err != nil {
		t.Fatal(err)
	}
	later, err := h.store.Scans().SaveSnapshot(ctx, store.StockSnapshot{
		ScanRunID: run.ID, Symbol: "2330", Name: "台積電", TradingDate: "2026-09-04",
		SourceTab: TabHealthCheck, Close: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if later.ID <= graded {
		t.Fatalf("fixture: the priceless snapshot (%d) must be newer than the gradeable one (%d)",
			later.ID, graded)
	}

	rep, err := o.Run(ctx, OutcomeConfig{AsOf: "2026-10-15"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Examined != 1 {
		t.Fatalf("examined %d; the gradeable check was silenced by a priceless later one", rep.Examined)
	}
	got, err := h.store.Outcomes().BySnapshot(ctx, graded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Return20D == nil {
		t.Fatalf("the only gradeable check of the day was never graded: %+v", got)
	}
	// The priceless row still exists, ungraded — it is a real record of an attempt.
	if _, err := h.store.Outcomes().BySnapshot(ctx, later.ID); err == nil {
		t.Fatal("an outcome row was opened for a snapshot with no basis price")
	}
}

// The market comes from the catalog when one is supplied, so a TPEx stock costs no extra probe.
func TestCatalogResolvesTheMarket(t *testing.T) {
	o := &Outcomes{}
	if got := o.marketOf("2330"); got != "" {
		t.Errorf("with no catalog the market must be blank (fetcher probes), got %q", got)
	}
	o.Catalog = testCatalog(t)
	if got := o.marketOf("2330"); got != "TW" {
		t.Errorf("marketOf(2330) = %q, want TW from the catalog", got)
	}
	if got := o.marketOf("0000"); got != "" {
		t.Errorf("an unknown symbol must fall back to the probe, got %q", got)
	}
}
