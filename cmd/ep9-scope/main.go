// Command ep9-scope is the EP-9 Phase 2a SCOPE MEASUREMENT.
//
// It is not the study. It reconstructs a handful of historical sessions at full fidelity,
// times them, prints the coverage funnel and the expected zeros, and writes the artifacts to
// reports/entryplanbacktest/<run>/ so a scope decision can be made on measured numbers.
//
// It writes nothing into any production archive: entryplanbacktest.ValidateOutputDir refuses
// data/research, data/market, data/market_replay, data/analysis_history, data/valuation and
// data/fundamental, and this binary checks the flag through it before doing any work.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest/recon"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"gopkg.in/yaml.v3"
)

func main() {
	log.SetFlags(0)
	var (
		cacheDir   = flag.String("cache", ".cache", "price cache directory (read-only)")
		configPath = flag.String("config", "configs/config.yaml", "scanner config (for the production filter values)")
		sectorFile = flag.String("sectors", "configs/sectors.yaml", "sector membership")
		replayDir  = flag.String("replay", marketservice.DefaultReplayDir, "regime replay archive (read-only)")
		outRoot    = flag.String("out", entryplanbacktest.DefaultOutputDir, "output root")
		sample     = flag.Int("sample", 5, "number of sessions to reconstruct for the cost measurement")
		fidelityN  = flag.Int("fidelity", 20, "number of sessions for the empty-context fidelity delta")
		stride     = flag.Int("stride", 0, "session stride; 0 = take the newest eligible sessions")
		funnelN    = flag.Int("funnel", 0, "sessions for the coverage funnel; 0 = every eligible session")
	)
	flag.Parse()

	if err := entryplanbacktest.ValidateOutputDir(*outRoot); err != nil {
		log.Fatalf("%v", err)
	}
	runDir := filepath.Join(*outRoot, time.Now().UTC().Format("20060102T150405Z")+"-scope")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		log.Fatalf("%v", err)
	}

	sc, err := loadScannerConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	// EntryPlan is off in configs/config.yaml (the key is absent, so the production default
	// false stands). The study turns it on HERE, in research code, rather than by editing the
	// production config.
	sc.EnableEntryPlan = true

	sl, err := fetcher.LoadSectorList(*sectorFile)
	if err != nil {
		log.Fatalf("sectors: %v", err)
	}

	t0 := time.Now()
	cache, err := recon.LoadCache(*cacheDir, recon.EligibilityBars)
	if err != nil {
		log.Fatalf("cache: %v", err)
	}
	loadElapsed := time.Since(t0)
	var afterLoad runtime.MemStats
	runtime.ReadMemStats(&afterLoad)

	replay, err := recon.OpenReplayArchive(*replayDir)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}

	eligible := cache.EligibleSessions(recon.EligibilityBars, entryplanbacktest.ExcursionSessions)
	covered := 0
	for _, d := range eligible {
		if replay.Covers(d) {
			covered++
		}
	}

	fmt.Printf("=== EP-9 Phase 2a scope measurement ===\n")
	fmt.Printf("baseline commit      %s\n", entryplanbacktest.BaselineCommit)
	fmt.Printf("methodology          %s\n", entryplanbacktest.MethodologyVersion)
	fmt.Printf("cache                %s  (%d symbols >= %d bars, load %.1fs, heap %.0f MB)\n",
		*cacheDir, len(cache.Symbols), recon.EligibilityBars, loadElapsed.Seconds(),
		float64(afterLoad.HeapAlloc)/(1<<20))
	fmt.Printf("session axis         %d sessions  %s .. %s\n", len(cache.Axis), cache.Axis[0], cache.Axis[len(cache.Axis)-1])
	fmt.Printf("eligible sessions    %d  (>=%d bars history, >=%d forward)  %s .. %s\n",
		len(eligible), recon.EligibilityBars, entryplanbacktest.ExcursionSessions,
		eligible[0], eligible[len(eligible)-1])
	fmt.Printf("replay archive       %d files, %d of the eligible sessions covered\n\n",
		len(replay.Dates), covered)

	r := &recon.Reconstructor{Scanner: scanner.New(sc), Cache: cache,
		Sectors: recon.NewSectorPanel(sl), Replay: replay, MinBars: recon.EligibilityBars}

	sessions := pick(eligible, *sample, *stride)
	funnelSessions := eligible
	if *funnelN > 0 {
		funnelSessions = pick(eligible, *funnelN, *stride)
	}
	report := runScope(r, replay, sessions, pick(eligible, *fidelityN, *stride), funnelSessions, runDir, sc)
	report.RunDir = runDir
	report.EligibleSessions = len(eligible)
	report.ReplayCoveredSessions = covered
	report.AxisFrom, report.AxisTo = cache.Axis[0], cache.Axis[len(cache.Axis)-1]
	report.EligibleFrom, report.EligibleTo = eligible[0], eligible[len(eligible)-1]
	report.SymbolsLoaded = len(cache.Symbols)
	report.CacheLoadSeconds = loadElapsed.Seconds()

	b, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, "scope.json"), append(b, '\n'), 0o644); err != nil {
		log.Fatalf("%v", err)
	}
	fmt.Printf("\nwrote %s\n", filepath.Join(runDir, "scope.json"))
}

// pick returns n sessions from the eligible list. stride>0 samples evenly across the whole
// range; stride==0 takes the NEWEST n, which is the conservative choice for a cost
// measurement because recent sessions have the most symbols with full history.
func pick(eligible []string, n, stride int) []string {
	if n <= 0 || len(eligible) == 0 {
		return nil
	}
	if n >= len(eligible) {
		return eligible
	}
	if stride > 0 {
		var out []string
		for i := len(eligible) - 1; i >= 0 && len(out) < n; i -= stride {
			out = append(out, eligible[i])
		}
		sort.Strings(out)
		return out
	}
	return eligible[len(eligible)-n:]
}

func loadScannerConfig(path string) (scanner.Config, error) {
	var cfg struct {
		Scanner scanner.Config `yaml:"scanner"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg.Scanner, err
	}
	err = yaml.Unmarshal(b, &cfg)
	return cfg.Scanner, err
}
