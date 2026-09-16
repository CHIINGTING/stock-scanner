// Command ep9-study runs the EP-9 Phase 2b outcome study.
//
// Two passes over the SAME session set:
//
//	REPLAYED_PIT   the metrics pass. Every number EP-9 reports comes from here, and every
//	               number is qualified by the replay's two biases.
//	REGIME_BLIND   a COVERAGE STATEMENT ONLY — what the committed baseline does on a session
//	               with no market snapshot, which is production's real behaviour today. No
//	               metric is computed on it, by the user's B9 ruling.
//
// It writes nothing into any production archive and nothing into the SQLite research store;
// entryplanbacktest.ValidateOutputDir refuses both and is checked before any work.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
		cacheDir   = flag.String("cache", ".cache", "price cache (read-only)")
		configPath = flag.String("config", "configs/config.yaml", "scanner config")
		sectorFile = flag.String("sectors", "configs/sectors.yaml", "sector membership")
		replayDir  = flag.String("replay", marketservice.DefaultReplayDir, "regime replay archive (read-only)")
		outRoot    = flag.String("out", entryplanbacktest.DefaultOutputDir, "output root")
		limit      = flag.Int("limit", 0, "reconstruct at most N sessions (0 = all covered); for smoke runs only")
	)
	flag.Parse()

	if err := entryplanbacktest.ValidateOutputDir(*outRoot); err != nil {
		log.Fatalf("%v", err)
	}
	runDir := filepath.Join(*outRoot, time.Now().UTC().Format("20060102T150405Z")+"-study")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		log.Fatalf("%v", err)
	}

	sc, err := loadScannerConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	sc.EnableEntryPlan = true // research override; configs/config.yaml is not touched

	sl, err := fetcher.LoadSectorList(*sectorFile)
	if err != nil {
		log.Fatalf("sectors: %v", err)
	}
	cache, err := recon.LoadCache(*cacheDir, recon.EligibilityBars)
	if err != nil {
		log.Fatalf("cache: %v", err)
	}
	replay, err := recon.OpenReplayArchive(*replayDir)
	if err != nil {
		log.Fatalf("replay: %v", err)
	}

	eligible := cache.EligibleSessions(recon.EligibilityBars, entryplanbacktest.ExcursionSessions)
	var covered, blindOnly []string
	for _, d := range eligible {
		if replay.Covers(d) {
			covered = append(covered, d)
		} else {
			blindOnly = append(blindOnly, d)
		}
	}
	if *limit > 0 && *limit < len(covered) {
		covered = covered[len(covered)-*limit:]
	}

	r := &recon.Reconstructor{Scanner: scanner.New(sc), Cache: cache,
		Sectors: recon.NewSectorPanel(sl), Replay: replay, MinBars: recon.EligibilityBars}

	fmt.Printf("=== EP-9 Phase 2b study ===\n")
	fmt.Printf("baseline commit   %s\n", entryplanbacktest.BaselineCommit)
	fmt.Printf("methodology       %s\n", entryplanbacktest.MethodologyVersion)
	fmt.Printf("symbols           %d (>= %d bars)\n", len(cache.Symbols), recon.EligibilityBars)
	fmt.Printf("eligible sessions %d; replay-covered %d; blind-only %d\n",
		len(eligible), len(covered), len(blindOnly))
	fmt.Printf("session set       %s .. %s (both passes)\n\n", covered[0], covered[len(covered)-1])

	execute(r, replay, cache, covered, blindOnly, eligible, runDir, sc, *cacheDir)
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
