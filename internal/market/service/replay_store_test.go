package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

func sampleReplay(date string) *model.RegimeReplay {
	r := model.NewRegimeReplay(date, time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC))
	r.Regime = model.RegimeBullPullback
	r.RuleID = model.RulePullbackOrderly
	r.Structure = model.StructureView{
		Benchmark: model.Benchmark0050, Structure: model.StructureUptrend,
		Breadth: model.BreadthCooling, Posture: model.PostureUnknown,
		TrendIntact: true, OrderlyCorrection: true,
		Metrics: map[string]float64{model.MetricBreadthMA20: 47.5},
	}
	r.Reasons = []string{"結構 UPTREND，廣度 COOLING"}
	r.InputExtent = model.ReplayExtent{
		BenchmarkSymbol: string(model.Benchmark0050),
		BenchmarkBars:   400, BenchmarkFirst: "2024-09-02", BenchmarkLast: date,
		BreadthIndex: 380, BreadthFirst: "2024-09-02", BreadthLast: date,
		BreadthUniverse: 1780,
	}
	return r
}

func TestSaveLoadReplayRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := sampleReplay("2026-08-07")

	path, err := SaveReplay(dir, in)
	if err != nil {
		t.Fatalf("SaveReplay: %v", err)
	}
	if filepath.Base(path) != "regime_2026-08-07.json" {
		t.Errorf("path = %s, want regime_<date>.json", path)
	}
	if !HasReplay(dir, "2026-08-07") {
		t.Error("HasReplay should see the file just written")
	}
	if HasReplay(dir, "2026-08-06") {
		t.Error("HasReplay reported a date that was never written")
	}

	out, err := LoadReplay(dir, "2026-08-07")
	if err != nil {
		t.Fatalf("LoadReplay: %v", err)
	}
	if out.Regime != model.RegimeBullPullback || out.RuleID != model.RulePullbackOrderly {
		t.Errorf("verdict changed on disk: %s / %s", out.Regime, out.RuleID)
	}
	if out.Structure.Posture != model.PostureUnknown {
		t.Errorf("posture came back %s", out.Structure.Posture)
	}
	if out.InputExtent != in.InputExtent {
		t.Errorf("input extent changed on disk:\n  %+v\n  %+v", in.InputExtent, out.InputExtent)
	}
	if out.Structure.Metrics[model.MetricBreadthMA20] != 47.5 {
		t.Errorf("metrics lost: %v", out.Structure.Metrics)
	}
	// A re-run replaces the day rather than accumulating files.
	if _, err := SaveReplay(dir, sampleReplay("2026-08-07")); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, _ := filepath.Glob(filepath.Join(dir, "regime_*.json"))
	if len(got) != 1 {
		t.Errorf("re-running a date left %d files: %v", len(got), got)
	}
}

// Malformed records must never reach the archive — and must not leave a temp file behind.
func TestSaveReplayRefusesMalformedRecords(t *testing.T) {
	dir := t.TempDir()

	bad := sampleReplay("2026-08-07")
	bad.Structure.Posture = model.PostureNeutral // a replay cannot know this
	if _, err := SaveReplay(dir, bad); err == nil {
		t.Fatal("a replay claiming a known posture was written to the archive")
	}
	if HasReplay(dir, "2026-08-07") {
		t.Error("an invalid replay reached disk")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("validation failure left files behind: %v", entries)
	}
	if _, err := SaveReplay(dir, nil); err == nil {
		t.Error("a nil replay was accepted")
	}
}

// LoadReplay validates on read: a file hand-edited into an undefined state must fail loudly
// rather than feed a study a regime nobody defined.
func TestLoadReplayValidatesOnRead(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveReplay(dir, sampleReplay("2026-08-07")); err != nil {
		t.Fatal(err)
	}
	path := ReplayPath(dir, "2026-08-07")
	b, _ := os.ReadFile(path)

	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}
	// Hand-promote the replay into a "live" posture, the single most damaging edit.
	generic["structure"].(map[string]any)["institutional_posture"] = "SUPPORTIVE"
	edited, _ := json.Marshal(generic)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReplay(dir, "2026-08-07"); err == nil {
		t.Fatal("a hand-edited replay with a known posture was loaded as valid")
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReplay(dir, "2026-08-07"); err == nil {
		t.Fatal("truncated JSON was loaded as valid")
	}
	if _, err := LoadReplay(dir, "2026-08-08"); !os.IsNotExist(err) {
		t.Errorf("a missing replay must surface as NotExist, got %v", err)
	}
}

// ── the two archives cannot be read as each other ─────────────────────────────────────
//
// This is the structural proof behind "a replay is not a live snapshot". It is not enough
// that the two live in different directories: someone WILL copy a file, or point a loader
// at the wrong path. So each loader must reject the other's bytes on CONTENT, independently
// of where the file sits.
//
// Both halves deliberately place the foreign bytes under the LOCAL filename, so a pass
// cannot come from "file not found". The assertions check that too.

func TestAReplayFileCannotBeLoadedAsALiveSnapshot(t *testing.T) {
	dir := t.TempDir()
	replayPath, err := SaveReplay(dir, sampleReplay("2026-08-07"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(replayPath)
	if err != nil {
		t.Fatal(err)
	}

	// Misfile the replay's bytes as if they were a snapshot.
	if err := os.WriteFile(SnapshotPath(dir, "2026-08-07"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadSnapshot(dir, "2026-08-07")
	if err == nil {
		t.Fatal("replay bytes were accepted as a live snapshot — a replay has no Market " +
			"Score, so it would have been consumed as score 0, i.e. maximally bearish, on " +
			"every replayed day")
	}
	if os.IsNotExist(err) {
		t.Fatalf("the test proved nothing: the file was missing, not rejected (%v)", err)
	}
	t.Logf("LoadSnapshot rejected the replay: %v", err)

	// And confirm the rejection is structural rather than incidental: the keys a snapshot
	// reader needs are simply not in the file.
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"date", "regime", "schema_version"} {
		if _, ok := generic[k]; ok {
			t.Errorf("a replay file carries the snapshot key %q; the rejection above may be "+
				"incidental rather than structural", k)
		}
	}
	// A zero score decoded from a file that has no score field is the exact failure mode
	// this separation exists to prevent, so spell it out.
	var asSnapshot model.Snapshot
	if err := json.Unmarshal(b, &asSnapshot); err != nil {
		t.Fatal(err)
	}
	if asSnapshot.Score != 0 {
		t.Fatalf("unexpected: replay bytes decoded to score %v", asSnapshot.Score)
	}
	if err := asSnapshot.Validate(); err == nil {
		t.Fatal("replay bytes decoded into a Snapshot that PASSES Validate with score 0 — " +
			"the JSON keys are no longer distinct enough to keep the archives apart")
	}
}

func TestALiveSnapshotCannotBeLoadedAsAReplay(t *testing.T) {
	dir := t.TempDir()
	snapPath, err := SaveSnapshot(dir, sampleSnapshot("2026-08-07"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ReplayPath(dir, "2026-08-07"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadReplay(dir, "2026-08-07")
	if err == nil {
		t.Fatal("live snapshot bytes were accepted as a point-in-time replay — a study " +
			"would then mix regimes that could see institutional posture with ones that " +
			"could not")
	}
	if os.IsNotExist(err) {
		t.Fatalf("the test proved nothing: the file was missing, not rejected (%v)", err)
	}
	t.Logf("LoadReplay rejected the snapshot: %v", err)

	// A snapshot whose posture happens to be UNKNOWN must still be rejected, otherwise the
	// only thing keeping the archives apart would be the one field most likely to coincide:
	// the live pipeline emits PostureUnknown whenever institutional evidence is missing.
	unknownPosture := sampleSnapshot("2026-08-08")
	unknownPosture.Structure.Posture = model.PostureUnknown
	p2, err := SaveSnapshot(dir, unknownPosture)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(p2)
	if err := os.WriteFile(ReplayPath(dir, "2026-08-08"), b2, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReplay(dir, "2026-08-08"); err == nil {
		t.Fatal("a live snapshot with PostureUnknown was accepted as a replay; the posture " +
			"check is load-bearing but must not be the ONLY separator")
	} else {
		t.Logf("still rejected with PostureUnknown: %v", err)
	}
}

// The glob every snapshot reader uses must not see replays — including one misfiled straight
// into the snapshot directory.
func TestMarketGlobDoesNotPickUpReplays(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveSnapshot(dir, sampleSnapshot("2026-08-07")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"2026-08-07", "2026-08-08", "2026-08-11"} {
		if _, err := SaveReplay(dir, sampleReplay(d)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := filepath.Glob(filepath.Join(dir, "market_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "market_2026-08-07.json" {
		t.Fatalf("market_*.json matched %v — a replay would be read as live history", got)
	}

	// The reverse glob, so a replay consumer cannot pick up live snapshots either.
	rep, err := filepath.Glob(filepath.Join(dir, "regime_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep) != 3 {
		t.Fatalf("regime_*.json matched %v, want 3 replays", rep)
	}
	for _, p := range rep {
		if strings.Contains(filepath.Base(p), "market_") {
			t.Errorf("%s matched both patterns", p)
		}
	}

	// And the paths themselves must never collide for the same date.
	if SnapshotPath(dir, "2026-08-07") == ReplayPath(dir, "2026-08-07") {
		t.Fatal("SnapshotPath and ReplayPath produce the same file")
	}
	if !strings.HasPrefix(filepath.Base(ReplayPath(dir, "2026-08-07")), "regime_") {
		t.Fatalf("ReplayPath lost its regime_ prefix: %s", ReplayPath(dir, "2026-08-07"))
	}
}

// The default output directory must not be the live archive. This is the one mistake a
// caller cannot recover from: replays interleaved into data/market/ would be indistinguishable
// from live snapshots by filename alone.
func TestDefaultReplayDirIsNotTheSnapshotDir(t *testing.T) {
	if DefaultReplayDir == "data/market" {
		t.Fatal("DefaultReplayDir points at the live snapshot archive")
	}
	if DefaultReplayDir != "data/market_replay" {
		t.Errorf("DefaultReplayDir = %q; cmd/market-regime-replay documents data/market_replay",
			DefaultReplayDir)
	}
}
