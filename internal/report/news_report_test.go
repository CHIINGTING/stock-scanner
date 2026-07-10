package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/news"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

func newsEntry() scanner.WatchlistEntry {
	e := scanner.WatchlistEntry{}
	e.A.Symbol = "2327"
	e.A.Name = "國巨"
	e.RocketScore = 55
	e.News = &news.StockNewsView{
		Computed: true,
		StockItems: []news.NewsSignal{{
			EventID:     "gooaye-ep-677",
			Signal:      news.SignalBullish,
			Strength:    9,
			PublishedAt: time.Now().Add(-24 * time.Hour),
			Summary:     "可放大資金的強勢族群",
			Sources:     []news.NewsSource{{Provider: "socialworkerdaily"}},
		}},
		SectorItems: []news.NewsSignal{{
			EventID:  "gooaye-ep-677",
			Signal:   news.SignalRotationOut,
			Strength: 6,
			Sectors:  []string{"被動元件"},
		}},
		Divergence: true,
	}
	return e
}

func newsSummary() *news.MarketNewsSummary {
	return &news.MarketNewsSummary{
		RotationIn:  []string{"銅箔基板", "封測"},
		Bullish:     []string{"被動元件"},
		RotationOut: []string{"光通訊"},
		RiskWarning: []string{"記憶體"},
		Fresh:       4, Active: 3, Expired: 2,
		AsOf: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
	}
}

// genHTMLNews renders with an explicit newsSummary + gv, returning the HTML.
func genHTMLNews(t *testing.T, entries []scanner.WatchlistEntry, gv GuardrailViewOptions, sum *news.MarketNewsSummary) string {
	t.Helper()
	dir := t.TempDir()
	r := New(Config{OutputDir: dir})
	date := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	market := []scanner.StockAnalysis{{Symbol: "2327", Name: "國巨", Close: 100}}
	if err := r.Generate(market, nil, entries, nil, "-", date, gv, sum); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report_20260605.html"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(b)
}

func TestNewsHiddenByDefault(t *testing.T) {
	html := genHTMLNews(t, []scanner.WatchlistEntry{newsEntry()}, GuardrailViewOptions{ShowNews: false}, newsSummary())
	if strings.Contains(html, "⑩ 消息面") {
		t.Error("news section must be hidden when ShowNews=false")
	}
	if strings.Contains(html, "市場消息面") {
		t.Error("news banner must be hidden when ShowNews=false")
	}
}

// TestNewsHiddenShadowIndependent is the core guarantee: with news data populated but
// ShowNews=false, output is byte-identical to output with no news data at all.
func TestNewsHiddenShadowIndependent(t *testing.T) {
	gv := GuardrailViewOptions{ShowNews: false}
	withNews := genHTMLNews(t, []scanner.WatchlistEntry{newsEntry()}, gv, newsSummary())

	plain := newsEntry()
	plain.News = nil
	withoutNews := genHTMLNews(t, []scanner.WatchlistEntry{plain}, gv, nil)

	if withNews != withoutNews {
		t.Error("ShowNews=false output must be identical whether or not News data is attached")
	}
}

func TestNewsShown(t *testing.T) {
	html := genHTMLNews(t, []scanner.WatchlistEntry{newsEntry()}, GuardrailViewOptions{ShowNews: true}, newsSummary())
	for _, want := range []string{
		"⑩ 消息面", "股癌EP677", "偏多", "資金輪動出場",
		"DIVERGENCE", "市場消息面",
		"資金輪動進場", "資金輪動流出", "風險警示", "銅箔基板",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected rendered news to contain %q", want)
		}
	}
}

// TestNewsNoForbiddenTokens ensures the news UI never emits trade-instruction tokens.
func TestNewsNoForbiddenTokens(t *testing.T) {
	off := genHTMLNews(t, []scanner.WatchlistEntry{newsEntry()}, GuardrailViewOptions{ShowNews: false}, newsSummary())
	on := genHTMLNews(t, []scanner.WatchlistEntry{newsEntry()}, GuardrailViewOptions{ShowNews: true}, newsSummary())

	// Tokens the news layer must never introduce. Count must not increase turning it on.
	for _, tok := range []string{"建議買入", "建議賣出", "可以買", "可以賣", "到價下單"} {
		if strings.Count(on, tok) != strings.Count(off, tok) {
			t.Errorf("news UI introduced forbidden token %q", tok)
		}
	}
}
