package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Regime-replay persistence — a SEPARATE archive from the live snapshots in snapshot_store.go.
//
// The two must never mix, because a replayed regime is not the regime a live run would have
// recorded (a replay cannot see institutional posture, so R4/R5 can never fire, and it has
// no Market Score at all). Mixing them would quietly corrupt any study built on the result.
//
// The separation is enforced in three independent places, none of which relies on a caller
// remembering anything:
//
//   - DIFFERENT DIRECTORY. DefaultReplayDir is data/market_replay, not data/market.
//   - DIFFERENT FILENAME. regime_<date>.json, so filepath.Glob("…/market_*.json") — the
//     pattern every snapshot reader uses — cannot pick a replay up even if one were
//     misfiled into data/market.
//   - DIFFERENT JSON KEYS. model.RegimeReplay persists replay_date / replayed_regime /
//     replay_schema_version, so LoadSnapshot on a replay file fails validation, and
//     LoadReplay on a snapshot file fails validation. Neither can be read as the other.
//
// Otherwise this mirrors snapshot_store.go exactly: atomic temp-file + rename so a crash
// mid-write leaves the previous file intact, and Validate() before the bytes are written so
// a malformed record never enters the archive.

// DefaultReplayDir is where cmd/market-regime-replay writes. Deliberately NOT data/market.
const DefaultReplayDir = "data/market_replay"

// DefaultSnapshotDir is the LIVE archive — the directory a replay must never be written to.
//
// It is declared here, next to DefaultReplayDir, so that the guard in
// cmd/market-regime-replay compares against a named thing instead of a string literal. The
// same path is also spelled out in model.Config.Defaulted (StorageDir), internal/research and
// internal/healthcheck; those are left alone deliberately — pulling three unrelated packages
// into this change to share one constant is a bigger edit than the guard it would serve.
// If they ever disagree, this one is the one the replay writer refuses to touch.
const DefaultSnapshotDir = "data/market"

// ReplayPath is <dir>/regime_<date>.json.
//
// The prefix is load-bearing, not cosmetic: it is what keeps a market_*.json glob from
// treating replayed history as live history.
func ReplayPath(dir, date string) string {
	return filepath.Join(dir, "regime_"+date+".json")
}

// SaveReplay validates then atomically writes r to <dir>/regime_<date>.json, returning the
// path. One file per replayed session: a re-run for the same date replaces it.
func SaveReplay(dir string, r *model.RegimeReplay) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode regime replay %s: %w", r.Date, err)
	}
	b = append(b, '\n')

	path := ReplayPath(dir, r.Date)
	tmp, err := os.CreateTemp(dir, "regime_*.json.tmp")
	if err != nil {
		return "", fmt.Errorf("temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("rename into %s: %w", path, err)
	}
	return path, nil
}

// LoadReplay reads back one replayed session. It validates on read as well: a file that has
// been hand-edited into an undefined state — or a live snapshot that got copied in here —
// must fail loudly rather than be consumed as replayed history.
func LoadReplay(dir, date string) (*model.RegimeReplay, error) {
	path := ReplayPath(dir, date)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r model.RegimeReplay
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// HasReplay reports whether a replay exists for date, without decoding it.
func HasReplay(dir, date string) bool {
	st, err := os.Stat(ReplayPath(dir, date))
	return err == nil && !st.IsDir()
}
