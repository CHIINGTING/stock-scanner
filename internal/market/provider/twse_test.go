package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Every test here is deterministic: fixtures are real captured TWSE responses committed
// under testdata/, and the HTTP path is exercised against httptest. `go test ./...` must
// never depend on TWSE being up — an exchange outage is not a reason for the suite to fail.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseBFI82U(t *testing.T) {
	cash, asOf, err := ParseBFI82U(fixture(t, "bfi82u_20260807.json"), "2026-08-07")
	if err != nil {
		t.Fatalf("ParseBFI82U: %v", err)
	}
	if asOf != "2026-08-07" {
		t.Errorf("asOf = %s", asOf)
	}
	// Values from the captured 2026-08-07 response.
	if cash.ForeignBuy != 310339947885 {
		t.Errorf("ForeignBuy = %v", cash.ForeignBuy)
	}
	if cash.ForeignSell != 351055691675 {
		t.Errorf("ForeignSell = %v", cash.ForeignSell)
	}
	if cash.ForeignNet != -40715743790 {
		t.Errorf("ForeignNet = %v", cash.ForeignNet)
	}
	// The official net is taken verbatim, but it should agree with buy-sell; a divergence
	// would mean the column layout moved.
	if got := cash.ForeignBuy - cash.ForeignSell; got != cash.ForeignNet {
		t.Errorf("buy-sell = %v but official net = %v", got, cash.ForeignNet)
	}
	// Other institutional categories are retained for later phases.
	for _, name := range []string{"投信", "自營商(自行買賣)", "自營商(避險)", "合計"} {
		if _, ok := cash.Others[name]; !ok {
			t.Errorf("category %q not retained", name)
		}
	}
	if cash.Others["合計"] == 0 {
		t.Error("合計 should be non-zero")
	}
}

func TestParseMIMargn(t *testing.T) {
	m, asOf, err := ParseMIMargn(fixture(t, "mi_margn_20260807.json"), "2026-08-07")
	if err != nil {
		t.Fatalf("ParseMIMargn: %v", err)
	}
	if asOf != "2026-08-07" {
		t.Errorf("asOf = %s", asOf)
	}
	if m.MarginPrevLots != 8958261 || m.MarginBalanceLots != 8986438 {
		t.Errorf("margin lots: prev=%v today=%v", m.MarginPrevLots, m.MarginBalanceLots)
	}
	if m.ShortPrevLots != 191897 || m.ShortBalanceLots != 192740 {
		t.Errorf("short lots: prev=%v today=%v", m.ShortPrevLots, m.ShortBalanceLots)
	}
	if m.MarginBalanceKTWD != 537664510 {
		t.Errorf("margin KTWD = %v", m.MarginBalanceKTWD)
	}
	// The daily change comes from the same response, not a second request.
	if got := m.MarginChangeLots(); got != 28177 {
		t.Errorf("MarginChangeLots = %v, want 28177", got)
	}
	if got := m.ShortChangeLots(); got != 843 {
		t.Errorf("ShortChangeLots = %v, want 843", got)
	}
}

func TestParseMIIndexBreadth(t *testing.T) {
	b, asOf, err := ParseMIIndexBreadth(fixture(t, "mi_index_20260807.json"), "2026-08-07")
	if err != nil {
		t.Fatalf("ParseMIIndexBreadth: %v", err)
	}
	if asOf != "2026-08-07" {
		t.Errorf("asOf = %s", asOf)
	}
	// The 股票 column, not 整體市場 — the latter reads 4,547/7,085 because it counts
	// warrants and ETFs, and any ratio built on it would be meaningless.
	if b.Advancing != 497 || b.LimitUp != 14 {
		t.Errorf("advancing = %d(%d), want 497(14)", b.Advancing, b.LimitUp)
	}
	if b.Declining != 509 || b.LimitDown != 3 {
		t.Errorf("declining = %d(%d), want 509(3)", b.Declining, b.LimitDown)
	}
	if b.Unchanged != 68 {
		t.Errorf("unchanged = %d, want 68", b.Unchanged)
	}
	if b.Advancing > 2000 {
		t.Errorf("advancing %d looks like the 整體市場 column", b.Advancing)
	}
}

// A holiday is not an outage. The response is well-formed with a non-OK stat, and it must
// map to ErrNoData so the caller records NO_DATA rather than retrying or degrading.
func TestHolidayIsNoDataNotFailure(t *testing.T) {
	_, _, err := ParseBFI82U(fixture(t, "bfi82u_holiday.json"), "2026-08-09")
	if err == nil {
		t.Fatal("a holiday response must not parse as success")
	}
	if !errors.Is(err, ErrNoData) {
		t.Errorf("holiday should be ErrNoData, got %v", err)
	}
}

// TWSE serves a neighbouring session for some inputs. Accepting that would date-shift the
// whole snapshot, so it must be refused rather than reconciled.
func TestDateMismatchIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parse func([]byte, string) error
		file  string
	}{
		{"BFI82U", func(b []byte, d string) error { _, _, e := ParseBFI82U(b, d); return e }, "bfi82u_20260807.json"},
		{"MI_MARGN", func(b []byte, d string) error { _, _, e := ParseMIMargn(b, d); return e }, "mi_margn_20260807.json"},
		{"MI_INDEX", func(b []byte, d string) error { _, _, e := ParseMIIndexBreadth(b, d); return e }, "mi_index_20260807.json"},
	} {
		err := tc.parse(fixture(t, tc.file), "2026-08-06") // fixture is 08-07
		if err == nil {
			t.Errorf("%s: a mismatched date must be refused", tc.name)
			continue
		}
		if !errors.Is(err, ErrDateMismatch) {
			t.Errorf("%s: want ErrDateMismatch, got %v", tc.name, err)
		}
	}
}

func TestParsersRejectGarbage(t *testing.T) {
	cases := [][]byte{
		[]byte("{not json"),
		[]byte(`{"stat":"OK","date":"20260807","fields":["a"],"data":[["只有一欄"]]}`),
		[]byte(`{"stat":"OK","date":"20260807","data":[]}`),
	}
	for i, b := range cases {
		if _, _, err := ParseBFI82U(b, "2026-08-07"); err == nil {
			t.Errorf("case %d: garbage parsed as success", i)
		}
	}
	// A well-formed response missing the foreign row is a layout change, not empty data.
	noForeign := []byte(`{"stat":"OK","date":"20260807","fields":["單位名稱","買進金額","賣出金額","買賣差額"],
	  "data":[["投信","1","2","-1"]]}`)
	if _, _, err := ParseBFI82U(noForeign, "2026-08-07"); err == nil {
		t.Error("a response without the foreign row must fail loudly")
	}
	// Same for the breadth table.
	noTable := []byte(`{"stat":"OK","date":"20260807","tables":[{"title":"別的表","fields":["x"],"data":[["1"]]}]}`)
	if _, _, err := ParseMIIndexBreadth(noTable, "2026-08-07"); err == nil {
		t.Error("a missing breadth table must fail loudly")
	}
}

func TestParseCountWithLimit(t *testing.T) {
	cases := []struct {
		in    string
		n, lu int
	}{
		{"497(14)", 497, 14},
		{"4,547(191)", 4547, 191},
		{"68", 68, 0},
		{" 509(3) ", 509, 3},
		{"", 0, 0},
	}
	for _, c := range cases {
		n, lu := parseCountWithLimit(c.in)
		if n != c.n || lu != c.lu {
			t.Errorf("%q → %d(%d), want %d(%d)", c.in, n, lu, c.n, c.lu)
		}
	}
}

func TestParseAmount(t *testing.T) {
	if v, err := parseAmount("310,339,947,885"); err != nil || v != 310339947885 {
		t.Errorf("thousands separator: %v %v", v, err)
	}
	if v, err := parseAmount(""); err != nil || v != 0 {
		t.Errorf("empty cell should be a clean zero: %v %v", v, err)
	}
	if v, err := parseAmount("-40,715,743,790"); err != nil || v != -40715743790 {
		t.Errorf("negative: %v %v", v, err)
	}
	// Unparsable text must be an error, not a silent zero — a layout change must not look
	// like a quiet market.
	if _, err := parseAmount("暫停交易"); err == nil {
		t.Error("unparsable text returned a silent zero")
	}
}

// ── HTTP path, against httptest rather than TWSE ─────────────────────────────────────

func twseStub(t *testing.T, status int, cash, margin, index []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		switch {
		case cash != nil && r.URL.Query().Get("dayDate") != "":
			w.Write(cash)
		case margin != nil && r.URL.Query().Get("selectType") == "MS":
			w.Write(margin)
		case index != nil:
			w.Write(index)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

func TestFetchAssemblesRawSnapshot(t *testing.T) {
	cash, margin, index := fixture(t, "bfi82u_20260807.json"), fixture(t, "mi_margn_20260807.json"), fixture(t, "mi_index_20260807.json")
	srv := twseStub(t, http.StatusOK, cash, margin, index)
	defer srv.Close()

	p := NewTWSEProvider(5 * time.Second)
	p.BaseURLs.BFI82U, p.BaseURLs.MIMargn, p.BaseURLs.MIIndex = srv.URL+"/cash", srv.URL+"/margin", srv.URL+"/index"

	raw, err := p.Fetch(context.Background(), "2026-08-07")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if raw.Cash == nil || raw.Margin == nil || raw.Breadth == nil {
		t.Fatalf("datasets missing: cash=%v margin=%v breadth=%v", raw.Cash != nil, raw.Margin != nil, raw.Breadth != nil)
	}
	if raw.PresentCount() != 3 {
		t.Errorf("PresentCount = %d, want 3", raw.PresentCount())
	}
	if len(raw.Sources) != 3 {
		t.Fatalf("want 3 provenance records, got %d", len(raw.Sources))
	}
	for _, s := range raw.Sources {
		if s.Status != model.SourceOK {
			t.Errorf("%s: status %s (%s)", s.Name, s.Status, s.Err)
		}
		if s.URL == "" || s.FetchedAt.IsZero() {
			t.Errorf("%s: provenance incomplete", s.Name)
		}
		if s.AsOfDate != "2026-08-07" {
			t.Errorf("%s: asOf %q", s.Name, s.AsOfDate)
		}
	}
}

// One dead endpoint must not take the others with it, and the failure must be recorded as
// missing rather than silently becoming a zero-valued dataset.
func TestFetchIsolatesPerSourceFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("selectType") == "MS" {
			w.WriteHeader(http.StatusInternalServerError) // margin is down
			return
		}
		if r.URL.Query().Get("dayDate") != "" {
			w.Write(fixture(t, "bfi82u_20260807.json"))
			return
		}
		w.Write(fixture(t, "mi_index_20260807.json"))
	}))
	defer srv.Close()

	p := NewTWSEProvider(5 * time.Second)
	p.BaseURLs.BFI82U, p.BaseURLs.MIMargn, p.BaseURLs.MIIndex = srv.URL+"/cash", srv.URL+"/margin", srv.URL+"/index"

	raw, err := p.Fetch(context.Background(), "2026-08-07")
	if err != nil {
		t.Fatalf("a single dead source must not fail the whole fetch: %v", err)
	}
	if raw.Margin != nil {
		t.Error("a failed source must leave its dataset nil, not zero-valued")
	}
	if raw.Cash == nil || raw.Breadth == nil {
		t.Error("the healthy sources should still have arrived")
	}
	var marginMeta *model.SourceMeta
	for i := range raw.Sources {
		if raw.Sources[i].Name == "TWSE/MI_MARGN" {
			marginMeta = &raw.Sources[i]
		}
	}
	if marginMeta == nil || marginMeta.Status != model.SourceError || marginMeta.Err == "" {
		t.Errorf("the failure must be recorded with a reason: %+v", marginMeta)
	}
}

func TestFetchRespectsContextAndBadDate(t *testing.T) {
	srv := twseStub(t, http.StatusOK, fixture(t, "bfi82u_20260807.json"), nil, nil)
	defer srv.Close()
	p := NewTWSEProvider(5 * time.Second)
	p.BaseURLs.BFI82U = srv.URL

	if _, err := p.Fetch(context.Background(), "07/08/2026"); err == nil {
		t.Error("a malformed date must be rejected before any request")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, err := p.Fetch(ctx, "2026-08-07")
	if err != nil {
		t.Fatalf("a cancelled context should degrade, not error out: %v", err)
	}
	if raw.Cash != nil {
		t.Error("a cancelled request must not produce data")
	}
	if raw.Sources[0].Status != model.SourceError {
		t.Errorf("cancellation should be recorded as an error source, got %s", raw.Sources[0].Status)
	}
}

func TestSkipBreadthIsRecorded(t *testing.T) {
	srv := twseStub(t, http.StatusOK, fixture(t, "bfi82u_20260807.json"), fixture(t, "mi_margn_20260807.json"), nil)
	defer srv.Close()
	p := NewTWSEProvider(5 * time.Second)
	p.SkipBreadth = true
	p.BaseURLs.BFI82U, p.BaseURLs.MIMargn = srv.URL+"/cash", srv.URL+"/margin"

	raw, err := p.Fetch(context.Background(), "2026-08-07")
	if err != nil {
		t.Fatal(err)
	}
	if raw.Breadth != nil {
		t.Error("SkipBreadth should leave breadth nil")
	}
	last := raw.Sources[len(raw.Sources)-1]
	if last.Status != model.SourceSkipped {
		t.Errorf("a skipped source must say so, got %s", last.Status)
	}
}
