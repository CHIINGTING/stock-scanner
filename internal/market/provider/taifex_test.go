package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func TestParseTAIFEXDaily(t *testing.T) {
	fut, asOf, err := ParseTAIFEXDaily(fixture(t, "taifex_txf_20260807.json"), "2026-08-07")
	if err != nil {
		t.Fatalf("ParseTAIFEXDaily: %v", err)
	}
	if asOf != "2026-08-07" {
		t.Errorf("asOf = %s", asOf)
	}
	if fut.Contract != "臺股期貨" || fut.Item != "外資及陸資" {
		t.Errorf("wrong row selected: %s / %s", fut.Contract, fut.Item)
	}
	if fut.LongOI != 8321 || fut.ShortOI != 96232 || fut.NetOI != -87911 {
		t.Errorf("OI = long %v short %v net %v, want 8321/96232/-87911", fut.LongOI, fut.ShortOI, fut.NetOI)
	}
	// The official net column is used verbatim; it should agree with long-short, and a
	// divergence would mean the layout moved.
	if fut.LongOI-fut.ShortOI != fut.NetOI {
		t.Errorf("long-short = %v but official net = %v", fut.LongOI-fut.ShortOI, fut.NetOI)
	}
	// The other institutional categories are retained for later phases.
	for _, item := range []string{"自營商", "投信"} {
		if _, ok := fut.OtherItems[item]; !ok {
			t.Errorf("%s not retained", item)
		}
	}
}

// The JSON endpoint accepts no date parameter and always serves the latest session. Asking
// it for a past date must fail loudly rather than hand back today's data wearing yesterday's
// label — that check is the only thing standing between this limitation and a corrupted
// replay.
func TestTAIFEXDailyRefusesWrongDate(t *testing.T) {
	_, asOf, err := ParseTAIFEXDaily(fixture(t, "taifex_txf_20260807.json"), "2026-08-06")
	if err == nil {
		t.Fatal("a past date must be refused")
	}
	if !errors.Is(err, ErrDateMismatch) {
		t.Errorf("want ErrDateMismatch, got %v", err)
	}
	if asOf != "2026-08-07" {
		t.Errorf("the actual feed date should still be reported, got %q", asOf)
	}
}

func TestParseTAIFEXDailyRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{
		[]byte("{not json"),
		[]byte("[]"),
		[]byte(`[{"Date":"20260807","ContractCode":"小型臺指期貨","Item":"外資及陸資","OpenInterest(Net)":"1"}]`),
	} {
		if _, _, err := ParseTAIFEXDaily(b, "2026-08-07"); err == nil {
			t.Errorf("garbage parsed as success: %s", b)
		}
	}
}

// ── backfill (Big5) ──────────────────────────────────────────────────────────────────

func TestParseTAIFEXBackfillCSV(t *testing.T) {
	days, err := ParseTAIFEXBackfillCSV(fixture(t, "taifex_backfill_big5.csv"))
	if err != nil {
		t.Fatalf("ParseTAIFEXBackfillCSV: %v", err)
	}
	if len(days) != 3 {
		t.Fatalf("got %d days, want 3 (2026-08-05..07)", len(days))
	}
	for i := 1; i < len(days); i++ {
		if days[i-1].Date >= days[i].Date {
			t.Fatalf("not ascending: %s then %s", days[i-1].Date, days[i].Date)
		}
	}
	last := days[len(days)-1]
	if last.Date != "2026-08-07" {
		t.Errorf("last day = %s", last.Date)
	}
	if last.Futures.NetOI != -87911 {
		t.Errorf("net OI = %v, want -87911", last.Futures.NetOI)
	}
	if last.Futures.LongOI-last.Futures.ShortOI != last.Futures.NetOI {
		t.Error("long-short disagrees with the official net column")
	}
}

// The two sources must mean the same thing, or a backfilled history would sit on a different
// scale from the days collected live. Verified on the one date both fixtures cover.
func TestDailyAndBackfillAreSemanticallyEquivalent(t *testing.T) {
	daily, _, err := ParseTAIFEXDaily(fixture(t, "taifex_txf_20260807.json"), "2026-08-07")
	if err != nil {
		t.Fatal(err)
	}
	days, err := ParseTAIFEXBackfillCSV(fixture(t, "taifex_backfill_big5.csv"))
	if err != nil {
		t.Fatal(err)
	}
	var same *model.FuturesOIData
	for _, d := range days {
		if d.Date == "2026-08-07" {
			same = d.Futures
		}
	}
	if same == nil {
		t.Fatal("the backfill fixture does not cover 2026-08-07")
	}
	if daily.LongOI != same.LongOI || daily.ShortOI != same.ShortOI || daily.NetOI != same.NetOI {
		t.Errorf("UTF-8 JSON and Big5 CSV disagree:\n  daily    long=%v short=%v net=%v\n  backfill long=%v short=%v net=%v",
			daily.LongOI, daily.ShortOI, daily.NetOI, same.LongOI, same.ShortOI, same.NetOI)
	}
	if daily.Contract != same.Contract || daily.Item != same.Item {
		t.Errorf("row identity differs: %s/%s vs %s/%s", daily.Contract, daily.Item, same.Contract, same.Item)
	}
}

// The fixture must genuinely be Big5, otherwise this whole path is being tested against
// something it will never see in production.
func TestBackfillFixtureIsActuallyBig5(t *testing.T) {
	raw := fixture(t, "taifex_backfill_big5.csv")
	if utf8Valid(raw) {
		t.Fatal("the backfill fixture decodes as UTF-8 — it is not exercising the Big5 path")
	}
	text, err := decodeBig5(raw)
	if err != nil {
		t.Fatalf("decodeBig5: %v", err)
	}
	if !strings.Contains(text, "臺股期貨") || !strings.Contains(text, "外資及陸資") {
		t.Error("Big5 decode did not produce the expected Chinese labels")
	}
}

func utf8Valid(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			i++
		case c&0xE0 == 0xC0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case c&0xF0 == 0xE0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		default:
			return false
		}
	}
	return true
}

func TestBackfillRejectsGarbage(t *testing.T) {
	if _, err := ParseTAIFEXBackfillCSV([]byte("only a header line")); err == nil {
		t.Error("a header-only response must fail")
	}
	if _, err := ParseTAIFEXBackfillCSV(nil); err == nil {
		t.Error("empty input must fail")
	}
}

func TestBackfillRangeValidation(t *testing.T) {
	b := NewTAIFEXBackfill(time.Second)
	if _, err := b.Range(context.Background(), "bad", "2026-08-07"); err == nil {
		t.Error("a malformed from-date must be rejected before any request")
	}
	if _, err := b.Range(context.Background(), "2026-08-07", "2026-08-01"); err == nil {
		t.Error("a reversed range must be rejected")
	}
}

// ── HTTP paths against httptest ──────────────────────────────────────────────────────

func TestTAIFEXFetchAndIsolation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "taifex_txf_20260807.json"))
	}))
	defer srv.Close()

	p := NewTAIFEXProvider(5 * time.Second)
	p.BaseURL = srv.URL
	raw, err := p.Fetch(context.Background(), "2026-08-07")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if raw.Futures == nil || raw.Futures.NetOI != -87911 {
		t.Fatalf("futures missing or wrong: %+v", raw.Futures)
	}
	if len(raw.Sources) != 1 || raw.Sources[0].Status != model.SourceOK {
		t.Errorf("provenance wrong: %+v", raw.Sources)
	}

	// A dead endpoint leaves the dataset nil with a recorded reason — never a zero-valued
	// position, which would read as "foreigners are flat".
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dead.Close()
	p.BaseURL = dead.URL
	raw, err = p.Fetch(context.Background(), "2026-08-07")
	if err != nil {
		t.Fatalf("a dead source should degrade, not error: %v", err)
	}
	if raw.Futures != nil {
		t.Error("a failed fetch must leave the dataset nil")
	}
	if raw.Sources[0].Status != model.SourceError || raw.Sources[0].Err == "" {
		t.Errorf("failure not recorded: %+v", raw.Sources[0])
	}
}

func TestBackfillRangeOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("backfill must POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err == nil {
			if r.Form.Get("commodityId") != "TXF" {
				t.Errorf("commodityId = %q", r.Form.Get("commodityId"))
			}
			if r.Form.Get("queryStartDate") != "2026/08/05" {
				t.Errorf("queryStartDate = %q", r.Form.Get("queryStartDate"))
			}
		}
		w.Write(fixture(t, "taifex_backfill_big5.csv"))
	}))
	defer srv.Close()

	b := NewTAIFEXBackfill(5 * time.Second)
	b.BaseURL = srv.URL
	days, err := b.Range(context.Background(), "2026-08-05", "2026-08-07")
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(days) != 3 {
		t.Errorf("got %d days, want 3", len(days))
	}
}
