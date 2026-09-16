package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/recon"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/study"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// Artifact is the whole machine-readable run file (EP-9 §26/§27).
type Artifact struct {
	MethodologyVersion string `json:"methodology_version"`
	BaselineCommit     string `json:"baseline_commit"`
	RuleVersion        string `json:"rule_version"`
	GeneratedAt        string `json:"generated_at"`

	ConclusionsHoldUnder string   `json:"conclusions_hold_under"`
	Caveats              []string `json:"caveats"`
	NotBacktestFitted    string   `json:"label"`

	DatasetFrom     string `json:"dataset_from"`
	DatasetTo       string `json:"dataset_to"`
	Sessions        int    `json:"sessions"`
	BlindOnly       int    `json:"blind_only_sessions_excluded_from_both_passes"`
	EligibleTotal   int    `json:"eligible_sessions_total"`
	SymbolsLoaded   int    `json:"symbols_loaded"`
	SymbolsObserved int    `json:"symbols_observed"`

	ScannerConfig map[string]any `json:"scanner_config"`

	Funnels map[string]*recon.Funnel `json:"funnels"`

	PIT   *study.Run `json:"replayed_pit"`
	Blind *study.Run `json:"regime_blind_coverage_only"`

	ReplayDigest string `json:"replay_archive_digest"`
	CacheDigest  string `json:"price_cache_digest"`
	DigestCovers string `json:"digests_cover"`

	// DroppedDuplicates is how many cache files a symbol de-duplication dropped. See the
	// data-integrity section of SUMMARY.md: without it one listing entered every session twice.
	// Drift is what a future reader needs to tell one run's population from another's. The
	// price cache is MUTABLE — it gains a session every trading day — so two runs of identical
	// code over "the same" cache cover different date ranges and produce different digests.
	// This was observed between two EP-9 runs hours apart: the axis gained 2026-09-16, the
	// eligible window moved forward by one session, and the study went from 355 sessions
	// ending 2026-08-18 to 356 ending 2026-08-19. Without this block the only evidence would
	// be two digests that differ for no stated reason.
	Drift DriftNote `json:"dataset_drift"`

	DroppedDuplicates int               `json:"dropped_duplicate_symbol_files"`
	DroppedDetail     map[string]string `json:"dropped_duplicate_detail,omitempty"`

	WallSeconds float64 `json:"wall_seconds"`
}

// DriftNote records the mutable inputs this run saw, so two runs can be told apart.
type DriftNote struct {
	// CacheNewestSession is the last session ANY cached symbol printed a bar on — the thing
	// that moves when the cache is refreshed.
	CacheNewestSession string `json:"cache_newest_session"`
	// AxisSessions is the full session axis length; EligibleSessions is what survives the
	// warm-up and outcome-window requirements; Covered is what also has a replay file.
	AxisSessions     int `json:"axis_sessions"`
	EligibleSessions int `json:"eligible_sessions"`
	CoveredSessions  int `json:"covered_sessions"`
	// Note explains, in words, what a reader should do when two runs disagree.
	Note string `json:"note"`
}

const driftNote = "The price cache is MUTABLE: it gains a session every trading day, and the " +
	"regime replay archive is regenerated from it. Two runs of identical code therefore cover " +
	"different date ranges and carry different digests. Compare price_cache_digest and " +
	"replay_archive_digest before comparing any number across runs; if they differ, the " +
	"populations differ and a cross-run delta is confounded by the dataset, not only by the " +
	"code. Observed: an EP-9 run on 355 sessions ending 2026-08-18 and a later run on 356 " +
	"ending 2026-08-19, hours apart, because the cache gained 2026-09-16."

func execute(r *recon.Reconstructor, replay *recon.ReplayArchive, cache *recon.Cache,
	covered, blindOnly, eligible []string, runDir string, sc scanner.Config, cacheDir string) {

	t0 := time.Now()
	a := &Artifact{
		MethodologyVersion: entryplanbacktest.MethodologyVersion,
		BaselineCommit:     entryplanbacktest.BaselineCommit,
		RuleVersion:        entryplan.RuleVersion,
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
		Caveats:            recon.ReplayCaveats(),
		NotBacktestFitted: "HEURISTIC — NOT BACKTEST-FITTED. No threshold in internal/entryplan " +
			"was fitted to anything measured here, and nothing measured here may be fed back " +
			"into one. A positive historical mean return is NOT 'validated profitable'.",
		DatasetFrom: covered[0], DatasetTo: covered[len(covered)-1],
		Sessions: len(covered), BlindOnly: len(blindOnly), EligibleTotal: len(eligible),
		SymbolsLoaded: len(cache.Symbols),
		ScannerConfig: map[string]any{
			"min_price": sc.MinPrice, "min_avg_volume": sc.MinAvgVolume, "top_n": sc.TopN,
			"use_adjusted_close": sc.UseAdjustedClose, "enable_rs_rank": sc.EnableRSRank,
			"enable_new_high": sc.EnableNewHigh, "enable_vcp": sc.EnableVCP,
			"enable_momentum_flow":            sc.EnableMomentumFlow,
			"enable_signal_guardrail_scoring": sc.EnableSignalGuardrailScoring,
			"enable_entry_plan":               sc.EnableEntryPlan,
			"enable_trend_extension":          sc.EnableTrendExtension,
			"enable_candlestick":              sc.EnableCandlestick,
			"enable_technical_indicators":     sc.EnableTechnicalIndicators,
			"enable_bias":                     sc.EnableBias,
		},
		Funnels: map[string]*recon.Funnel{},
		Drift: DriftNote{
			CacheNewestSession: cache.Axis[len(cache.Axis)-1],
			AxisSessions:       len(cache.Axis),
			EligibleSessions:   len(eligible),
			CoveredSessions:    len(covered),
			Note:               driftNote,
		},
		DroppedDuplicates: len(cache.DroppedDuplicates),
		DroppedDetail:     cache.DroppedDuplicates,
		DigestCovers:      "the EXACT files this run read: one sha256 per replay session file and per price-cache symbol file, rolled into one sha256 over 'name digest' lines sorted by name",
	}

	pitFunnel := recon.NewFunnel(recon.ArmReplayedPIT, recon.FullFidelity)
	blindFunnel := recon.NewFunnel(recon.ArmRegimeBlind, recon.FullFidelity)

	var pitObs, blindObs []study.Observation
	symbols := map[string]bool{}

	fmt.Println("── reconstructing (both passes, same session set) ──")
	for i, d := range covered {
		row, err := replay.Row(d)
		if err != nil {
			fmt.Printf("  %s: replay unreadable (%v) — SKIPPED, never downgraded to blind\n", d, err)
			continue
		}
		pit, err := r.Reconstruct(d, recon.ArmReplayedPIT, recon.FullFidelity)
		if err != nil {
			fmt.Printf("  %s: %v\n", d, err)
			continue
		}
		pitFunnel.Add(pit)
		pitObs = append(pitObs, study.ObserveSession(pit, cache, row.Regime,
			entryplanbacktest.RegimeReplayedPIT)...)

		blind, err := r.Reconstruct(d, recon.ArmRegimeBlind, recon.FullFidelity)
		if err == nil {
			blindFunnel.Add(blind)
			blindObs = append(blindObs, study.ObserveSession(blind, cache, model.RegimeUnknown,
				entryplanbacktest.RegimeUnavailable)...)
		}
		for j := range pit.Entries {
			symbols[pit.Entries[j].A.Symbol] = true
		}
		if (i+1)%50 == 0 {
			fmt.Printf("    %d/%d  (%.0fs, %d PIT observations)\n",
				i+1, len(covered), time.Since(t0).Seconds(), len(pitObs))
		}
	}
	a.SymbolsObserved = len(symbols)
	a.Funnels["REPLAYED_PIT"] = pitFunnel
	a.Funnels["REGIME_BLIND"] = blindFunnel

	fmt.Printf("\n── evaluating %d PIT observations over %d arms x %d wait windows ──\n",
		len(pitObs), 4, len(study.WaitWindows))
	a.PIT = study.Execute(pitObs, entryplanbacktest.RegimeReplayedPIT, recon.ReplayCaveats())
	a.PIT.Meta = metaFor(a, entryplanbacktest.ArmZoneLimit, study.WaitWindows[1], a.PIT)

	fmt.Printf("── regime-blind coverage pass (%d observations, NO METRICS by ruling) ──\n",
		len(blindObs))
	a.Blind = study.Execute(blindObs, entryplanbacktest.RegimeUnavailable, nil)

	a.ConclusionsHoldUnder = a.PIT.Holds

	// Digests, so a REPLAYED_PIT result stays re-derivable.
	rm, err := replay.HashDates(covered)
	if err == nil {
		a.ReplayDigest = rm.Digest
		writeJSON(filepath.Join(runDir, "replay_manifest.json"), rm)
		_ = replay.CopyDates(covered, filepath.Join(runDir, "market_replay"))
	}
	cm, err := hashCache(cacheDir)
	if err == nil {
		a.CacheDigest = cm.Digest
		writeJSON(filepath.Join(runDir, "cache_manifest.json"), cm)
	}
	a.PIT.ReplayDigest, a.PIT.CacheDigest, a.PIT.DigestCovers = a.ReplayDigest, a.CacheDigest, a.DigestCovers

	a.WallSeconds = time.Since(t0).Seconds()
	writeJSON(filepath.Join(runDir, "run.json"), a)
	writeCSV(filepath.Join(runDir, "metrics.csv"), a.PIT)
	summary := renderSummary(a)
	_ = os.WriteFile(filepath.Join(runDir, "SUMMARY.md"), []byte(summary), 0o644)
	fmt.Print("\n" + summary)
	fmt.Printf("\nwrote %s\n", runDir)
}

func metaFor(a *Artifact, arm entryplanbacktest.ExecutionArm, wait int, r *study.Run) entryplanbacktest.RunMetadata {
	m := entryplanbacktest.RunMetadata{
		MethodologyVersion: a.MethodologyVersion, BaselineCommit: a.BaselineCommit,
		RuleVersion: a.RuleVersion, DatasetFrom: a.DatasetFrom, DatasetTo: a.DatasetTo,
		SymbolsLoaded: a.SymbolsLoaded, SymbolsObserved: a.SymbolsObserved,
		Arm: arm, WaitSessions: wait,
		ExclusionsByReason:        map[string]int{},
		RegimeProvenanceCounts:    map[entryplanbacktest.RegimeProvenance]int{},
		ValuationProvenanceCounts: map[entryplanbacktest.ValuationProvenance]int{},
		GeneratedAt:               time.Now().UTC(),
	}
	var n int
	for _, v := range r.StatusCounts {
		n += v
	}
	m.Observations = n
	m.RegimeProvenanceCounts[r.Provenance] = n
	m.ValuationProvenanceCounts[entryplanbacktest.ValuationUnavailableHistorically] = n
	return m
}

func writeJSON(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Printf("encode %s: %v\n", path, err)
		return
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		fmt.Printf("write %s: %v\n", path, err)
	}
}

// ── cache digest (EP-10/B10) ──────────────────────────────────────────────────────────────

func hashCache(dir string) (recon.ArchiveManifest, error) {
	m := recon.ArchiveManifest{Dir: dir, Files: map[string]string{}}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return m, err
	}
	sort.Strings(files)
	roll := sha256.New()
	for _, f := range files {
		h, err := hashFile(f)
		if err != nil {
			return m, err
		}
		base := filepath.Base(f)
		m.Files[base] = h
		fmt.Fprintf(roll, "%s %s\n", base, h)
	}
	m.Digest = hex.EncodeToString(roll.Sum(nil))
	return m, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
