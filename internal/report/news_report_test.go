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
	cell := func(d, p, k, n int, net, disp string) news.SectorWindowCell {
		return news.SectorWindowCell{Days: d, Pos: p, Risk: k, Neg: n, Net: net, Display: disp}
	}
	return &news.MarketNewsSummary{
		Windows: []int{1, 3, 7},
		Sectors: []news.SectorNewsRow{
			{Name: "被動元件", LatestType: news.SignalRiskWarning, LatestEP: "EP677", Cells: []news.SectorWindowCell{
				cell(1, 1, 0, 0, "偏多", "🟢偏多"), cell(3, 1, 1, 0, "偏多", "🟢🟡偏多"), cell(7, 3, 2, 1, "偏多轉風險", "🟢×3 🟡×2 🔴×1")}},
			{Name: "CCL 銅箔基板", LatestType: news.SignalBullish, LatestEP: "EP677", Cells: []news.SectorWindowCell{
				cell(1, 0, 0, 0, "", "－"), cell(3, 2, 0, 0, "偏多", "🟢偏多"), cell(7, 2, 0, 0, "偏多", "🟢×2")}},
			{Name: "記憶體", LatestType: news.SignalBearish, LatestEP: "EP678", Cells: []news.SectorWindowCell{
				cell(1, 0, 1, 0, "風險", "🟡風險"), cell(3, 0, 1, 1, "分歧", "🟡🔴分歧"), cell(7, 0, 1, 2, "偏空", "🟡×1 🔴×2")}},
		},
		Fresh: 2, Active: 18, Expired: 11,
		AsOf: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
	}
}

// genHTMLNews renders with an explicit newsSummary + gv, returning the HTML.
func genHTMLNews(t *testing.T, entries []scanner.WatchlistEntry, gv GuardrailViewOptions, sum *news.MarketNewsSummary) string {
	return genHTMLNewsEp(t, entries, gv, sum, nil)
}

// genHTMLNewsEp additionally passes episode cards for the 股癌情報 tab.
func genHTMLNewsEp(t *testing.T, entries []scanner.WatchlistEntry, gv GuardrailViewOptions, sum *news.MarketNewsSummary, eps []news.EpisodeView) string {
	t.Helper()
	dir := t.TempDir()
	r := New(Config{OutputDir: dir})
	date := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	market := []scanner.StockAnalysis{{Symbol: "2327", Name: "國巨", Close: 100}}
	if err := r.Generate(market, nil, entries, nil, "-", date, gv, sum, eps); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report_20260605.html"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	return string(b)
}

func sampleEpisodes() []news.EpisodeView {
	return []news.EpisodeView{
		{
			EP: 678, Label: "EP678", PublishedAt: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
			RawTitle: "股癌筆記EP678", Conflict: true,
			Sources: []news.NewsSource{{Provider: "socialworkerdaily", URL: "https://socialworkerdaily.com/notes-of-gooaye-ep-678/"}},
			Lines: []news.EpisodeLine{
				{Signal: news.SignalBearish, Entities: "記憶體", Evidence: "利多不漲", Strength: 3, Conflict: true},
				{Signal: news.SignalBullish, Entities: "CCL 銅箔基板、國巨(2327)", Evidence: "強勢族群", Strength: 4},
			},
			Stocks:  []news.StockReference{{Code: "2327", Name: "國巨", Resolved: true}},
			Excerpt: "…內文摘錄…",
		},
	}
}

// TestGooayeTabShown: with ShowNews + episodes, the 股癌情報 tab + a card render.
func TestGooayeTabShown(t *testing.T) {
	html := genHTMLNewsEp(t, []scanner.WatchlistEntry{newsEntry()}, GuardrailViewOptions{ShowNews: true}, newsSummary(), sampleEpisodes())
	for _, want := range []string{
		"tab(event,'gooaye')", "股癌情報", "id=\"tab-gooaye\"",
		"EP678", "股癌筆記EP678", "記憶體", "利多不漲", "觀點分歧",
		"國巨(2327)", "內文摘錄", "socialworkerdaily",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("gooaye tab should contain %q", want)
		}
	}
}

// TestGooayeHiddenByDefault: no tab/pane when ShowNews=false, even with episodes present.
func TestGooayeHiddenByDefault(t *testing.T) {
	html := genHTMLNewsEp(t, []scanner.WatchlistEntry{newsEntry()}, GuardrailViewOptions{ShowNews: false}, newsSummary(), sampleEpisodes())
	for _, bad := range []string{"tab(event,'gooaye')", "id=\"tab-gooaye\"", "股癌情報"} {
		if strings.Contains(html, bad) {
			t.Errorf("gooaye tab must be hidden when ShowNews=false, found %q", bad)
		}
	}
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
		"⑩ 消息面", "股癌EP677", "偏多", "資金輪動出場", "DIVERGENCE",
		// market banner: per-sector multi-window table (方案 A + windows)
		"市場消息面", "近1日", "近3日", "近7日",
		"被動元件", "🟢偏多", "🟢×3", "CCL 銅箔基板", "－", "記憶體", "EP677",
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
