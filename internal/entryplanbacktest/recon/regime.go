package recon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/deep-huang/stock-scanner/internal/entryplanbacktest"
	"github.com/deep-huang/stock-scanner/internal/market/model"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/scanner"
)

// The RESEARCH-LOCAL regime adapter.
//
// It exists because production has no wire from the replay archive to the entry-plan bridge:
// marketservice.LoadReplay has no non-test caller anywhere in the repo, cmd/scanner's
// loadMarketContext (main.go:789-801) reads only the LIVE snapshot store, and the
// model.RegimeReplay → entryplan.RegimeSession adapter is still the open TODO(D-3) recorded
// at internal/entryplan/doc.go:361 and internal/entryplan/series.go:297.
//
// It is deliberately NOT that adapter. It does not build an entryplan.RegimeSeries, it does
// not touch internal/entryplan, and it implements no fallback: it produces the same
// scanner.EntryPlanMarket value the production bridge already accepts, for one session, from
// one archive named by the caller. Implementing D-3 is a production change and is out of
// EP-9's scope.

// Arm names one regime treatment. The two are separate arms with separate outputs and are
// never merged in any metric.
type Arm string

const (
	// ArmRegimeBlind passes EntryPlanMarket{Available:false} — byte-identical to what
	// production does when no dashboard snapshot covers the session
	// (cmd/scanner/main.go:124-131 records the regime as UNAVAILABLE, never as neutral).
	ArmRegimeBlind Arm = "REGIME_BLIND"
	// ArmReplayedPIT passes the point-in-time replayed regime for the session.
	ArmReplayedPIT Arm = "REPLAYED_PIT"
)

// ReplayArchive is a read-only view of data/market_replay.
//
// It carries its own CONTENT HASH because a REPLAYED_PIT result is not reproducible from the
// archive's name. The .gitignore entry for data/market_replay/ says why in the repo's own
// words: breadth is computed over the price cache's current membership, so re-running the
// replay after .cache changes yields different breadth numbers and potentially a different
// regime. Hashing the files the run actually read, and copying them beside the output, is the
// only thing that makes the result re-derivable later.
type ReplayArchive struct {
	Dir string
	// Dates are the sessions the archive covers, ascending.
	Dates []string

	byDate map[string]struct{}
	rows   map[string]*model.RegimeReplay
}

// OpenReplayArchive indexes the archive directory. It does NOT decode every file: decoding is
// lazy, per session, so a scope measurement over ten sessions does not pay for 364.
func OpenReplayArchive(dir string) (*ReplayArchive, error) {
	if dir == "" {
		dir = marketservice.DefaultReplayDir
	}
	files, err := filepath.Glob(filepath.Join(dir, "regime_*.json"))
	if err != nil {
		return nil, err
	}
	a := &ReplayArchive{Dir: dir, byDate: map[string]struct{}{}, rows: map[string]*model.RegimeReplay{}}
	for _, f := range files {
		base := filepath.Base(f)
		d := base[len("regime_") : len(base)-len(".json")]
		if _, err := ParseDate(d); err != nil {
			continue
		}
		a.Dates = append(a.Dates, d)
		a.byDate[d] = struct{}{}
	}
	sort.Strings(a.Dates)
	return a, nil
}

// Covers reports whether the archive has a file for date.
func (a *ReplayArchive) Covers(date string) bool {
	if a == nil {
		return false
	}
	_, ok := a.byDate[date]
	return ok
}

// Market returns the EntryPlanMarket for one session under one arm, and the provenance that
// must be stamped on every observation produced with it.
//
// UNDER ArmReplayedPIT A MISSING OR INVALID FILE IS NOT SILENTLY DOWNGRADED TO BLIND. It
// returns UNAVAILABLE provenance and an error, so the caller either skips the session or
// records it as uncovered. A silent downgrade would put regime-blind rows inside a
// REPLAYED_PIT table, which is the mixing decision 1 forbids.
func (a *ReplayArchive) Market(arm Arm, date string) (scanner.EntryPlanMarket, entryplanbacktest.RegimeProvenance, error) {
	switch arm {
	case ArmRegimeBlind:
		// No regime is read at all. Available:false is a STATEMENT — production's own
		// encoding of "no dashboard snapshot covered this session" — not a placeholder.
		return scanner.EntryPlanMarket{Available: false}, entryplanbacktest.RegimeUnavailable, nil
	case ArmReplayedPIT:
		r, err := a.Row(date)
		if err != nil {
			return scanner.EntryPlanMarket{Available: false}, entryplanbacktest.RegimeUnavailable, err
		}
		return scanner.EntryPlanMarket{Available: true, Regime: string(r.Regime)},
			entryplanbacktest.RegimeReplayedPIT, nil
	}
	return scanner.EntryPlanMarket{}, entryplanbacktest.RegimeUnavailable,
		fmt.Errorf("recon: %q is not an arm", arm)
}

// Row decodes and caches one replayed session, through the production loader.
//
// marketservice.LoadReplay validates on read (replay_store.go:91-94), so a snapshot file
// misfiled into the replay directory, or a replay that claims an institutional posture, fails
// here rather than becoming a regime.
func (a *ReplayArchive) Row(date string) (*model.RegimeReplay, error) {
	if a == nil {
		return nil, fmt.Errorf("recon: no replay archive")
	}
	if r, ok := a.rows[date]; ok {
		return r, nil
	}
	r, err := marketservice.LoadReplay(a.Dir, date)
	if err != nil {
		return nil, err
	}
	a.rows[date] = r
	return r, nil
}

// ReplayCaveats are the two biases that must travel with every REPLAYED_PIT output. They are
// returned as data rather than written into a doc comment so a report cannot omit them by
// forgetting to copy a paragraph.
//
// Both are the archive's own, verbatim in substance: model.RegimeReplay carries them as
// mandatory caveats on every row and Validate refuses a row without them.
func ReplayCaveats() []string {
	return []string{
		"REPLAYED_REGIME_POSTURE_UNKNOWN: a replayed regime has no institutional posture and " +
			"no Market Score, so analyzer rules R4 and R5 can never fire in it " +
			"(internal/market/analyzer/regime_replay.go:20-23).",
		"REPLAY_UNIVERSE_AS_CACHED: breadth is measured over the price cache's CURRENT " +
			"membership, not the symbols listed on the replayed session (survivorship). " +
			"Measured on 2026-08-31, the one session both archives cover: breadth_above_ma20 " +
			"46.65 live vs 50.56 replayed (+3.91pp), advancing_ratio 22.39 vs 30.60 (+8.21pp), " +
			"while all ten price metrics matched exactly " +
			"(internal/market/model/regime_replay.go:49-54).",
	}
}

// ── Making a REPLAYED_PIT result re-derivable ─────────────────────────────────────────────

// ArchiveManifest is the content hash of every replay file a run read, plus a hash over those
// hashes. Sorted by date so the digest is deterministic.
type ArchiveManifest struct {
	Dir    string            `json:"dir"`
	Files  map[string]string `json:"files"` // date → sha256 of the file bytes
	Digest string            `json:"digest"`
}

// HashDates hashes the named sessions' files. It hashes what the run READ, not the whole
// directory: a run over ten sessions that recorded a digest of 364 files would fail to match
// itself the moment an unrelated 365th session was replayed.
func (a *ReplayArchive) HashDates(dates []string) (ArchiveManifest, error) {
	m := ArchiveManifest{Dir: a.Dir, Files: map[string]string{}}
	sorted := append([]string(nil), dates...)
	sort.Strings(sorted)
	roll := sha256.New()
	for _, d := range sorted {
		h, err := hashFile(marketservice.ReplayPath(a.Dir, d))
		if err != nil {
			return m, err
		}
		m.Files[d] = h
		fmt.Fprintf(roll, "%s %s\n", d, h)
	}
	m.Digest = hex.EncodeToString(roll.Sum(nil))
	return m, nil
}

// CopyDates copies the named replay files into dst, so the archive travels with the result.
func (a *ReplayArchive) CopyDates(dates []string, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, d := range dates {
		src := marketservice.ReplayPath(a.Dir, d)
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, filepath.Base(src)), b, 0o644); err != nil {
			return err
		}
	}
	return nil
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
