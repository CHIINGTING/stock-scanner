package validator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/analysishistory"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// writeCache writes a fetcher-format cache file so PriceStore can serve forward prices.
func writeCache(t *testing.T, dir, code string, candles []fetcher.Candle) {
	t.Helper()
	type cf struct {
		FetchedAt time.Time         `json:"fetched_at"`
		Data      fetcher.StockData `json:"data"`
	}
	b, _ := json.Marshal(cf{FetchedAt: time.Now(), Data: fetcher.StockData{Symbol: code, Candles: candles}})
	if err := os.WriteFile(filepath.Join(dir, code+"_TW.json"), b, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

func TestEvaluateCohorts(t *testing.T) {
	cacheDir := t.TempDir()
	sideDir := t.TempDir()

	// Rising series from 2026-07-01 so BUY-side forward returns are positive.
	var candles []fetcher.Candle
	base := time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		p := 100.0 + float64(i)
		candles = append(candles, fetcher.Candle{Date: base.AddDate(0, 0, i), Open: p, High: p, Low: p, Close: p, Volume: 1000})
	}
	writeCache(t, cacheDir, "2330", candles)

	// Sidecar on the signal day matching the trust-buy + foreign-reversal cohort.
	writeSidecar(t, sideDir, "2026-07-01", analysishistory.StockData{
		Code: "2330", Action: "BUY", Score: 80,
		Institution: &analysishistory.InstitutionPayload{
			Foreign:         analysishistory.LegPayload{Transition: "SELL_TO_BUY", DataComplete: true},
			InvestmentTrust: analysishistory.LegPayload{ConsecutiveBuyDays: 4, StreakComplete: true, DataComplete: true},
		},
		Bias: &analysishistory.BiasPayload{Risk: "NORMAL"},
	})

	sc := NewSidecarStore(sideDir)
	ps := NewPriceStore(cacheDir)
	dates := []time.Time{time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	cohorts := []Cohort{TrustBuyForeignReversalNotOverheated(3), HighBiasForeignDistribution()}

	stats := EvaluateCohorts(sc, ps, dates, []int{1, 3}, 3, cohorts)

	if stats[0].N != 1 {
		t.Fatalf("cohort0 N = %d, want 1", stats[0].N)
	}
	if stats[0].Wins != 1 || stats[0].WinRate != 1.0 {
		t.Errorf("cohort0 wins=%d winrate=%.2f, want 1 / 1.0", stats[0].Wins, stats[0].WinRate)
	}
	if stats[0].PrimaryReturn <= 0 {
		t.Errorf("cohort0 primary return should be positive, got %.2f", stats[0].PrimaryReturn)
	}
	if stats[0].AvgMFE <= 0 {
		t.Errorf("cohort0 MFE should be positive, got %.2f", stats[0].AvgMFE)
	}
	// The distribution cohort should not match this record.
	if stats[1].N != 0 {
		t.Errorf("cohort1 N = %d, want 0", stats[1].N)
	}
}

func TestCohortExcludesIncomplete(t *testing.T) {
	// Same cohort predicate but StreakComplete=false → must NOT match.
	rec := analysishistory.StockData{
		Institution: &analysishistory.InstitutionPayload{
			Foreign:         analysishistory.LegPayload{Transition: "SELL_TO_BUY", DataComplete: true},
			InvestmentTrust: analysishistory.LegPayload{ConsecutiveBuyDays: 4, StreakComplete: false, DataComplete: true},
		},
		Bias: &analysishistory.BiasPayload{Risk: "NORMAL"},
	}
	if TrustBuyForeignReversalNotOverheated(3).Match(rec) {
		t.Errorf("incomplete streak must be excluded from cohort")
	}
}
