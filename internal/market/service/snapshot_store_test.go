package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func sampleSnapshot(date string) *model.Snapshot {
	s := model.NewSnapshot(date, time.Date(2026, 8, 7, 15, 30, 0, 0, time.UTC))
	s.Score = 67
	s.Confidence = 0.72
	s.Regime = model.RegimeBullPullback
	s.RuleID = model.RulePullbackOrderly
	s.Structure = model.StructureView{
		Benchmark: model.Benchmark0050, Structure: model.StructureUptrend,
		Breadth: model.BreadthCooling, Posture: model.PostureNeutral,
		TrendIntact: true, OrderlyCorrection: true,
		Metrics: map[string]float64{model.MetricBreadthMA20: 47.5},
	}
	s.Evidence = []model.Evidence{
		{Source: model.EvidencePrice, Direction: model.DirBullish, Strength: model.StrengthModerate,
			Confidence: 0.8, Available: true},
		model.MissingEvidence(model.EvidenceMargin, "MI_MARGN timeout"),
	}
	s.Raw = model.RawSnapshot{
		Date:    date,
		Futures: &model.FuturesOIData{Contract: "臺股期貨", Item: "外資及陸資", NetOI: -87911},
	}
	return s
}

func TestSaveLoadSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := sampleSnapshot("2026-08-07")

	path, err := SaveSnapshot(dir, in)
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if filepath.Base(path) != "market_2026-08-07.json" {
		t.Errorf("path = %s, want market_<date>.json", path)
	}
	if !HasSnapshot(dir, "2026-08-07") {
		t.Error("HasSnapshot should see the file just written")
	}
	if HasSnapshot(dir, "2026-08-06") {
		t.Error("HasSnapshot reported a date that was never written")
	}

	out, err := LoadSnapshot(dir, "2026-08-07")
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if out.Regime != model.RegimeBullPullback || out.RuleID != model.RulePullbackOrderly {
		t.Errorf("verdict changed on disk: %s / %s", out.Regime, out.RuleID)
	}
	if out.Raw.Futures == nil || out.Raw.Futures.NetOI != -87911 {
		t.Error("raw observation lost — offline re-derivation would be impossible")
	}
	if v, ok := out.Structure.Metric(model.MetricBreadthMA20); !ok || v != 47.5 {
		t.Errorf("structure metric lost: %v %v", v, ok)
	}
	if out.Evidence[1].Usable() {
		t.Error("the unavailable evidence came back usable")
	}
}

// A malformed snapshot must never enter the archive: old entries staying trustworthy is the
// only reason the archive is worth keeping.
func TestSaveSnapshotRejectsInvalid(t *testing.T) {
	dir := t.TempDir()

	bad := sampleSnapshot("2026-08-07")
	bad.Regime = model.Regime("Bull Market") // pre-rename value
	if _, err := SaveSnapshot(dir, bad); err == nil {
		t.Error("an undefined regime must not be written")
	}

	bad2 := sampleSnapshot("2026-08-07")
	bad2.Evidence[1].Direction = model.DirNeutral // unavailable but claims a reading
	if _, err := SaveSnapshot(dir, bad2); err == nil {
		t.Error("missing evidence dressed as neutral must not be written")
	}

	if _, err := SaveSnapshot(dir, nil); err == nil {
		t.Error("nil snapshot must be rejected")
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("rejected writes left %d files behind", len(entries))
	}
}

func TestLoadSnapshotRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(SnapshotPath(dir, "2026-08-07"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(dir, "2026-08-07"); err == nil {
		t.Error("malformed JSON should fail")
	}

	// A hand-edited file that decodes but is semantically undefined must also fail, rather
	// than feeding a validation run a regime nobody defined.
	edited := `{"date":"2026-08-06","schema_version":1,"regime":"MEGA_BULL","rule_id":"R6"}`
	if err := os.WriteFile(SnapshotPath(dir, "2026-08-06"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSnapshot(dir, "2026-08-06")
	if err == nil {
		t.Fatal("an undefined regime on disk should fail to load")
	}
	if !strings.Contains(err.Error(), "undefined regime") {
		t.Errorf("unexpected error: %v", err)
	}

	if _, err := LoadSnapshot(dir, "2026-01-01"); err == nil {
		t.Error("a missing file should error")
	}
}

// Re-running the same date replaces the file rather than accumulating duplicates.
func TestSaveSnapshotOverwritesSameDate(t *testing.T) {
	dir := t.TempDir()
	first := sampleSnapshot("2026-08-07")
	if _, err := SaveSnapshot(dir, first); err != nil {
		t.Fatal(err)
	}
	second := sampleSnapshot("2026-08-07")
	second.Score = 42
	second.Regime = model.RegimeSideways
	second.RuleID = model.RuleSidewaysRange
	if _, err := SaveSnapshot(dir, second); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d files, want 1 per trading day", len(entries))
	}
	out, err := LoadSnapshot(dir, "2026-08-07")
	if err != nil {
		t.Fatal(err)
	}
	if out.Score != 42 || out.Regime != model.RegimeSideways {
		t.Error("re-run did not replace the day's snapshot")
	}
}

// The atomic write must not leave temp files behind on success.
func TestSaveSnapshotLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveSnapshot(dir, sampleSnapshot("2026-08-07")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("got %d files, want exactly the snapshot", len(entries))
	}
}

// market_<date>.json is the ONLY snapshot format. The legacy Dashboard writer that once
// shared this directory was removed in M8, so the filename no longer has to avoid a
// collision — but the prefix stays, matching internal/institution's {source}_{date}.json
// convention and keeping the directory self-describing.
func TestSnapshotPathShape(t *testing.T) {
	if got := SnapshotPath("/x", "2026-08-07"); got != "/x/market_2026-08-07.json" {
		t.Errorf("SnapshotPath = %s", got)
	}
}
