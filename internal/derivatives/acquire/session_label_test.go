package acquire

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/derivatives"
)

// §6, in the ERROR → NOT_PUBLISHED direction.
//
// A partition that is absent because the exchange has not published it yet and a partition
// that is absent because every row that could have been it was unassignable look identical at
// the point the status is chosen — and they imply opposite actions. NOT_PUBLISHED means "come
// back later"; a relabelled TradingSession never resolves by waiting, so reporting it that way
// leaves a layout change reading as a normal afternoon for as long as the label stays renamed.
//
// It is not a hypothetical failure mode either: §14 gates turning `derivatives.show` on behind
// "a week of AVAILABLE in the data-health panel", and a permanent NOT_PUBLISHED is a gate that
// never fires and never says why.

// relabelSession rewrites every row's TradingSession, which is what a feed-side rename looks
// like from here.
func relabelSession(t *testing.T, file, label string) []byte {
	t.Helper()
	raw, err := ReadFixture("", file)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		r["TradingSession"] = label
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestARelabelledSessionIsErrorNotNotPublished — every one of the 277 rows carries 早盤, so
// BOTH partitions are empty and nothing at all was stored. That must not read as two sessions
// the exchange has yet to release.
func TestARelabelledSessionIsErrorNotNotPublished(t *testing.T) {
	s := openR15(t)
	tr := &FixtureTransport{Bodies: map[string][]byte{
		OptionsByStrike.Name: relabelSession(t, "options_by_strike_20260904.json", "早盤"),
	}}
	r := runner(t, tr, s)
	r.Datasets = []Dataset{OptionsByStrike}
	res := mustRun(t, r, closed())

	for _, session := range []string{derivatives.SessionDay, derivatives.SessionAfterHours} {
		h := health(t, res, OptionsByStrike.Name, session)
		if h.Status != derivatives.StatusError {
			t.Fatalf("%s: want ERROR, got %s (%s) — a session label nobody recognises never "+
				"resolves by waiting, so NOT_PUBLISHED is an instruction to wait forever",
				session, h.Status, h.Reason)
		}
		// The reason has to name the count, or the panel says "ERROR" and the reader still
		// has to go read the response to find out that every row was thrown away.
		if !strings.Contains(h.Reason, "277") {
			t.Fatalf("%s: reason does not name the rejected count: %q", session, h.Reason)
		}
		if !strings.Contains(h.Reason, "TradingSession") {
			t.Fatalf("%s: reason does not name the column that failed: %q", session, h.Reason)
		}
		if h.RejectedRowCount != 277 {
			t.Fatalf("%s: rejected count = %d, want 277", session, h.RejectedRowCount)
		}
		if h.RowCount != 0 {
			t.Fatalf("%s: RowCount = %d, want 0", session, h.RowCount)
		}
	}
	if n := countRows(t, s, "derivative_snapshots"); n != 0 {
		t.Fatalf("%d snapshot(s) stored from a response no row of which could be placed", n)
	}
	if n := countRows(t, s, "options_oi_by_strike"); n != 0 {
		t.Fatalf("%d strike row(s) stored", n)
	}

	// And it survives the round trip, which is where the collapse would actually reach a
	// reader: the data-health panel reads the table, not the RunResult.
	for _, session := range []string{derivatives.SessionDay, derivatives.SessionAfterHours} {
		if got := storedHealth(t, s, OptionsByStrike.Name, session).Status; got != derivatives.StatusError {
			t.Fatalf("stored %s status: want ERROR, got %s", session, got)
		}
	}
}

// TestAnAbsentSessionBesideUnassignableRowsIsError is the discriminating case: the day rows
// parsed fine and ARE stored, while the night partition is absent from a response that also
// threw a row away.
//
// The thrown-away row cannot be attributed to a session — TradingSession is what decides which
// snapshot a row belongs to — so it may well have BEEN the night session, and calling the
// night session merely unpublished would present as "come back later" a session that was
// partly discarded. This is the same reasoning rejectionReason applies when it counts an
// unassignable row against every session of the response.
func TestAnAbsentSessionBesideUnassignableRowsIsError(t *testing.T) {
	day := sessionBody(t, "options_by_strike_20260904.json", "一般")
	var rows []map[string]string
	if err := json.Unmarshal(day, &rows); err != nil {
		t.Fatal(err)
	}
	// One row of the day session relabelled: 150 assignable 一般 rows, 1 unassignable, no
	// 盤後 partition at all.
	rows[0]["TradingSession"] = "早盤"
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}

	s := openR15(t)
	tr := &FixtureTransport{Bodies: map[string][]byte{OptionsByStrike.Name: body}}
	r := runner(t, tr, s)
	r.Datasets = []Dataset{OptionsByStrike}
	res := mustRun(t, r, closed())

	dayH := health(t, res, OptionsByStrike.Name, derivatives.SessionDay)
	if dayH.Status != derivatives.StatusPartial {
		t.Fatalf("DAY: want PARTIAL, got %s (%s) — the rows that DID assign are a real "+
			"observation and an absent OTHER session must not condemn them", dayH.Status, dayH.Reason)
	}
	if dayH.RowCount == 0 {
		t.Fatal("DAY stored no rows")
	}
	night := health(t, res, OptionsByStrike.Name, derivatives.SessionAfterHours)
	if night.Status != derivatives.StatusError {
		t.Fatalf("AFTER_HOURS: want ERROR, got %s (%s)", night.Status, night.Reason)
	}
	if !strings.Contains(night.Reason, "1 of") {
		t.Fatalf("AFTER_HOURS reason does not name the rejected count: %q", night.Reason)
	}
	if night.RejectedRowCount != 1 {
		t.Fatalf("AFTER_HOURS rejected count = %d, want 1", night.RejectedRowCount)
	}
}

// The converse, so the fix above cannot pass by calling every absent session an ERROR: a
// response that parsed cleanly and simply carried one session is STILL NOT_PUBLISHED. That is
// TestASessionAbsentFromTheResponseIsNotPublished in acquire_test.go, which is unchanged and
// must keep passing — the two tests are each other's mutation guard.
