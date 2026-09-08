package optionshistory_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/dailydata"
	"github.com/deep-huang/stock-scanner/internal/derivatives"
	"github.com/deep-huang/stock-scanner/internal/derivatives/institutional"
	"github.com/deep-huang/stock-scanner/internal/derivatives/options"
	"github.com/deep-huang/stock-scanner/internal/derivatives/optionshistory"
	"github.com/deep-huang/stock-scanner/internal/derivatives/provider"
	"github.com/deep-huang/stock-scanner/internal/store"
)

// M5's point-in-time reader, and the two properties it has that M4's does not: one session
// date is TWO snapshots, and the settling expiry comes from the snapshot archived FOR that
// date rather than from the live feed.

const (
	source            = provider.SourceOptionsByStrike
	dataset           = "options_by_strike"
	settlementSource  = provider.SourceFinalSettlement
	settlementDataset = "final_settlement"
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

// at builds a UTC instant from a Taipei wall clock: every boundary in §7.3 is only meaningful
// in Taipei and the column stores UTC.
func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, dailydata.Taipei)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

func fptr(v float64) *float64 { return &v }

// strikes builds one session's rows: a call and a put at one strike of one expiry.
func strikes(expiry string, callVol, putVol, callOI, putOI *float64) []provider.OptionStrikeRow {
	return []provider.OptionStrikeRow{
		{Product: provider.ProductTXO, ExpiryCode: expiry, Strike: 46000,
			CallPut: provider.CallSide, Volume: callVol, OI: callOI},
		{Product: provider.ProductTXO, ExpiryCode: expiry, Strike: 46000,
			CallPut: provider.PutSide, Volume: putVol, OI: putOI},
	}
}

// putSession writes one session's snapshot and its rows.
func putSession(t *testing.T, st *store.Store, date, session string, fetched time.Time,
	rows []provider.OptionStrikeRow) derivatives.Snapshot {

	t.Helper()
	payload := fmt.Sprintf("%s|%s|%v", date, session, rows)
	snap, err := derivatives.PutSnapshot(ctx(), st, derivatives.Snapshot{
		Source: source, TradingDate: date, TradingSession: session,
		DateSource: derivatives.DateObserved, Payload: payload, FetchedAt: fetched,
	})
	if err != nil {
		t.Fatalf("PutSnapshot(%s/%s @ %s): %v", date, session, fetched, err)
	}
	if _, err := derivatives.PutOptionStrikes(ctx(), st, snap.ID, source, fetched, rows); err != nil {
		t.Fatalf("PutOptionStrikes(%s/%s): %v", date, session, err)
	}
	return snap
}

func putHealth(t *testing.T, st *store.Store, date, session string, status derivatives.Status,
	recorded time.Time, rejected int) {

	t.Helper()
	if err := derivatives.PutDataHealth(ctx(), st, derivatives.DataHealth{
		Dataset: dataset, TradingDate: date, Session: session, Source: source,
		Status: status, AsOf: date, RecordedAt: recorded, RejectedRowCount: rejected,
	}); err != nil {
		t.Fatalf("PutDataHealth(%s/%s, %s): %v", date, session, status, err)
	}
}

// putSettlement archives a settlement response FOR a trading date. The feed carries no date of
// its own, so the snapshot's is ASSIGNED — which is exactly why the comparison has to read the
// snapshot archived for D rather than the live response.
func putSettlement(t *testing.T, st *store.Store, date, settlingDay, expiry string, fetched time.Time) {
	t.Helper()
	payload := fmt.Sprintf(`[{"TheFinalSettlementDay":"%s","Contract":"TXO",`+
		`"ContractName":"臺指選擇權","ContractDeliveryMonth":"%s",`+
		`"TheFinalSettlementPrice":"46520"}]`, settlingDay, expiry)
	if _, err := derivatives.PutSnapshot(ctx(), st, derivatives.Snapshot{
		Source: settlementSource, TradingDate: date, TradingSession: derivatives.SessionCombined,
		DateSource: derivatives.DateAssigned, Payload: payload, FetchedAt: fetched,
	}); err != nil {
		t.Fatalf("PutSnapshot(settlement %s): %v", date, err)
	}
}

func query(asOf, session string, count int) optionshistory.Query {
	return optionshistory.Query{
		Dataset:           dataset,
		Source:            source,
		SettlementDataset: settlementDataset,
		SettlementSource:  settlementSource,
		Product:           provider.ProductTXO,
		Family:            options.FamilyAll,
		Session:           session,
		AsOf:              asOf,
		Count:             count,
	}
}

func mustLoad(t *testing.T, st *store.Store, q optionshistory.Query) optionshistory.Result {
	t.Helper()
	res, err := optionshistory.Load(ctx(), st, q)
	if err != nil {
		t.Fatalf("Load(asOf=%s): %v", q.AsOf, err)
	}
	return res
}

func ratioOn(t *testing.T, res optionshistory.Result, date string, m options.MetricType) (float64, bool) {
	t.Helper()
	for _, o := range res.Observations {
		if o.TradingDate != date {
			continue
		}
		p, err := o.PCR(m)
		if err != nil {
			t.Fatal(err)
		}
		return p.Ratio.Value()
	}
	t.Fatalf("no observation for %s", date)
	return 0, false
}

// theCorrectionFixture is M4's three-revision case in M5's shape:
//
//	09-01 revision 0, fetched 09-01, put OI 1,000
//	09-04 revision 0, fetched 09-04, put OI 1,500
//	09-01 revision 1, fetched 09-05, put OI 1,200   ← a correction to an EARLIER date, LATER
func theCorrectionFixture(t *testing.T) *store.Store {
	st := openR15(t)
	for _, d := range []struct {
		date    string
		fetched string
		putOI   float64
	}{
		{"2026-09-01", "2026-09-01 15:30:00", 1000},
		{"2026-09-04", "2026-09-04 15:30:00", 1500},
		{"2026-09-01", "2026-09-05 09:00:00", 1200},
	} {
		fetched := at(t, d.fetched)
		putSession(t, st, d.date, provider.SessionDay, fetched,
			strikes("202609", fptr(10), fptr(10), fptr(1000), fptr(d.putOI)))
		putHealth(t, st, d.date, provider.SessionDay, derivatives.StatusAvailable, fetched, 0)
		putSettlement(t, st, d.date, "20260828", "202608F4", fetched)
	}
	return st
}

// TestEachDateResolvesToTheRevisionVisibleAtTheCutoff.
func TestEachDateResolvesToTheRevisionVisibleAtTheCutoff(t *testing.T) {
	st := theCorrectionFixture(t)

	t.Run("as of 09-04 the correction is invisible", func(t *testing.T) {
		res := mustLoad(t, st, query("2026-09-04", provider.SessionDay, 10))
		got, ok := ratioOn(t, res, "2026-09-01", options.MetricOIPCR)
		if !ok {
			t.Fatal("09-01 has no ratio")
		}
		if got != 1.0 {
			t.Errorf("09-01 OI PCR = %v, want 1.0 (1,000/1,000) — the correction was fetched "+
				"on 09-05 and a reading dated 09-04 could not have had it", got)
		}
		if res.LatestTradingDate() != "2026-09-04" {
			t.Errorf("latest trading date = %s", res.LatestTradingDate())
		}
	})

	t.Run("as of 09-06 the correction is visible and 09-04 is still the latest session", func(t *testing.T) {
		res := mustLoad(t, st, query("2026-09-06", provider.SessionDay, 10))
		got, ok := ratioOn(t, res, "2026-09-01", options.MetricOIPCR)
		if !ok {
			t.Fatal("09-01 has no ratio")
		}
		if got != 1.2 {
			t.Errorf("09-01 OI PCR = %v, want 1.2 (1,200/1,000)", got)
		}
		if res.LatestTradingDate() != "2026-09-04" {
			t.Errorf("latest trading date = %s, want 2026-09-04 — a correction to an older "+
				"session does not make that session current", res.LatestTradingDate())
		}
	})
}

// TestOneSessionDateIsTwoIndependentlyResolvedSnapshots.
//
// §10.2b stores the by-strike response as two snapshots. Each is resolved against the cutoff
// on its own: the night rows are published later and can be corrected on their own, so a
// COMBINED figure legitimately spans two revisions and has to say so.
func TestOneSessionDateIsTwoIndependentlyResolvedSnapshots(t *testing.T) {
	st := openR15(t)
	const date = "2026-09-04"
	dayAt := at(t, "2026-09-04 15:30:00")
	nightAt := at(t, "2026-09-05 06:00:00")

	putSession(t, st, date, provider.SessionDay, dayAt,
		strikes("202609", fptr(100), fptr(50), fptr(1000), fptr(1000)))
	putHealth(t, st, date, provider.SessionDay, derivatives.StatusAvailable, dayAt, 0)
	putSettlement(t, st, date, "20260828", "202608F4", dayAt)

	// As of 09-04 the night session has not been fetched at all.
	early := mustLoad(t, st, query(date, provider.SessionCombined, 1))
	if len(early.Observations) != 1 {
		t.Fatalf("%d observations", len(early.Observations))
	}
	obs := early.Observations[0]
	if obs.SourceStatus != institutional.StatusPartial {
		t.Errorf("a combined figure with one session present is %s, want PARTIAL",
			obs.SourceStatus)
	}
	if obs.Volume.Ratio.Observed {
		t.Error("a COMBINED volume ratio was published from the day session alone; 一般 " +
			"alone is a third of the activity and reconciles against nothing")
	}
	if !contains(obs.Volume.Coverage.SessionsMissing, provider.SessionAfterHours) {
		t.Errorf("the absent session is not named: %+v", obs.Volume.Coverage)
	}

	// The night rows arrive.
	putSession(t, st, date, provider.SessionAfterHours, nightAt,
		strikes("202609", fptr(20), fptr(30), nil, nil))
	putHealth(t, st, date, provider.SessionAfterHours, derivatives.StatusAvailable, nightAt, 0)

	// Still invisible to a reading dated 09-04: it was fetched the next morning.
	stillEarly := mustLoad(t, st, query(date, provider.SessionCombined, 1))
	if stillEarly.Observations[0].Volume.Ratio.Observed {
		t.Error("the night snapshot fetched on 09-05 reached a reading dated 09-04")
	}

	later := mustLoad(t, st, query("2026-09-05", provider.SessionCombined, 1))
	obs = later.Observations[0]
	if obs.SourceStatus != institutional.StatusAvailable {
		t.Fatalf("status = %s (%s)", obs.SourceStatus, obs.Volume.Reason)
	}
	v, ok := obs.Volume.Ratio.Value()
	if !ok {
		t.Fatalf("no combined volume ratio: %s", obs.Volume.Reason)
	}
	if v != 80.0/120.0 {
		t.Errorf("combined volume PCR = %v, want (50+30)/(100+20)", v)
	}
	if len(obs.Provenance.Snapshots) != 2 {
		t.Errorf("the combined figure cites %d snapshots, want 2", len(obs.Provenance.Snapshots))
	}
	if at := obs.Provenance.FetchedAt(); !at.Equal(nightAt) {
		t.Errorf("fetched_at = %s, want the LATEST of the contributing snapshots (%s)",
			at, nightAt)
	}
	// The open-interest half is arithmetically UNAFFECTED by the night rows, because they
	// carry none — so a combined open-interest figure is computable and is exactly the day
	// figure. §10.8 still publishes open interest for DAY only, and the out-of-policy
	// combination says so on itself rather than being silently unavailable.
	oiCombined, ok := obs.OI.Ratio.Value()
	if !ok {
		t.Fatalf("the combined open-interest figure is absent: %s", obs.OI.Reason)
	}
	if oiCombined != 1.0 {
		t.Errorf("combined OI PCR = %v, want 1.0 — the night rows carry no open interest, "+
			"so adding them must change nothing", oiCombined)
	}
	dayOnly := mustLoad(t, st, query("2026-09-05", provider.SessionDay, 1)).Observations[0]
	if v, _ := dayOnly.OI.Ratio.Value(); v != oiCombined {
		t.Errorf("the DAY open-interest figure (%v) and the combined one (%v) differ, so a "+
			"盤後 row has started carrying open interest", v, oiCombined)
	}
	if !hasWarning(obs.OI.Warnings, options.WarnSessionOutsidePolicy) {
		t.Errorf("the combined open-interest figure carries no %s warning: %+v",
			options.WarnSessionOutsidePolicy, obs.OI.Warnings)
	}
	if hasWarning(dayOnly.OI.Warnings, options.WarnSessionOutsidePolicy) {
		t.Error("the DAY open-interest figure is flagged out of policy, and it is the policy")
	}
}

func hasWarning(ws []options.Warning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}

// TestTheSettlingExpiryComesFromTheSnapshotArchivedForTheDate.
//
// The live feed always answers with the most recent settlement event, so rerunning an older
// session against it excludes nothing and inflates open interest. Reading the snapshot
// archived FOR the date is what makes that impossible.
func TestTheSettlingExpiryComesFromTheSnapshotArchivedForTheDate(t *testing.T) {
	st := openR15(t)
	const settled = "2026-09-04"
	const after = "2026-09-09"
	rows := append(
		strikes("202609F1", fptr(10), fptr(10), fptr(6000), fptr(11000)),
		strikes("202609W2", fptr(10), fptr(10), fptr(1000), fptr(2000))...)

	fetchedA := at(t, "2026-09-04 15:30:00")
	putSession(t, st, settled, provider.SessionDay, fetchedA, rows)
	putHealth(t, st, settled, provider.SessionDay, derivatives.StatusAvailable, fetchedA, 0)
	// The settlement response archived FOR 09-04 says 202609F1 settled that day.
	putSettlement(t, st, settled, "20260904", "202609F1", fetchedA)

	// A later session, whose own settlement snapshot names a DIFFERENT expiry. This is the
	// state the live feed would be in when 09-04 is recomputed.
	fetchedB := at(t, "2026-09-09 15:30:00")
	putSession(t, st, after, provider.SessionDay, fetchedB, rows)
	putHealth(t, st, after, provider.SessionDay, derivatives.StatusAvailable, fetchedB, 0)
	putSettlement(t, st, after, "20260909", "202609W2", fetchedB)

	res := mustLoad(t, st, query("2026-09-10", provider.SessionDay, 10))

	got, ok := ratioOn(t, res, settled, options.MetricOIPCR)
	if !ok {
		t.Fatal("09-04 has no open-interest ratio")
	}
	// 202609F1 is excluded on 09-04: 2,000 / 1,000.
	if got != 2.0 {
		t.Errorf("09-04 OI PCR = %v, want 2.0 — 202609F1 settled that day and its 11,000 "+
			"lots are no longer live exposure; including them gives %v",
			got, 13000.0/7000.0)
	}
	dr, _ := res.Resolution(settled)
	if dr.SettlingExpiry != "202609F1" {
		t.Errorf("09-04 settling expiry = %q", dr.SettlingExpiry)
	}
	if !dr.SettlementRead {
		t.Error("09-04 reports no settlement read")
	}

	// And on 09-09 a different expiry settles, so a different one is excluded.
	dr9, _ := res.Resolution(after)
	if dr9.SettlingExpiry != "202609W2" {
		t.Errorf("09-09 settling expiry = %q", dr9.SettlingExpiry)
	}
}

// TestWithoutASettlementSnapshotTheOpenInterestHalfIsMissing.
func TestWithoutASettlementSnapshotTheOpenInterestHalfIsMissing(t *testing.T) {
	st := openR15(t)
	const date = "2026-09-04"
	fetched := at(t, "2026-09-04 15:30:00")
	putSession(t, st, date, provider.SessionDay, fetched,
		strikes("202609", fptr(10), fptr(20), fptr(100), fptr(200)))
	putHealth(t, st, date, provider.SessionDay, derivatives.StatusAvailable, fetched, 0)
	// No settlement snapshot at all.

	res := mustLoad(t, st, query(date, provider.SessionDay, 1))
	obs := res.Observations[0]
	if obs.OI.Ratio.Observed {
		t.Fatal("an open-interest ratio was published with no settlement snapshot behind it")
	}
	if obs.OI.Status != options.RatioMissing {
		t.Errorf("OI status = %s, want MISSING", obs.OI.Status)
	}
	if !obs.Volume.Ratio.Observed {
		t.Error("the volume half went missing too; the two have different dependencies")
	}
	dr, _ := res.Resolution(date)
	if dr.SettlementRead || dr.SettlementReason == "" {
		t.Errorf("the settlement read is not recorded as absent: %+v", dr)
	}
}

// TestAHealthRecordThatIsNotUsableWinsOverVisibleRows — the STALE laundering rule.
func TestAHealthRecordThatIsNotUsableWinsOverVisibleRows(t *testing.T) {
	st := openR15(t)
	const date = "2026-09-04"
	fetched := at(t, "2026-09-04 15:30:00")
	putSession(t, st, date, provider.SessionDay, fetched,
		strikes("202609", fptr(10), fptr(20), fptr(100), fptr(200)))
	putHealth(t, st, date, provider.SessionDay, derivatives.StatusStale, fetched, 0)
	putSettlement(t, st, date, "20260828", "202608F4", fetched)

	res := mustLoad(t, st, query(date, provider.SessionDay, 1))
	obs := res.Observations[0]
	if obs.SourceStatus != institutional.StatusStale {
		t.Errorf("status = %s, want STALE", obs.SourceStatus)
	}
	if obs.Volume.Ratio.Observed || obs.OI.Ratio.Observed {
		t.Error("a ratio was computed from rows a STALE health record covers; using them " +
			"would launder a failed freshness contract into every change and percentile")
	}
	if res.ValidObservations != 0 {
		t.Errorf("valid observations = %d", res.ValidObservations)
	}
}

// TestAPartialHealthRecordBlocksTheAggregate — §6.2, through the reader.
func TestAPartialHealthRecordBlocksTheAggregate(t *testing.T) {
	st := openR15(t)
	const date = "2026-09-04"
	fetched := at(t, "2026-09-04 15:30:00")
	putSession(t, st, date, provider.SessionDay, fetched,
		strikes("202609", fptr(10), fptr(20), fptr(100), fptr(200)))
	putHealth(t, st, date, provider.SessionDay, derivatives.StatusPartial, fetched, 4)
	putSettlement(t, st, date, "20260828", "202608F4", fetched)

	obs := mustLoad(t, st, query(date, provider.SessionDay, 1)).Observations[0]
	if obs.Volume.Ratio.Observed {
		t.Fatal("a PCR was published from a PARTIAL snapshot")
	}
	if obs.Volume.Coverage.RejectedRows != 4 {
		t.Errorf("the rejected-row count did not reach the aggregate: %d",
			obs.Volume.Coverage.RejectedRows)
	}
}

// bulkFixture writes n consecutive dates, all imported at one instant, so that only the
// trading-date bound can separate them.
func bulkFixture(t *testing.T, n int) *store.Store {
	t.Helper()
	st := openR15(t)
	imported := at(t, "2026-06-01 08:00:00")
	for i := 1; i <= n; i++ {
		date := fmt.Sprintf("2026-06-%02d", i)
		putSession(t, st, date, provider.SessionDay, imported,
			strikes("202609", fptr(10), fptr(10), fptr(1000), fptr(float64(1000+i*100))))
		putHealth(t, st, date, provider.SessionDay, derivatives.StatusAvailable, imported, 0)
		putSettlement(t, st, date, "20260528", "202605F4", imported)
	}
	return st
}

// TestTheScanLimitKeepsTheMostRecentOptionDates is §10.7's contract for the bounded query this
// package adds.
//
// The history is LONGER than the scan limit, and that is the whole design of the test: a Go
// re-sort cannot recover a date the SQL LIMIT already truncated away, so ordering the SQL the
// other way produces a correctly-sorted list of the WRONG days — and is invisible whenever
// MaxScanDates exceeds the available history, which is the shape of every small fixture.
func TestTheScanLimitKeepsTheMostRecentOptionDates(t *testing.T) {
	st := bulkFixture(t, 20)

	q := query("2026-06-20", provider.SessionDay, 3)
	q.MaxScanDates = 5 // fewer than the 20 dates in the store

	res := mustLoad(t, st, q)
	if len(res.Observations) < 3 {
		t.Fatalf("got %d observations, want at least 3", len(res.Observations))
	}
	for i, want := range []string{"2026-06-20", "2026-06-19", "2026-06-18"} {
		if res.Observations[i].TradingDate != want {
			t.Errorf("observation %d is %s, want %s — the scan limit kept a set of dates "+
				"chosen by something other than the session, so the walk truncated away the "+
				"days it was asked for", i, res.Observations[i].TradingDate, want)
		}
	}
	// And the values, so a correctly-ordered list of the wrong days is caught by more than
	// its labels: day i's put OI is 1,000 + 100i against a fixed 1,000 call side.
	if v, ok := ratioOn(t, res, "2026-06-20", options.MetricOIPCR); !ok || v != 3.0 {
		t.Errorf("06-20 OI PCR = %v, want 3.0", v)
	}
}

// TestTheTwoAsOfBoundsAreNotTheSameBound: over a bulk import, a reading as of day 10 sees
// exactly ten sessions. Days 11-20 are in the table and share the import's fetched_at; what
// excludes them is that they had not happened.
func TestTheTwoAsOfBoundsAreNotTheSameBound(t *testing.T) {
	st := bulkFixture(t, 20)
	res := mustLoad(t, st, query("2026-06-10", provider.SessionDay, 60))

	if got := res.LatestTradingDate(); got != "2026-06-10" {
		t.Errorf("latest trading date = %s, want 2026-06-10", got)
	}
	if res.ValidObservations != 10 {
		t.Errorf("valid observations = %d, want 10", res.ValidObservations)
	}
	for _, d := range res.Dates {
		if d.TradingDate > "2026-06-10" {
			t.Errorf("%s is in a result read as of 2026-06-10", d.TradingDate)
		}
	}
}

// TestTheDatesComeBackNewestFirst, which is what stops a correction to an older session from
// presenting itself as the current one.
func TestTheDatesComeBackNewestFirst(t *testing.T) {
	res := mustLoad(t, bulkFixture(t, 20), query("2026-06-20", provider.SessionDay, 20))
	prev := ""
	for _, d := range res.Dates {
		if prev != "" && d.TradingDate >= prev {
			t.Fatalf("%s came after %s", d.TradingDate, prev)
		}
		prev = d.TradingDate
	}
}

// TestLoadViewIsOneCutoffForTheReadingAndItsHistory.
func TestLoadViewIsOneCutoffForTheReadingAndItsHistory(t *testing.T) {
	st := bulkFixture(t, 20)
	q := query("2026-06-20", provider.SessionDay, 20)
	p := institutional.DefaultPolicy()
	p.MinPercentileSample = 5

	view, res, err := optionshistory.LoadView(ctx(), st, q, options.MetricOIPCR, p)
	if err != nil {
		t.Fatal(err)
	}
	if view.TradingDate != "2026-06-20" {
		t.Errorf("the view is for %s", view.TradingDate)
	}
	if view.AsOf != "2026-06-20" {
		t.Errorf("the view's as-of is %s", view.AsOf)
	}
	if !view.PCR.Ratio.Observed {
		t.Fatalf("no ratio on the view: %s", view.PCR.Reason)
	}
	if v, _ := view.Percentile.Value(); v != 1 {
		t.Errorf("the highest reading of the series ranks %v", v)
	}
	if view.Distribution.Size() != 20 {
		t.Errorf("the distribution holds %d values, want 20", view.Distribution.Size())
	}
	hc, ok := view.Change(1)
	if !ok {
		t.Fatal("no CHANGE_1OBS")
	}
	if v, ok := hc.Change.Value(); !ok || v <= 0 {
		t.Errorf("CHANGE_1OBS = %v (%s); the series rises every day", v, hc.Status)
	}
	if res.ValidObservations != 20 {
		t.Errorf("valid observations = %d", res.ValidObservations)
	}
}

// TestAQueryWithoutASettlementFeedIsAllowedAndSaysSo — the open-interest half is MISSING
// rather than silently computed over a settled expiry.
func TestAQueryWithoutASettlementFeedIsAllowedAndSaysSo(t *testing.T) {
	st := bulkFixture(t, 3)
	q := query("2026-06-03", provider.SessionDay, 3)
	q.SettlementDataset, q.SettlementSource = "", ""

	res := mustLoad(t, st, q)
	obs := res.Observations[0]
	if obs.OI.Ratio.Observed {
		t.Fatal("an open-interest ratio was published with no settlement feed configured")
	}
	dr, _ := res.Resolution(obs.TradingDate)
	if dr.SettlementReason == "" {
		t.Error("no reason was recorded for the absent settlement read")
	}
}

// TestAHalfConfiguredSettlementFeedIsRejected: one of the two names is a typo, not a policy.
func TestAHalfConfiguredSettlementFeedIsRejected(t *testing.T) {
	q := query("2026-06-03", provider.SessionDay, 3)
	q.SettlementSource = ""
	if _, err := optionshistory.Load(ctx(), openR15(t), q); err == nil {
		t.Fatal("a query naming a settlement dataset with no source was accepted")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
