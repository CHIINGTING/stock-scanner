package service

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir() // fresh dir → also exercises MkdirAll
	want := &model.Dashboard{
		Date:             "2026-07-14",
		MarketScore:      model.MarketScore{Total: 62, AvailableCount: 7, Modules: []model.ModuleScore{{Name: "ForeignFutures", Score: 40, Weight: 20, State: "More Short", Available: true}}},
		Regime:           model.RegimeInfo{Regime: model.RegimeBullPullback, Confidence: model.ConfidenceMedium, Description: "d", Strategy: "s", Risk: "r"},
		Sectors:          []model.SectorRank{{Name: "Memory", Stars: 5, Score: 83}},
		TopOpportunities: []model.Opportunity{{Sector: "Memory", Reasons: []string{"x"}}},
		TopRisks:         []model.Risk{{Sector: "Steel", Reasons: []string{"y"}}},
	}
	path, err := Save(dir, want)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if path != StoragePath(dir, "2026-07-14") {
		t.Errorf("path = %q, want %q", path, StoragePath(dir, "2026-07-14"))
	}

	got, err := Load(dir, "2026-07-14")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Date != want.Date || got.MarketScore.Total != want.MarketScore.Total ||
		got.Regime.Regime != want.Regime.Regime || len(got.MarketScore.Modules) != 1 ||
		len(got.Sectors) != 1 || got.Sectors[0].Stars != 5 {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestSaveNilDashboard(t *testing.T) {
	if _, err := Save(t.TempDir(), nil); err == nil {
		t.Error("Save(nil) should error")
	}
}
