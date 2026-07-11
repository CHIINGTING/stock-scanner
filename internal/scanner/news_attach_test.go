package scanner

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/news"
)

func newsWLEntry(sym string) WatchlistEntry {
	e := WatchlistEntry{}
	e.A.Symbol = sym
	e.RocketScore = 42
	e.WatchAction = WatchAction("WATCH")
	e.ExplosionProb = "MED"
	return e
}

func TestAttachNewsPopulatesMatch(t *testing.T) {
	entries := []WatchlistEntry{newsWLEntry("2327"), newsWLEntry("9999")}
	views := map[string]*news.StockNewsView{
		"2327": {Computed: true, StockItems: []news.NewsSignal{{Signal: news.SignalBullish}}},
	}
	AttachNews(entries, views)
	if entries[0].News == nil {
		t.Error("matched entry should get News")
	}
	if entries[1].News != nil {
		t.Error("unmatched entry must stay nil")
	}
}

func TestAttachNewsIgnoresNotComputed(t *testing.T) {
	entries := []WatchlistEntry{newsWLEntry("2327")}
	views := map[string]*news.StockNewsView{"2327": {Computed: false}}
	AttachNews(entries, views)
	if entries[0].News != nil {
		t.Error("Computed=false view must not attach")
	}
}

func TestAttachNewsEmptyNoop(t *testing.T) {
	entries := []WatchlistEntry{newsWLEntry("2327")}
	AttachNews(entries, nil)
	if entries[0].News != nil {
		t.Error("nil views must be a noop")
	}
}

func TestAttachNewsDoesNotChangeScore(t *testing.T) {
	entries := []WatchlistEntry{newsWLEntry("2327")}
	beforeScore := entries[0].RocketScore
	beforeAction := entries[0].WatchAction
	beforeProb := entries[0].ExplosionProb
	views := map[string]*news.StockNewsView{
		"2327": {Computed: true, StockItems: []news.NewsSignal{{Signal: news.SignalBearish, Strength: 9}}},
	}
	AttachNews(entries, views)
	if entries[0].RocketScore != beforeScore {
		t.Error("AttachNews must not change RocketScore")
	}
	if entries[0].WatchAction != beforeAction {
		t.Error("AttachNews must not change WatchAction")
	}
	if entries[0].ExplosionProb != beforeProb {
		t.Error("AttachNews must not change ExplosionProb")
	}
}
