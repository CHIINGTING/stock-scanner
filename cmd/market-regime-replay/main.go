// Command market-regime-replay rebuilds the market regime for every session in the local
// price cache and writes one file per session to data/market_replay/.
//
//	go run ./cmd/market-regime-replay -dry-run                       # count and distribution only
//	go run ./cmd/market-regime-replay                                # write data/market_replay/
//	go run ./cmd/market-regime-replay -from 2025-01-01 -to 2025-12-31
//
// OFFLINE. It reads .cache and writes data/market_replay/. It CANNOT write to data/market/ at
// all: run() refuses an -out that resolves to service.DefaultSnapshotDir, because 364 replay
// files interleaved into the version-controlled live archive is the one mistake a caller
// cannot undo by re-running anything.
//
// To be precise about what that guarantee rests on: the only provider this file constructs
// is provider.CacheFeed, whose Benchmark and Universe methods read local JSON files. It does
// not construct provider.TWSE or provider.TAIFEX, and it never calls into internal/fetcher.
// net/http IS in the linked binary — internal/market/provider also contains the exchange
// clients, so importing the package for CacheFeed drags them in — but no code path reachable
// from main() uses one. Verify by reading the imports below and run(): there is no ctx, no
// timeout flag, and no NetProvider.
//
// WHAT IT PRODUCES IS NOT A LIVE SNAPSHOT, for two independent reasons.
//
// POSTURE: a replay cannot see institutional posture (no producer works retroactively), so
// institutional_posture is UNKNOWN on every row and the two decision-table rules that read
// posture (R4, R5) can never fire. There is no Market Score either.
//
// UNIVERSE: breadth is measured over the symbols in .cache RIGHT NOW, because
// CacheFeed.Universe globs *.json. That is survivorship bias against the replayed day's real
// listed universe, and it is not small: on 2026-08-31 — the only day both archives cover —
// breadth_above_ma20 was 46.65 live and 50.56 replayed, advancing_ratio 22.39 vs 30.60, while
// all ten price metrics agreed to the last decimal. Re-running this command after .cache has
// gained or lost symbols will produce different breadth for the same past sessions.
//
// A replayed regime may therefore DIFFER from the regime a live run recorded for the same
// day. See internal/market/model/regime_replay.go for why the artifact is a separate type in
// a separate directory rather than a flag on the snapshot.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/analyzer"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
	"github.com/deep-huang/stock-scanner/internal/market/service"
)

func main() {
	log.SetFlags(0)

	cacheDir := flag.String("cache-dir", ".cache", "price cache to replay from (read-only)")
	out := flag.String("out", service.DefaultReplayDir,
		"directory for regime_<date>.json files (must not be "+service.DefaultSnapshotDir+")")
	from := flag.String("from", "", "earliest session to write, YYYY-MM-DD (optional)")
	to := flag.String("to", "", "latest session to write, YYYY-MM-DD (optional)")
	dryRun := flag.Bool("dry-run", false, "replay and report, write nothing")
	flag.Parse()

	if err := run(*cacheDir, *out, *from, *to, *dryRun); err != nil {
		log.Fatalf("market-regime-replay: %v", err)
	}
}

func run(cacheDir, outDir, from, to string, dryRun bool) error {
	if err := checkDate("-from", from); err != nil {
		return err
	}
	if err := checkDate("-to", to); err != nil {
		return err
	}
	if from != "" && to != "" && from > to {
		return fmt.Errorf("-from %s is after -to %s", from, to)
	}
	if _, err := os.Stat(cacheDir); err != nil {
		return fmt.Errorf("no price cache at %s: %w", cacheDir, err)
	}
	// THE guard, checked at the point of use rather than only on the constant.
	//
	// service.TestDefaultReplayDirIsNotTheSnapshotDir asserts DefaultReplayDir != "data/market",
	// which is necessary and not sufficient: changing the flag's default here to the literal
	// "data/market" left that test — and the entire suite — green, and would have dumped 364
	// regime_*.json files into the version-controlled live archive. Nothing downstream would
	// have caught it either, since ReplayPath's regime_ prefix means a market_*.json glob just
	// silently ignores them. Hence a check on outDir itself, before anything is written.
	if err := refuseSnapshotDir(outDir); err != nil {
		return err
	}

	feed := provider.CacheFeed{Dir: cacheDir}
	th := model.StructureThresholds{}.Defaulted()

	bench := model.Benchmark0050
	bars, err := feed.Benchmark(string(bench), "")
	if err != nil {
		return fmt.Errorf("benchmark %s: %w", bench, err)
	}
	u, err := feed.Universe("")
	if err != nil {
		return fmt.Errorf("universe: %w", err)
	}
	fmt.Printf("cache          %s\n", cacheDir)
	fmt.Printf("benchmark      %s, %d bars (%s → %s)\n",
		bench, len(bars), bars[0].Date, bars[len(bars)-1].Date)
	// "as cached now" is not padding: this count is the membership the breadth numbers below
	// were computed over, and it is what changes between two re-runs of the same window.
	fmt.Printf("universe       %d symbols (as cached NOW) × %d dates (%d low-coverage dates dropped)\n",
		u.Symbols, len(u.Dates), len(u.DroppedDates))
	fmt.Printf("warm-up        first %d benchmark bars are skipped (MinBenchmarkBars)\n",
		th.MinBenchmarkBars)

	panel := analyzer.BuildBreadthPanel(u.Dates, u.Closes)
	rows := analyzer.ReplayRegimes(bench, bars, panel, th)
	if len(rows) == 0 {
		return fmt.Errorf("cache produced no replayable session (bars=%d, axis=%d)",
			len(bars), len(u.Dates))
	}

	// Window filtering happens AFTER the replay, never before: dropping bars up front would
	// shorten every moving average behind the first kept day and silently change its verdict.
	kept := rows[:0:0]
	for _, r := range rows {
		if from != "" && r.Date < from {
			continue
		}
		if to != "" && r.Date > to {
			continue
		}
		kept = append(kept, r)
	}

	fmt.Printf("replayed       %d sessions (%s → %s)\n",
		len(rows), rows[0].Date, rows[len(rows)-1].Date)
	if len(kept) == 0 {
		return fmt.Errorf("no replayed session falls inside -from %q -to %q", from, to)
	}
	if len(kept) != len(rows) {
		fmt.Printf("window         %d sessions selected (%s → %s)\n",
			len(kept), kept[0].Date, kept[len(kept)-1].Date)
	}

	printDistribution("regime distribution", func() map[string]int {
		m := map[string]int{}
		for _, r := range kept {
			m[string(r.Regime)]++
		}
		return m
	}(), len(kept))
	printDistribution("rule distribution", func() map[string]int {
		m := map[string]int{}
		for _, r := range kept {
			m[r.RuleID]++
		}
		return m
	}(), len(kept))
	printDistribution("posture distribution", func() map[string]int {
		m := map[string]int{}
		for _, r := range kept {
			m[string(r.Structure.Posture)]++
		}
		return m
	}(), len(kept))

	if dryRun {
		fmt.Printf("\ndry run        nothing written (would have written %d files to %s)\n",
			len(kept), outDir)
	} else {
		start := time.Now()
		written := 0
		for i := range kept {
			if _, err := service.SaveReplay(outDir, &kept[i]); err != nil {
				return fmt.Errorf("save %s: %w", kept[i].Date, err)
			}
			written++
		}
		fmt.Printf("\nwrote          %d files to %s/regime_<date>.json in %s\n",
			written, outDir, time.Since(start).Round(time.Millisecond))
	}

	warn()
	return nil
}

// warn is printed on every run, dry or not. The distribution above looks exactly like a
// live one, which is precisely why it needs a label attached to it in the same breath.
func warn() {
	fmt.Print(`
WARNING — these are REPLAYED regimes, not live snapshots.
  * institutional_posture is UNKNOWN on every row: nothing reconstructs 法人籌碼 history, so
    decision-table rules R4 and R5 (which read posture) can never fire in a replay.
  * there is no Market Score and no evidence list: the type has no field for either, so
    nothing was filled in with a placeholder.
  * breadth is computed over the symbols in the price cache AS IT IS NOW, not over the
    universe that was actually listed on the replayed session — CacheFeed.Universe globs
    *.json, so membership is a property of today's cache. This is survivorship bias, and it
    is measurable: on 2026-08-31 breadth_above_ma20 was 46.65 in the live snapshot and 50.56
    here (+3.91pp), advancing_ratio 22.39 vs 30.60, while every price metric matched exactly.
    BreadthQuality feeds rules R3/R6/R7/R8/R9, so on a day nearer a threshold this alone can
    change the regime. input_extent.breadth_symbols_loaded records the count used.
  * re-running this command after .cache gains or loses symbols will produce DIFFERENT breadth
    for the same past sessions. The output is reproducible from a given cache, not stable
    across time.
  * a replayed regime MAY DIFFER from what a live run recorded for the same session. Do not
    concatenate these files with data/market/market_*.json, and do not treat the two as one
    series in any study.
`)
}

func printDistribution(label string, counts map[string]int, total int) {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	fmt.Printf("\n%s (%d sessions)\n", label, total)
	for _, k := range keys {
		fmt.Printf("  %-16s %5d  %5.1f%%\n", k, counts[k], float64(counts[k])/float64(total)*100)
	}
}

func checkDate(flagName, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return fmt.Errorf("%s %q is not YYYY-MM-DD", flagName, v)
	}
	return nil
}

// refuseSnapshotDir rejects any -out that resolves to the live snapshot archive.
//
// Compared after filepath.Clean on both sides so "data/market/", "./data/market" and
// "data/market_replay/../market" are all caught; a bare string comparison would only catch
// the exact spelling. Absolute paths are resolved too, so -out $PWD/data/market fails.
func refuseSnapshotDir(outDir string) error {
	clean := filepath.Clean(outDir)
	live := filepath.Clean(service.DefaultSnapshotDir)
	same := clean == live
	if !same {
		// The relative default is relative to the process's working directory, which is the
		// repo root for `go run ./cmd/...`. Resolve both to compare an absolute -out.
		if a, err := filepath.Abs(clean); err == nil {
			if b, err := filepath.Abs(live); err == nil && a == b {
				same = true
			}
		}
	}
	if !same {
		return nil
	}
	return fmt.Errorf("-out %s is the LIVE snapshot archive. A replay has no Market Score and "+
		"UNKNOWN posture, and its breadth is computed over today's cache membership; "+
		"interleaving these files with market_*.json would make replayed history "+
		"indistinguishable from live history in a directory that IS version-controlled. "+
		"Write to %s (the default) or another directory", outDir, service.DefaultReplayDir)
}
