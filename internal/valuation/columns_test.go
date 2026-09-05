package valuation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A missing P/E COLUMN is a parse failure. It is not a market of unprofitable companies.
//
// This distinction did not exist before FU-11. A nil P/E used to mean only "no sample": the
// row was dropped from the distribution and nothing else read it. FU-11 made an absent P/E a
// FINANCIAL FACT — the exchanges withhold the ratio exactly when trailing earnings are
// non-positive — so every nil in the archive now says "this company was not profitable that
// session".
//
// Which means a renamed header manufactures that fact out of a schema change, and the archive
// keeps no record of which nils were real. One bad backfill month inside a good series reads
// as P P [renamed month] P P → INTERMITTENT → "the earnings denominator crossed zero", about a
// company that never did.

func fixtureProvider(t *testing.T, h http.Handler) *HistoricalProvider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p := NewHistoricalProvider(nil)
	p.Throttle, p.Retries, p.RateLimitBackoff = 0, 0, time.Millisecond
	p.twseURL, p.tpexURL = srv.URL+"/twse", srv.URL+"/tpex"
	return p
}

func serveJSON(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

// TWSE, one stock one month: a renamed P/E header must fail the month, not archive nils.
func TestTWSEMonthRefusesWhenThePEColumnIsUnresolvable(t *testing.T) {
	p := fixtureProvider(t, serveJSON(`{"stat":"OK","fields":["日期","PER","股價淨值比"],
		"data":[["115/08/03","26.5","3.1"],["115/08/04","26.8","3.2"]]}`))

	rows, err := p.TWSEMonth(context.Background(), "2330",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))

	if err == nil {
		t.Fatalf("a response with no 本益比 column returned %d rows and no error; every one "+
			"of them would archive a nil P/E, which now reads as a session on which the "+
			"company had no positive earnings", len(rows))
	}
	if len(rows) != 0 {
		t.Fatalf("%d rows returned alongside the error", len(rows))
	}
}

// TPEx, one session for the WHOLE MARKET: the same failure would write a false "no earnings"
// for every listed stock at once.
func TestTPEXSessionRefusesWhenThePEColumnIsUnresolvable(t *testing.T) {
	p := fixtureProvider(t, serveJSON(`{"tables":[{"date":"115/09/01",
		"fields":["股票代號","名稱","PER"],
		"data":[["6488","環球晶","33.4"],["1815","富喬","51.7"]],
		"totalCount":2}]}`))

	rows, err := p.TPEXSession(context.Background(),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))

	if err == nil {
		t.Fatalf("a whole-market response with no 本益比 column returned %d rows and no "+
			"error — a schema change would archive 'not profitable' for the entire market",
			len(rows))
	}
	if len(rows) != 0 {
		t.Fatalf("%d rows returned alongside the error", len(rows))
	}
}

// The converse, which must keep working: the column EXISTS and the exchange published no
// value in it. That nil is the financial fact, and it must be archived.
func TestAnEmptyPEValueIsStillArchivedAsAFact(t *testing.T) {
	p := fixtureProvider(t, serveJSON(`{"stat":"OK","fields":["日期","本益比","股價淨值比"],
		"data":[["115/08/03","-","3.1"],["115/08/04","26.8","3.2"]]}`))

	rows, err := p.TWSEMonth(context.Background(), "2330",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("a published dash is not a parse failure: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2 — both sessions were observed", len(rows))
	}
	if rows[0].PERatio != nil {
		t.Fatalf("a dash became %v; it means the exchange published no ratio", *rows[0].PERatio)
	}
	if rows[1].PERatio == nil || *rows[1].PERatio != 26.8 {
		t.Fatalf("the published ratio was lost: %+v", rows[1])
	}
}

// ── the live whole-market dataset ─────────────────────────────────────────────────────

// A row with no P/E KEY at all is skipped. A row whose P/E key is present but empty is kept,
// because that emptiness is the exchange's statement.
func TestLiveRowsWithNoPEKeyAreSkippedNotArchivedAsNil(t *testing.T) {
	got := parseRatioRows([]map[string]string{
		{"Code": "2330", "Date": "20260904", "PEratio": "27.88"},
		{"Code": "2337", "Date": "20260904", "PEratio": "-"},
		// A renamed field: the key this parser knows is simply absent.
		{"Code": "3055", "Date": "20260904", "PER": "12.3"},
	})

	if _, ok := got["3055"]; ok {
		t.Fatal("a row whose P/E column could not be found was archived with a nil P/E, " +
			"which reads as a company with no positive earnings — manufactured from a " +
			"renamed field")
	}
	if r, ok := got["2337"]; !ok || r.PERatio != nil {
		t.Fatalf("a PUBLISHED dash must still be archived as an absent ratio: %+v", got["2337"])
	}
	if r, ok := got["2330"]; !ok || r.PERatio == nil || *r.PERatio != 27.88 {
		t.Fatalf("the normal row was lost: %+v", got["2330"])
	}
}

// And the shape that would result: three sessions, one of them from a renamed month, must not
// read as a denominator crossing zero.
func TestARenamedColumnCannotManufactureAnEarningsTransition(t *testing.T) {
	var n int
	p := fixtureProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		if n == 2 {
			// The middle month, with the header renamed.
			_, _ = w.Write([]byte(`{"stat":"OK","fields":["日期","PER"],
				"data":[["115/09/01","26.5"]]}`))
			return
		}
		_, _ = w.Write([]byte(`{"stat":"OK","fields":["日期","本益比"],
			"data":[["115/08/03","26.5"]]}`))
	}))

	var all []Ratios
	for i := 0; i < 3; i++ {
		rows, err := p.TWSEMonth(context.Background(), "2330",
			time.Date(2026, time.Month(8+i), 1, 0, 0, 0, 0, time.UTC))
		if err != nil {
			continue // the refused month contributes nothing, which is the point
		}
		all = append(all, rows...)
	}
	if got := PEAvailabilityShape(eligibleSessions(all, "", 0)); got != PEPersistent {
		t.Fatalf("shape = %s — a renamed header in one month became evidence that the "+
			"earnings denominator crossed zero", got)
	}
}

// ── the provider-side construction the persistence shape depends on ───────────────────

// A stock absent from the whole-market response must be archived with NO Ratios — not with a
// row whose nil P/E would read as a session the exchange declined to rate.
//
// The suitability layer's "an acquisition gap is not a financial fact" guarantee rests on this
// and nothing else, and the shape-level tests cannot see it: they operate on row slices that
// have already been built.
func TestAStockAbsentFromTheDatasetIsArchivedWithNoRatios(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"Code": "2330", "Date": "20260904", "PEratio": "27.88"},
		})
	}))
	defer srv.Close()
	old := endpointOverride
	endpointOverride = map[string]string{twsePE: srv.URL, tpexPE: srv.URL}
	t.Cleanup(func() { endpointOverride = old })

	svc := NewService(nil, t.TempDir(), nil)
	snaps, err := svc.FetchAndStore(context.Background(), []string{"2330.TW", "9999.TW"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snaps {
		if s.Symbol != "9999.TW" {
			continue
		}
		if s.Ratios != nil {
			t.Fatalf("a stock absent from the dataset was archived with a Ratios row "+
				"(P/E %v). Under FU-11 that nil says the company had no positive earnings, "+
				"which nobody observed: %+v", s.Ratios.PERatio, s.Ratios)
		}
		if s.Status == Available {
			t.Fatalf("status = %s for a stock that is not in the dataset", s.Status)
		}
		return
	}
	t.Fatal("no snapshot was produced for the absent stock")
}

// A renamed CODE column must not look like a holiday.
//
// The asymmetry this closes: an unmatched 股票代號 header empties every row's code, every row
// is skipped, and the caller gets an empty map — which `cmd/valuation-backfill` reads as a
// non-trading day. A renamed column would report the whole backfill window as holidays: no
// error, no rows, and a summary in which the exchange simply appears never to have opened.
//
// Different false fact from the P/E case, same shape: a parse failure wearing the costume of
// an observation.
func TestTPEXSessionRefusesWhenTheCodeColumnIsUnresolvable(t *testing.T) {
	p := fixtureProvider(t, serveJSON(`{"tables":[{"date":"115/09/01",
		"fields":["SecCode","名稱","本益比"],
		"data":[["6488","環球晶","33.4"],["1815","富喬","51.7"]],
		"totalCount":2}]}`))

	rows, err := p.TPEXSession(context.Background(),
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))

	if err == nil {
		t.Fatalf("a response with no 股票代號 column returned %d rows and no error; the "+
			"caller cannot tell that from a holiday", len(rows))
	}
	if len(rows) != 0 {
		t.Fatalf("%d rows returned alongside the error", len(rows))
	}
}
