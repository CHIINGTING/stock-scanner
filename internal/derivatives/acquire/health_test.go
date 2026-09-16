package acquire

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/derivatives"
)

// The status vocabulary, tested where it is consumed.
//
// §6 separates eight statuses because collapsing any pair of them destroys an instruction the
// reader needs. The collapse never happens in the enum — it happens at the two places a status
// stops being a status: the mapping onto the shared panel vocabulary, and the line a reader
// actually sees.

// TestTheFourDistinctStatusesNeverCollapse.
//
// MISSING, NOT_PUBLISHED, NO_SESSION and STALE each carry a claim whose nearest neighbour
// implies the opposite action, so none of them may reach a reader as AVAILABLE, as a number,
// or as evidence.
func TestTheFourDistinctStatusesNeverCollapse(t *testing.T) {
	for _, s := range MustExplain {
		t.Run(string(s), func(t *testing.T) {
			if mapped, ok := s.MapToHealthcheck(); ok && mapped == "AVAILABLE" {
				t.Fatalf("%s maps onto AVAILABLE — a reader would act on it as an observation", s)
			}
			if mapped, ok := s.MapToHealthcheck(); ok && mapped == "PARTIAL" {
				t.Fatalf("%s maps onto PARTIAL", s)
			}
			if s.Numeric() {
				t.Fatalf("%s renders as a number; R14 spent a milestone on INSUFFICIENT_DATA "+
					"rendered as 0", s)
			}
			if s.Usable() {
				t.Fatalf("%s reads as usable evidence", s)
			}
			r := Record{
				Dataset: "options_by_strike", TradingDate: FixtureDate,
				Session: derivatives.SessionAfterHours, Status: s,
				Reason: "the exchange has not released it yet",
			}
			line := r.Explain()
			if !strings.Contains(line, string(s)) {
				t.Fatalf("Explain() dropped the status: %q", line)
			}
			if strings.Contains(line, string(derivatives.StatusAvailable)) {
				t.Fatalf("Explain() rendered %s as AVAILABLE: %q", s, line)
			}
			if strings.TrimSpace(line) == "" {
				t.Fatalf("%s renders as nothing at all, which reads as fine", s)
			}
		})
	}
}

// TestOnlyObservationsAreUsable pins the other side of the same line, so a change that made
// everything unusable would fail too.
func TestOnlyObservationsAreUsable(t *testing.T) {
	for _, s := range Statuses {
		want := s == derivatives.StatusAvailable || s == derivatives.StatusPartial
		if s.Usable() != want {
			t.Fatalf("%s: Usable()=%v, want %v", s, s.Usable(), want)
		}
		if s.Numeric() != want {
			t.Fatalf("%s: Numeric()=%v, want %v", s, s.Numeric(), want)
		}
		if !s.Valid() {
			t.Fatalf("%s is in Statuses but not Valid()", s)
		}
	}
	if len(Statuses) != 8 {
		t.Fatalf("§6 names eight statuses, this build has %d — a new one must be classified "+
			"here, not left to default", len(Statuses))
	}
}

// TestAnUnexplainedDistinctStatusIsRefused — a NOT_PUBLISHED with no reason reads as any other
// quiet row, which is already halfway to the collapse §6 forbids.
func TestAnUnexplainedDistinctStatusIsRefused(t *testing.T) {
	for _, s := range MustExplain {
		r := Record{
			Dataset: "margin", TradingDate: FixtureDate,
			Session: derivatives.SessionCombined, Status: s,
		}
		if err := r.Validate(); err == nil {
			t.Fatalf("%s was accepted with no reason", s)
		}
		r.Reason = "because"
		if err := r.Validate(); err != nil {
			t.Fatalf("%s with a reason was refused: %v", s, err)
		}
	}
}

// TestTheZeroStatusIsNotAValidRecord — a code path that forgot to set a status must fail at
// the write rather than publish a default.
func TestTheZeroStatusIsNotAValidRecord(t *testing.T) {
	r := NewRecord(Margin, FixtureDate, derivatives.SessionCombined)
	if err := r.Validate(); err == nil {
		t.Fatal("a record with no status was accepted")
	}
}

func TestRecordValidationRejectsMalformedKeys(t *testing.T) {
	base := func() Record {
		r := NewRecord(OptionsByStrike, FixtureDate, derivatives.SessionDay)
		r.Status = derivatives.StatusAvailable
		return r
	}
	cases := []struct {
		name  string
		mutbr func(*Record)
	}{
		{"no dataset", func(r *Record) { r.Dataset = "" }},
		{"no trading date", func(r *Record) { r.TradingDate = "" }},
		{"no session", func(r *Record) { r.Session = "" }},
		{"a session outside the vocabulary", func(r *Record) { r.Session = "NIGHT" }},
		{"a status outside the vocabulary", func(r *Record) { r.Status = "OK" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base()
			tc.mutbr(&r)
			if err := r.Validate(); err == nil {
				t.Fatalf("accepted: %+v", r)
			}
		})
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("the unmutated record was refused: %v", err)
	}
}

// TestAsOfIsTheTradingDateEvenForMargin — §3.1's presentation note. Margin's own effective
// date changes only when margins change, so showing it as as_of would light up the STALE rule
// on a figure that is current and simply has not moved.
func TestAsOfIsTheTradingDateEvenForMargin(t *testing.T) {
	r := NewRecord(Margin, FixtureDate, derivatives.SessionCombined)
	r.Status = derivatives.StatusAvailable
	if got := r.DataHealth().AsOf; got != FixtureDate {
		t.Fatalf("as_of %q, want the trading date %q", got, FixtureDate)
	}
}

// TestNewRecordCarriesWhatIsKnownBeforeTheRequest — the expected availability is what makes
// NOT_PUBLISHED actionable ("come back after 15:00" rather than "come back"), and it is
// carried on the record rather than looked up later, so the record stays readable after the
// catalogue moves.
func TestNewRecordCarriesWhatIsKnownBeforeTheRequest(t *testing.T) {
	r := NewRecord(InstitutionalFutures, FixtureDate, derivatives.SessionCombined)
	if r.Dataset != InstitutionalFutures.Name {
		t.Fatalf("dataset %q", r.Dataset)
	}
	if r.Source != InstitutionalFutures.Endpoint {
		t.Fatalf("source %q — the endpoint the bytes came from is stored verbatim", r.Source)
	}
	if r.ExpectedAvailability == "" {
		t.Fatal("no expected availability")
	}
	r.Status = derivatives.StatusNotPublished
	r.Reason = "not out yet"
	if !strings.Contains(r.Explain(), r.ExpectedAvailability) {
		t.Fatalf("a NOT_PUBLISHED line must say when to come back: %q", r.Explain())
	}
}

// TestHealthRoundTripsThroughTheDatabase — every field survives, because a panel reads the
// stored row and not the in-memory one.
func TestHealthRoundTripsThroughTheDatabase(t *testing.T) {
	s := openR15(t)
	ctx := context.Background()
	fetched := time.Date(2026, 9, 4, 7, 30, 0, 0, time.UTC)

	want := Record{
		Dataset: OptionsByStrike.Name, TradingDate: FixtureDate,
		Session: derivatives.SessionAfterHours, Source: OptionsByStrike.Endpoint,
		Status: derivatives.StatusPartial, ExpectedAvailability: "after the night session settles",
		FetchedAt: fetched, RowCount: 126, RejectedRowCount: 2,
		Reason: "2 row(s) rejected", RecordedAt: fetched,
	}
	if err := PutRecords(ctx, s, want); err != nil {
		t.Fatal(err)
	}
	got, err := HealthFor(ctx, s, FixtureDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
	g := got[0]
	if g.Dataset != want.Dataset || g.Session != want.Session || g.Source != want.Source ||
		g.Status != want.Status || g.RowCount != want.RowCount ||
		g.RejectedRowCount != want.RejectedRowCount || g.Reason != want.Reason ||
		g.ExpectedAvailability != want.ExpectedAvailability {
		t.Fatalf("round trip lost something:\n got %+v\nwant %+v", g, want)
	}
	if !g.FetchedAt.Equal(fetched) {
		t.Fatalf("fetched_at %v, want %v", g.FetchedAt, fetched)
	}

	// A later run for the same key REPLACES: a health record is the current answer to "what
	// happened when we last tried", not an observation. The evidence of the earlier attempt
	// survives in derivative_snapshots, which is where evidence belongs.
	later := want
	later.Status = derivatives.StatusAvailable
	later.RejectedRowCount = 0
	later.Reason = ""
	if err := PutRecords(ctx, s, later); err != nil {
		t.Fatal(err)
	}
	got, err = HealthFor(ctx, s, FixtureDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the second attempt appended instead of replacing: %d records", len(got))
	}
	if got[0].Status != derivatives.StatusAvailable {
		t.Fatalf("status %s after the second attempt", got[0].Status)
	}
}

// TestNothingIsWrittenWhenOneRecordIsInvalid — a run must not leave half its health behind and
// abort on the record that would have explained why.
func TestNothingIsWrittenWhenOneRecordIsInvalid(t *testing.T) {
	s := openR15(t)
	good := NewRecord(Margin, FixtureDate, derivatives.SessionCombined)
	good.Status = derivatives.StatusAvailable
	good.RowCount = 31
	bad := NewRecord(OptionsByStrike, FixtureDate, derivatives.SessionDay)
	bad.Status = derivatives.StatusMissing // no reason

	if err := PutRecords(context.Background(), s, good, bad); err == nil {
		t.Fatal("an invalid record was accepted")
	}
	if n := countRows(t, s, "derivative_data_health"); n != 0 {
		t.Fatalf("%d record(s) written despite the refusal", n)
	}
}
