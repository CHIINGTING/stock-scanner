package analyzer

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func TestAggregateWeightedAverage(t *testing.T) {
	mods := []model.ModuleScore{
		{Name: "A", Score: 100, Weight: 20, Available: true},
		{Name: "B", Score: 0, Weight: 20, Available: true},
		{Name: "C", Score: 50, Weight: 60, Available: true},
	}
	ms := Aggregate(mods)
	// (100*20 + 0*20 + 50*60) / 100 = 50
	if ms.Total != 50 {
		t.Errorf("Total = %.2f, want 50", ms.Total)
	}
	if ms.AvailableCount != 3 {
		t.Errorf("AvailableCount = %d, want 3", ms.AvailableCount)
	}
}

// A missing module must drop out and the remaining weights renormalize — NOT drag the score
// toward zero.
func TestAggregateRenormalizesOverAvailable(t *testing.T) {
	mods := []model.ModuleScore{
		{Name: "A", Score: 80, Weight: 50, Available: true},
		{Name: "B", Score: 40, Weight: 50, Available: false}, // unavailable
	}
	ms := Aggregate(mods)
	if ms.Total != 80 {
		t.Errorf("Total = %.2f, want 80 (B excluded)", ms.Total)
	}
	if ms.AvailableCount != 1 {
		t.Errorf("AvailableCount = %d, want 1", ms.AvailableCount)
	}
}

func TestAggregateEmptyIsZeroNoPanic(t *testing.T) {
	ms := Aggregate(nil)
	if ms.Total != 0 || ms.AvailableCount != 0 {
		t.Errorf("empty aggregate = {%.2f, %d}, want {0, 0}", ms.Total, ms.AvailableCount)
	}
	all := []model.ModuleScore{{Name: "A", Score: 99, Weight: 10, Available: false}}
	if got := Aggregate(all); got.Total != 0 || got.AvailableCount != 0 {
		t.Errorf("all-unavailable aggregate = {%.2f, %d}, want {0, 0}", got.Total, got.AvailableCount)
	}
}
