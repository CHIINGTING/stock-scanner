package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/stockcatalog"
)

const testOverlay = `
stocks:
  - symbol: "2330.TW"
    name: "台積電"
    industry: "半導體"
    aliases: [TSMC, 台積]
    tags: [ai]
  - symbol: "2317.TW"
    name: "鴻海"
    aliases: [Foxconn]
  - symbol: "6488.TWO"
    name: "環球晶"
  - symbol: "9999"
    name: "無市場股"
  - symbol: "1234"
    name: "停用股"
    enabled: false
`

func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(p, []byte(testOverlay), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := stockcatalog.Load(stockcatalog.Sources{
		NamesFile: "-", SectorsFile: "-", AliasesFile: "-", OverlayFile: p,
	})
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return &Server{Catalog: c}
}

func get(t *testing.T, s *Server, url string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func decodeSearch(t *testing.T, rec *httptest.ResponseRecorder) SearchResponse {
	t.Helper()
	var out SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestSearchBySymbol(t *testing.T) {
	s := testServer(t)
	rec := get(t, s, "/api/v1/stocks?q=2330")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	out := decodeSearch(t, rec)
	if out.Count != 1 || out.Results[0].Symbol != "2330" || out.Results[0].Name != "台積電" {
		t.Fatalf("out = %+v", out)
	}
	r := out.Results[0]
	if r.Market != "TWSE" || r.MarketCode != "TW" || r.MarketSource != "CATALOG" {
		t.Fatalf("market fields = %+v", r)
	}
	if r.Match != string(stockcatalog.MatchSymbolExact) {
		t.Fatalf("match = %q", r.Match)
	}
}

func TestSearchByNameAndAlias(t *testing.T) {
	s := testServer(t)
	for _, q := range []string{"台積電", "台積", "TSMC", "tsmc"} {
		out := decodeSearch(t, get(t, s, "/api/v1/stocks?q="+q))
		if out.Count != 1 || out.Results[0].Symbol != "2330" {
			t.Fatalf("q=%q → %+v", q, out)
		}
	}
}

func TestSearchPartialSymbolIsOrdered(t *testing.T) {
	s := testServer(t)
	out := decodeSearch(t, get(t, s, "/api/v1/stocks?q=23"))
	if out.Count != 2 {
		t.Fatalf("out = %+v", out)
	}
	if out.Results[0].Symbol != "2317" || out.Results[1].Symbol != "2330" {
		t.Fatalf("order = %s,%s", out.Results[0].Symbol, out.Results[1].Symbol)
	}
}

// The same query must produce byte-identical bodies. The dashboard's first row is the one a
// hurried human clicks, and it must not depend on map iteration.
func TestSearchIsByteIdenticalAcrossRuns(t *testing.T) {
	s := testServer(t)
	first := get(t, s, "/api/v1/stocks?q=2").Body.String()
	for i := 0; i < 25; i++ {
		if got := get(t, s, "/api/v1/stocks?q=2").Body.String(); got != first {
			t.Fatalf("run %d differs:\n%s\n%s", i, got, first)
		}
	}
}

// A blank search box is not an error. It is the state every page load starts in.
func TestEmptyQueryIsAnEmptyOKResult(t *testing.T) {
	s := testServer(t)
	for _, url := range []string{"/api/v1/stocks", "/api/v1/stocks?q=", "/api/v1/stocks?q=%20%20"} {
		rec := get(t, s, url)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", url, rec.Code)
		}
		out := decodeSearch(t, rec)
		if out.Count != 0 || len(out.Results) != 0 {
			t.Fatalf("%s → %+v", url, out)
		}
		// [] not null: a client should not have to handle two shapes of "nothing".
		if !strings.Contains(rec.Body.String(), `"results":[]`) {
			t.Fatalf("%s body = %s, want an empty array", url, rec.Body.String())
		}
	}
}

func TestQueryIsTrimmed(t *testing.T) {
	s := testServer(t)
	out := decodeSearch(t, get(t, s, "/api/v1/stocks?q=%20%202330%20"))
	if out.Count != 1 || out.Results[0].Symbol != "2330" {
		t.Fatalf("out = %+v", out)
	}
	if out.Query != "2330" {
		t.Fatalf("echoed query = %q, want it trimmed", out.Query)
	}
}

func TestDisabledStockIsExcludedUnlessAsked(t *testing.T) {
	s := testServer(t)
	if out := decodeSearch(t, get(t, s, "/api/v1/stocks?q=停用")); out.Count != 0 {
		t.Fatalf("disabled stock leaked: %+v", out)
	}
	out := decodeSearch(t, get(t, s, "/api/v1/stocks?q=停用&include_disabled=true"))
	if out.Count != 1 || out.Results[0].Symbol != "1234" {
		t.Fatalf("out = %+v", out)
	}
}

func TestUnknownMarketIsReportedAsUnknown(t *testing.T) {
	s := testServer(t)
	out := decodeSearch(t, get(t, s, "/api/v1/stocks?q=9999"))
	if out.Count != 1 {
		t.Fatalf("out = %+v", out)
	}
	r := out.Results[0]
	if r.Market != "UNKNOWN" || r.MarketCode != "" || r.MarketSource != "UNKNOWN" {
		t.Fatalf("market fields = %+v", r)
	}
}

func TestLimit(t *testing.T) {
	s := testServer(t)
	if out := decodeSearch(t, get(t, s, "/api/v1/stocks?q=2&limit=1")); out.Count != 1 {
		t.Fatalf("out = %+v", out)
	}
}

// A malformed parameter is a client bug. Answering it with defaults hides that bug forever.
func TestBadParametersAre400(t *testing.T) {
	s := testServer(t)
	for _, url := range []string{
		"/api/v1/stocks?q=2&limit=abc",
		"/api/v1/stocks?q=2&limit=0",
		"/api/v1/stocks?q=2&limit=99999",
		"/api/v1/stocks?q=2&include_disabled=maybe",
	} {
		rec := get(t, s, url)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", url, rec.Code)
		}
		var env errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != ErrBadRequest {
			t.Fatalf("%s body = %s", url, rec.Body.String())
		}
	}
}

// The response must not leak catalog bookkeeping to a browser.
func TestResponseCarriesNoInternalConfig(t *testing.T) {
	s := testServer(t)
	body := get(t, s, "/api/v1/stocks?q=2330").Body.String()
	for _, forbidden := range []string{"NameSource", "name_source", "Enabled", "enabled", "Sectors", "sectors"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

func TestPostIsNotAllowed(t *testing.T) {
	s := testServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/stocks", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("POST must not be served by a read-only search endpoint")
	}
}

func TestHealthz(t *testing.T) {
	s := testServer(t)
	rec := get(t, s, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "ok" {
		t.Fatalf("out = %+v", out)
	}
}

// ── review follow-ups (R14-M2) ────────────────────────────────────────────────────────

// "The catalog is not loaded" and "your search matched nothing" are different answers, and
// every endpoint must give the same one. A 200 with [] here would be a silent lie.
func TestNilCatalogIs503(t *testing.T) {
	s := &Server{}
	for _, url := range []string{"/api/v1/stocks?q=2330", "/api/v1/stocks", "/healthz"} {
		rec := get(t, s, url)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", url, rec.Code)
		}
		var env errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s body = %s", url, rec.Body.String())
		}
		if env.Error.Code != ErrUnavailable {
			t.Fatalf("%s code = %q, want %s", url, env.Error.Code, ErrUnavailable)
		}
	}
}

// A malformed parameter is still a client bug even when the service is down; the 400 is the
// more actionable answer and must win.
func TestBadParameterBeatsUnavailable(t *testing.T) {
	s := &Server{}
	if rec := get(t, s, "/api/v1/stocks?q=2330&limit=abc"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// A server with no health service says so, and says it about the SERVICE.
//
// The route is registered either way. Left unregistered it would fall through to the "/"
// catch-all and answer STOCK_NOT_FOUND, blaming the symbol for a service that was never
// configured — a 404 that sends the reader looking for a typo in a ticker that is fine.
func TestHealthRouteWithoutAServiceIs503(t *testing.T) {
	s := testServer(t) // no Health
	rec := get(t, s, "/api/v1/stocks/2330/health")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), ErrUnavailable) {
		t.Fatalf("body = %s, want %s", rec.Body.String(), ErrUnavailable)
	}
	if strings.Contains(rec.Body.String(), ErrNotFound) {
		t.Fatalf("a missing service was reported as a missing stock: %s", rec.Body.String())
	}
}

// The search pattern must not swallow neighbouring paths.
func TestSearchPatternDoesNotSwallowOtherPaths(t *testing.T) {
	s := testServer(t)
	for _, url := range []string{"/api/v1/stocks/2330", "/api/v1/stocks/"} {
		if rec := get(t, s, url); rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", url, rec.Code)
		}
	}
}

// The echoed query is user input. It must reach a browser with < > & already escaped, so a
// page that ever interpolates it into markup cannot be turned into a script tag.
func TestEchoedQueryIsHTMLEscaped(t *testing.T) {
	s := testServer(t)
	rec := get(t, s, "/api/v1/stocks?q=%3Cscript%3Ealert(1)%3C%2Fscript%3E")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("unescaped markup in body: %s", rec.Body.String())
	}
	// It must still round-trip to the original string for a JSON client.
	out := decodeSearch(t, rec)
	if out.Query != "<script>alert(1)</script>" {
		t.Fatalf("query = %q", out.Query)
	}
}
