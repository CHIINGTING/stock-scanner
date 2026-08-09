package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// stubNet is a NetProvider that returns whatever it is told to, so the pipeline can be
// exercised end to end without a network.
type stubNet struct {
	name string
	snap *model.RawSnapshot
	err  error
}

func (s stubNet) Name() string { return s.name }
func (s stubNet) Fetch(context.Context, string) (*model.RawSnapshot, error) {
	return s.snap, s.err
}

// writeCacheSeries builds a synthetic .cache good enough to drive price + breadth.
func writeCacheSeries(t *testing.T, dir string, symbols int, days int) []string {
	t.Helper()
	type candle struct {
		Date  time.Time
		Close float64
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var dates []string
	for s := 0; s < symbols; s++ {
		var cs []candle
		for i := 0; i < days; i++ {
			d := start.AddDate(0, 0, i)
			cs = append(cs, candle{Date: d, Close: 100 + 0.4*float64(i)})
			if s == 0 {
				dates = append(dates, d.Format("2006-01-02"))
			}
		}
		code := "1" + string(rune('0'+s/100%10)) + string(rune('0'+s/10%10)) + string(rune('0'+s%10))
		rec := map[string]any{"fetched_at": time.Now(),
			"data": map[string]any{"Symbol": code, "Name": code, "Market": "TW", "Candles": cs}}
		b, _ := json.Marshal(rec)
		if err := os.WriteFile(filepath.Join(dir, code+"_TW.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The benchmark must exist under its own symbol.
	src, _ := os.ReadFile(filepath.Join(dir, "1000_TW.json"))
	var rec map[string]any
	json.Unmarshal(src, &rec)
	rec["data"].(map[string]any)["Symbol"] = "0050"
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, "0050_TW.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dates
}

func testPipeline(t *testing.T, net ...NetProvider) (*Pipeline, []string, string) {
	t.Helper()
	cacheDir, outDir := t.TempDir(), t.TempDir()
	dates := writeCacheSeries(t, cacheDir, 600, 200)
	cfg := model.MarketConfig{StorageDir: outDir}.Defaulted()
	return NewPipeline(cfg, provider.CacheFeed{Dir: cacheDir}, net...), dates, outDir
}

func TestPipelineProducesCompleteSnapshot(t *testing.T) {
	raw := &model.RawSnapshot{
		Cash:    &model.CashData{ForeignBuy: 3e11, ForeignSell: 3.4e11, ForeignNet: -4e10},
		Margin:  &model.MarginBalanceData{MarginBalanceLots: 9e6, MarginPrevLots: 8.99e6},
		Futures: &model.FuturesOIData{Contract: "臺股期貨", Item: "外資及陸資", LongOI: 8321, ShortOI: 96232, NetOI: -87911},
	}
	p, dates, _ := testPipeline(t, stubNet{name: "stub", snap: raw})
	date := dates[len(dates)-1]

	snap, err := p.Run(context.Background(), date)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := snap.Validate(); err != nil {
		t.Fatalf("snapshot invalid: %v", err)
	}
	if snap.Date != date || snap.SchemaVer != model.SnapshotSchemaVersion {
		t.Errorf("header wrong: %s v%d", snap.Date, snap.SchemaVer)
	}
	if !snap.Regime.Valid() || snap.Regime == model.RegimeUnknown {
		t.Errorf("regime = %s on a complete day", snap.Regime)
	}
	if snap.RuleID == "" {
		t.Error("no rule id recorded")
	}
	if len(snap.Evidence) != 5 {
		t.Errorf("want all five evidences present, got %d", len(snap.Evidence))
	}
	// Everything needed to reconstruct the day must be there.
	if snap.Raw.Futures == nil || snap.Raw.Cash == nil || snap.Raw.Margin == nil {
		t.Error("raw observations not carried into the snapshot")
	}
	if len(snap.Structure.Metrics) == 0 {
		t.Error("structure metrics missing")
	}
	if len(snap.Reasons) < 2 {
		t.Error("score/confidence must be explained in Reasons")
	}
	if snap.Score < 0 || snap.Score > 100 || snap.Confidence < 0 || snap.Confidence > 1 {
		t.Errorf("out of range: score=%v conf=%v", snap.Score, snap.Confidence)
	}
}

// The architectural invariant, verified at the level that actually ships: the regime must
// be reproducible from the stored structure alone, with no reference to the score.
func TestPipelineRegimeIsReproducibleWithoutScore(t *testing.T) {
	p, dates, _ := testPipeline(t)
	snap, err := p.Run(context.Background(), dates[len(dates)-1])
	if err != nil {
		t.Fatal(err)
	}
	// Re-decide from the persisted view alone.
	again := decideFromStored(snap)
	if again != snap.Regime {
		t.Errorf("stored structure re-decides to %s but snapshot says %s", again, snap.Regime)
	}
}

// A dead exchange must degrade the day, not fail it — and must never become neutral data.
func TestPipelineToleratesDeadExchange(t *testing.T) {
	p, dates, _ := testPipeline(t, stubNet{name: "dead", err: errors.New("boom")})
	snap, err := p.Run(context.Background(), dates[len(dates)-1])
	if err != nil {
		t.Fatalf("a dead exchange must not fail the day: %v", err)
	}
	if snap.Raw.Cash != nil || snap.Raw.Futures != nil {
		t.Error("a failed source must leave datasets nil")
	}
	// Price and breadth still carry the day; the institutional evidence is unavailable.
	var usable int
	for _, e := range snap.Evidence {
		if e.Usable() {
			usable++
		}
	}
	if usable < 2 {
		t.Errorf("price and breadth should still be usable, got %d", usable)
	}
	if snap.Structure.Posture != model.PostureUnknown {
		t.Errorf("with no institutional data the posture must be UNKNOWN, got %s", snap.Structure.Posture)
	}
	if snap.Regime == model.RegimeUnknown {
		t.Error("price+breadth alone are enough to describe the market")
	}
	// The outage must be visible in the record.
	var sawErr bool
	for _, s := range snap.Raw.Sources {
		if s.Status == model.SourceError {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("the failure is not recorded in provenance")
	}
	if len(snap.Caveats) == 0 {
		t.Error("missing evidence should be listed in caveats")
	}
}

// No price cache at all is the one case that genuinely cannot produce a day.
func TestPipelineFailsWithoutBenchmark(t *testing.T) {
	cfg := model.MarketConfig{StorageDir: t.TempDir()}.Defaulted()
	p := NewPipeline(cfg, provider.CacheFeed{Dir: t.TempDir()})
	if _, err := p.Run(context.Background(), "2026-08-07"); err == nil {
		t.Error("an empty cache must be an error, not an empty snapshot")
	}
	if _, err := p.Run(context.Background(), "not-a-date"); err == nil {
		t.Error("a malformed date must be rejected")
	}
}

func TestPipelineRunAndSaveRoundTrips(t *testing.T) {
	p, dates, outDir := testPipeline(t)
	date := dates[len(dates)-1]
	snap, path, err := p.RunAndSave(context.Background(), date)
	if err != nil {
		t.Fatalf("RunAndSave: %v", err)
	}
	if filepath.Dir(path) != outDir {
		t.Errorf("wrote to %s, want %s", path, outDir)
	}
	back, err := LoadSnapshot(outDir, date)
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if back.Regime != snap.Regime || back.RuleID != snap.RuleID || back.Score != snap.Score {
		t.Error("the archived day differs from the one produced")
	}
}

// The institutional context windows come from the archive. With an empty archive they must
// simply be absent — the analyzers then report lower confidence rather than inventing values.
func TestPipelineHistoryDegradesOnEmptyArchive(t *testing.T) {
	raw := &model.RawSnapshot{
		Futures: &model.FuturesOIData{Contract: "臺股期貨", Item: "外資及陸資", NetOI: -87911},
	}
	p, dates, _ := testPipeline(t, stubNet{name: "stub", snap: raw})
	snap, err := p.Run(context.Background(), dates[len(dates)-1])
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range snap.Evidence {
		if e.Source != model.EvidenceFutures {
			continue
		}
		if _, ok := e.Metric(model.MetricFutPercentile); ok {
			t.Error("an empty archive cannot produce a crowding percentile")
		}
		if e.Confidence > 0.6 {
			t.Errorf("confidence should be low without history, got %v", e.Confidence)
		}
	}
}

func decideFromStored(s *model.Snapshot) model.Regime {
	// Deliberately re-runs the engine on the persisted view only.
	return reDecide(s.Structure)
}
