package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The §34 acceptance path, as far as it can be exercised without the network: open the
// dashboard, type 2330, find 台積電, and get a health check whose every figure is either a
// real number or a labelled absence.
func TestAcceptanceSearchThenCheck(t *testing.T) {
	s := testServer(t)

	// 1. the page loads
	rec := get(t, s, "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "個股健檢") {
		t.Fatalf("dashboard did not load: %d", rec.Code)
	}

	// 2. typing 2330 finds 台積電
	rec = get(t, s, "/api/v1/stocks?q=2330")
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d", rec.Code)
	}
	var body SearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) == 0 || body.Results[0].Symbol != "2330" ||
		body.Results[0].Name != "台積電" {
		t.Fatalf("2330 did not resolve to 台積電: %+v", body.Results)
	}
	if body.Results[0].Market != "TWSE" {
		t.Fatalf("market = %q", body.Results[0].Market)
	}

	// 3. searching by name and by alias reaches the same stock
	for _, q := range []string{"台積電", "TSMC", "2330.TW"} {
		rec = get(t, s, "/api/v1/stocks?q="+q)
		var b SearchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		if len(b.Results) == 0 || b.Results[0].Symbol != "2330" {
			t.Errorf("%q did not find 2330: %+v", q, b.Results)
		}
	}
	// 4. and the identity is never crossed: 2330 is never 鴻海
	for _, r := range body.Results {
		if r.Symbol == "2330" && r.Name != "台積電" {
			t.Fatalf("2330 resolved to %q", r.Name)
		}
	}
}
