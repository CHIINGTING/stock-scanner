package segbacktest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testDataset(t *testing.T) *Dataset {
	t.Helper()
	dir := t.TempDir()
	fakeCache(t, dir, "2330", "TW", "台積電", rampBars(t, "2026-01-01", 90, 900), time.Now())
	ds, err := LoadDataset(LoadOptions{Dir: dir})
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	return ds
}

func TestRenderSelfContained(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, testDataset(t), RenderOptions{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	html := buf.String()

	// The page must carry its own engine, UI and data — no network at view time.
	for _, want := range []string{
		"var SBT",                   // engine.js inlined
		"function drawChart",        // panel.js inlined
		`id="sbt-data"`,             // dataset block
		"var DATA = JSON.parse",     // dataset wiring
		"區間策略回測面板",                  // title
		`id="runBtn"`, `id="chart"`, // the controls panel.js binds to
		`id="markSel"`, `id="ranking"`, `id="details"`, `id="closing"`,
		`id="symList"`, `id="from"`, `id="to"`, `id="costOn"`, `id="discount"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// Nothing may load or call out at view time: no external script/style/image, no
	// network API. (The word "CDN" appears in a source comment, so match on the
	// mechanisms rather than on prose.)
	for _, bad := range []string{
		"<script src", "<link ", "<img ", "@import",
		"src=\"http", "href=\"http", "url(http",
		"fetch(", "XMLHttpRequest", "WebSocket", "importScripts",
	} {
		if strings.Contains(html, bad) {
			t.Errorf("page reaches outside itself: found %q", bad)
		}
	}
}

// The panel replays rules; it must never present itself as an order router.
func TestRenderHasNoOrderLanguage(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, testDataset(t), RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, bad := range []string{"PLACE_ORDER", "AUTO_BUY", "下單 API"} {
		if strings.Contains(html, bad) {
			t.Errorf("page contains order-placement language %q", bad)
		}
	}
	if !strings.Contains(html, "不構成投資建議") {
		t.Error("page is missing the not-investment-advice disclaimer")
	}
}

func TestRenderDefaultsAppearInForm(t *testing.T) {
	var buf bytes.Buffer
	opts := RenderOptions{
		Capital: 500000, Reserve: 200000, AddAmount: 25000,
		AddCount: 12, AddDay: 10, Discount: 28, CostsOn: true,
		GeneratedAt: time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC),
	}
	if err := Render(&buf, testDataset(t), opts); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		`id="capital" min="0" step="10000" value="500000"`,
		`id="reserve" min="0" step="10000" value="200000"`,
		`id="addAmount" min="0" step="1000" value="25000"`,
		`id="addCount" min="0" step="1" value="12"`,
		`id="addDay" min="1" max="28" step="1" value="10"`,
		`id="discount" min="1" max="100" step="1" value="28"`,
		`id="costOn" checked`,
		"2026-07-31 09:00",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("form default missing: %q", want)
		}
	}
}

func TestRenderAppliesZeroValueDefaults(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, testDataset(t), RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	// Zero means "not set" for the money fields, so sane numbers must be filled in —
	// otherwise the page loads with a form that cannot run.
	for _, want := range []string{`value="100000"`, `value="10000"`, `value="6"`, `value="5"`, `value="100"`} {
		if !strings.Contains(html, want) {
			t.Errorf("expected fallback default %q in form", want)
		}
	}
	// Reserve legitimately defaults to 0 (逢跌狙擊 off unless asked for).
	if !strings.Contains(html, `id="reserve" min="0" step="10000" value="0"`) {
		t.Error("reserve should default to 0")
	}
	if strings.Contains(html, `id="costOn" checked`) {
		t.Error("costs should be unchecked when CostsOn is false")
	}
}

// A stock name is untrusted text from the price feed. It must not be able to close the
// <script> block that carries the dataset.
func TestRenderEscapesHostileStockName(t *testing.T) {
	ds := testDataset(t)
	ds.Stocks[0].Name = `</script><script>alert(1)</script>`
	var buf bytes.Buffer
	if err := Render(&buf, ds, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("hostile stock name broke out of the data block")
	}
	// It survives as escaped JSON, so the name still displays correctly.
	if !strings.Contains(html, `</script>`) {
		t.Error("expected the name to be JSON-escaped inside the data block")
	}
	// And the payload must still parse as JSON.
	start := strings.Index(html, `id="sbt-data" type="application/json">`)
	if start < 0 {
		t.Fatal("data block not found")
	}
	start += len(`id="sbt-data" type="application/json">`)
	end := strings.Index(html[start:], "</script>")
	if end < 0 {
		t.Fatal("data block not terminated")
	}
	var round Dataset
	if err := json.Unmarshal([]byte(html[start:start+end]), &round); err != nil {
		t.Fatalf("embedded dataset does not parse: %v", err)
	}
	if len(round.Stocks) != 1 || round.Stocks[0].Name != ds.Stocks[0].Name {
		t.Errorf("round-trip lost the name: %+v", round.Stocks)
	}
}

func TestRenderRejectsEmptyDataset(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, nil, RenderOptions{}); err == nil {
		t.Error("nil dataset should error")
	}
	if err := Render(&buf, &Dataset{}, RenderOptions{}); err == nil {
		t.Error("dataset with no stocks should error")
	}
}
