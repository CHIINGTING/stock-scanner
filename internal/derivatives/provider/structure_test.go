package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func readMalformed(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "malformed", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// §6.2 whole-response rejection: nothing is stored, the status is ERROR, and no partial
// answer is offered. Undefined behaviour here is how a partial payload becomes an AVAILABLE
// number.
func TestStructuralFailuresRejectTheWholeResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    string
		parse   func([]byte) error
		because string
	}{
		{
			name:    "an HTML error page",
			file:    "html_error_page.html",
			because: "the body is not the expected content type",
		},
		{
			name:    "an empty body",
			file:    "empty.json",
			because: "there are no bytes to trust",
		},
		{
			name:    "an empty array",
			file:    "no_rows.json",
			because: "the payload is empty where the exchange should have returned rows",
		},
		{
			name:    "an undecodable envelope",
			file:    "undecodable.json",
			because: "the envelope does not decode",
		},
		{
			name: "a required column renamed away",
			file: "missing_openinterest_column.json",
			because: "a struct decode would fill the missing key with \"\", decode it to " +
				"ABSENT under §3.1, and publish an AVAILABLE response with a whole " +
				"dimension silently gone",
		},
		{
			name: "two trading dates in a single-session feed",
			file: "options_by_strike_mixed_dates.json",
			because: "the fetcher validates ONE date against the session it asked for, " +
				"and there is no correct answer to which one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := readMalformed(t, tc.file)
			got, err := ParseOptionsByStrike(body)
			if err == nil {
				t.Fatalf("accepted %s (%d rows) — %s", tc.file, len(got.Rows), tc.because)
			}
			if !errors.Is(err, ErrStructure) {
				t.Fatalf("error %v is not an ErrStructure — the caller cannot tell a "+
					"whole-response rejection from a bad row", err)
			}
			if got != nil {
				t.Fatal("a structurally rejected response must return NO rows")
			}
		})
	}
}

// Every field of every R15 endpoint is a JSON string today. The day one is not, that is a
// layout change and it says so, rather than being reformatted through a float.
func TestANumericJSONValueIsStructural(t *testing.T) {
	got, err := ParseFuturesDaily(readMalformed(t, "futures_daily_numeric_json.json"))
	if err == nil {
		t.Fatalf("accepted a JSON number where the feed sends strings: %+v", got)
	}
	if !errors.Is(err, ErrStructure) {
		t.Fatalf("error %v is not an ErrStructure", err)
	}
}

// §6.2 PARTIAL: the failure is per-ROW and the row's identity is intact, so valid rows are
// stored and rejected rows are COUNTED.
//
// The distinction this pins is the one the spec spends a paragraph on: a row rejected for a
// malformed value is NOT the same as a row carrying "-". The second is an observation of
// absence and is stored normally.
func TestAMalformedValueRejectsTheRowRatherThanBecomingAbsent(t *testing.T) {
	got, err := ParseOptionsByStrike(readMalformed(t, "options_by_strike_bad_rows.json"))
	if err != nil {
		t.Fatalf("a per-row failure must not reject the response: %v", err)
	}
	if got.Report.Status() != "PARTIAL" {
		t.Fatalf("status %s, want PARTIAL", got.Report.Status())
	}
	if got.Report.RowsSeen != 5 || got.Report.RowsAccepted != 2 || len(got.Report.Rejected) != 3 {
		t.Fatalf("seen %d accepted %d rejected %d, want 5/2/3 — rejected rows must be COUNTED, "+
			"because an aggregate whose correctness depends on completeness may not be "+
			"published from a PARTIAL snapshot",
			got.Report.RowsSeen, got.Report.RowsAccepted, len(got.Report.Rejected))
	}

	rejected := map[string]RejectedRow{}
	for _, r := range got.Report.Rejected {
		rejected[r.Field] = r
	}
	// StrikePrice is REAL NOT NULL and part of the UNIQUE key: a row whose strike will not
	// parse has no identity, so there is nowhere to put an absence and the row cannot be
	// stored. This is the ONE numeric column where §3.1's ABSENT does not apply.
	if r, ok := rejected["StrikePrice"]; !ok || r.Token != "四萬六" {
		t.Errorf("a non-numeric StrikePrice must reject the row, got %v", got.Report.Rejected)
	}
	if _, ok := rejected["CallPut"]; !ok {
		t.Error("an unrecognised CallPut must reject the row — a mis-mapped side inverts a " +
			"PCR without changing its magnitude, which no reconciliation can see")
	}
	if _, ok := rejected["TradingSession"]; !ok {
		t.Error("an unrecognised TradingSession must reject the row — the session decides " +
			"which snapshot the row is stored under")
	}

	// The row with Volume "n/a" is STORED, with Volume absent, because Volume is nullable.
	// It is also the row that makes a new sentinel visible.
	var stored *OptionStrikeRow
	for i := range got.Rows {
		if got.Rows[i].Strike == 46200 {
			stored = &got.Rows[i]
		}
	}
	if stored == nil {
		t.Fatal("the row carrying an unknown Volume token must still be stored: §3.1 makes " +
			"the VALUE absent, it does not reject the row")
	}
	if stored.Volume != nil {
		t.Errorf("Volume %v — an unparsable token is ABSENT, not zero and not an error", *stored.Volume)
	}
	if stored.OI != nil {
		t.Errorf(`OpenInterest "-" decoded to %v, want ABSENT`, *stored.OI)
	}
	if stored.Settlement != nil {
		t.Errorf(`SettlementPrice "NULL" decoded to %v, want ABSENT`, *stored.Settlement)
	}
	if len(got.Report.UnknownTokens) != 1 ||
		got.Report.UnknownTokens[0].Field != "Volume" ||
		got.Report.UnknownTokens[0].Token != "n/a" {
		t.Errorf("unknown-token census %v, want exactly Volume=\"n/a\" — the KNOWN absences "+
			"(-, NULL, \"\") must stay silent or the one that matters is buried under "+
			"100,000 that do not",
			got.Report.UnknownTokens)
	}

	// The good row: "1,234" survives the thousands separator, "0" is an observed zero.
	var good *OptionStrikeRow
	for i := range got.Rows {
		if got.Rows[i].Strike == 46000 {
			good = &got.Rows[i]
		}
	}
	if good == nil || good.Volume == nil || *good.Volume != 1234 {
		t.Fatalf(`"1,234" must decode to 1234, got %+v`, good)
	}
	if good.OI == nil || *good.OI != 0 {
		t.Fatalf(`"0" must be an OBSERVED ZERO, got %+v`, good.OI)
	}
}
