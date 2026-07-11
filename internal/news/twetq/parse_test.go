package twetq

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStockCodesFromSitemapFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "sitemap.xml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	codes := StockCodesFromSitemap(body)
	if len(codes) < 50 {
		t.Fatalf("expected many stock codes from sitemap, got %d", len(codes))
	}
	// 2327 (國巨) is a known covered stock.
	found := false
	for _, c := range codes {
		if c == "2327" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 2327 in covered stocks")
	}
}

func TestStockCodesFromSitemapMalformed(t *testing.T) {
	if got := StockCodesFromSitemap([]byte("<not-xml")); got != nil {
		t.Errorf("malformed XML should yield nil, got %v", got)
	}
}

func TestParseStockPageFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "stock_2327.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	code, name := ParseStockPage(string(body))
	if code != "2327" {
		t.Errorf("code = %q want 2327", code)
	}
	if name == "" {
		t.Errorf("expected a company name, got empty")
	}
}

func TestParsePodcastJSShellDegradesToEmpty(t *testing.T) {
	// The real /podcast page is a JS shell with no server-side episodes → empty (graceful).
	body, err := os.ReadFile(filepath.Join("testdata", "podcast.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if items := parsePodcast(body); len(items) != 0 {
		t.Errorf("JS-shell podcast should yield 0 items, got %d", len(items))
	}
}

func TestParsePodcastServerRendered(t *testing.T) {
	// If twetq ever server-renders episodes, the parser must pick them up + dedup.
	html := `<html><body><h2>股癌 EP677</h2><p>...</p><h2>EP 676</h2><p>EP677 again</p></body></html>`
	items := parsePodcast([]byte(html))
	if len(items) != 2 {
		t.Fatalf("expected 2 unique episodes, got %d", len(items))
	}
	if items[0].EpisodeID != "gooaye-ep-677" || items[1].EpisodeID != "gooaye-ep-676" {
		t.Errorf("unexpected episode ids: %q %q", items[0].EpisodeID, items[1].EpisodeID)
	}
}
