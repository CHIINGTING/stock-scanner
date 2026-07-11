package news

import (
	"testing"
	"time"
)

func TestBuildEpisodes(t *testing.T) {
	p677 := time.Date(2026, 7, 8, 15, 0, 0, 0, time.UTC)
	p678 := time.Date(2026, 7, 11, 15, 0, 0, 0, time.UTC)
	signals := []NewsSignal{
		{EventID: "gooaye-ep-677", PublishedAt: p677, Signal: SignalBullish, Strength: 4, Sectors: []string{"CCL 銅箔基板"},
			Stocks: []StockReference{{Code: "2327", Name: "國巨", Resolved: true}}, Summary: "強勢族群：CCL", RawTitle: "股癌筆記EP677",
			Sources: []NewsSource{{Provider: "socialworkerdaily", URL: "https://swd/677"}}, ContentExcerpt: "…摘錄…"},
		{EventID: "gooaye-ep-677", PublishedAt: p677, Signal: SignalRiskWarning, Strength: 2, Sectors: []string{"被動元件"},
			Summary: "進入修正", Sources: []NewsSource{{Provider: "socialworkerdaily", URL: "https://swd/677"}}},
		{EventID: "gooaye-ep-678", PublishedAt: p678, Signal: SignalBearish, Strength: 3, Sectors: []string{"記憶體"},
			Conflict: true, Summary: "利多不漲", Sources: []NewsSource{{Provider: "socialworkerdaily", URL: "https://swd/678"}}},
	}
	eps := BuildEpisodes(signals)
	if len(eps) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(eps))
	}
	// newest first: EP678 before EP677.
	if eps[0].EP != 678 || eps[1].EP != 677 {
		t.Fatalf("episodes not newest-first: %d then %d", eps[0].EP, eps[1].EP)
	}
	e678, e677 := eps[0], eps[1]
	if !e678.Conflict {
		t.Error("EP678 should carry the conflict flag")
	}
	if e677.Label != "EP677" || e677.RawTitle != "股癌筆記EP677" {
		t.Errorf("EP677 label/title wrong: %q / %q", e677.Label, e677.RawTitle)
	}
	if len(e677.Lines) != 2 {
		t.Errorf("EP677 should have 2 lines, got %d", len(e677.Lines))
	}
	// lines sorted by strength desc → bullish(4) before risk(2).
	if e677.Lines[0].Signal != SignalBullish || e677.Lines[1].Signal != SignalRiskWarning {
		t.Errorf("EP677 lines not strength-sorted: %+v", e677.Lines)
	}
	// entities include resolved stock with code.
	if e677.Lines[0].Entities == "" || len(e677.Stocks) != 1 || e677.Stocks[0].Code != "2327" {
		t.Errorf("EP677 stock/entity aggregation wrong: entities=%q stocks=%+v", e677.Lines[0].Entities, e677.Stocks)
	}
	// sources deduped (two signals, same source) → 1 source.
	if len(e677.Sources) != 1 || e677.Sources[0].Provider != "socialworkerdaily" {
		t.Errorf("EP677 sources not deduped: %+v", e677.Sources)
	}
	if e677.Excerpt == "" {
		t.Error("EP677 excerpt should be preserved")
	}
}

func TestBuildEpisodesEmpty(t *testing.T) {
	if eps := BuildEpisodes(nil); len(eps) != 0 {
		t.Errorf("nil signals → no episodes, got %d", len(eps))
	}
}
