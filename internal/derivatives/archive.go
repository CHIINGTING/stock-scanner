package derivatives

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The raw archive: the bytes the exchange actually sent, on disk, forever.
//
// Same shape as the valuation archive (data/<layer>/<observed-date>/<file>) because that
// contract has already survived a review round that caught a live look-ahead. It exists
// alongside the database rather than instead of it: the database holds the SESSION PARTITIONS
// a reader queries, and this holds the undivided response those partitions were derived from.
// Without it, "why does the DAY snapshot say that" is unanswerable a month later.
//
// §14: deleting these is optional and destroys the only record of what was knowable on those
// days, so nothing here ever deletes or truncates one.

// DefaultArchiveDir matches configs' derivatives.data_dir default.
const DefaultArchiveDir = "data/derivatives"

// ArchiveEntry is one preserved response.
type ArchiveEntry struct {
	// Path is where it landed.
	Path string
	// ContentHash is the sha256 of the bytes, the same hash the snapshot carries.
	ContentHash string
	// Written is false when an identical file was already there — the idempotent re-run.
	Written bool
}

// ArchiveRaw writes one response under its trading date, atomically, at a deterministic path.
//
// The filename is CONTENT-ADDRESSED: <source>.<first 16 hex of sha256>.json. That single
// choice settles three requirements that pull against each other:
//
//   - deterministic — the same bytes always land at the same path, computable without
//     consulting the database or a counter
//   - idempotent — a re-run finds the file already there and writes nothing, so a scheduler
//     firing twice costs one stat call
//   - immutable — a REVISED response has different bytes and therefore a different name, so
//     it lands beside the original instead of overwriting it. A plain <source>.json would
//     have made the second fetch of a corrected session destroy the evidence of the first,
//     which is exactly what §5 forbids and exactly what a "deterministic filename" invites.
//
// The write is atomic in the sense that matters: a temp file in the same directory, then
// rename. A crash mid-write leaves a .tmp file, never a truncated archive that reads as a
// short but valid JSON array.
func ArchiveRaw(dir, tradingDate, source string, body []byte) (ArchiveEntry, error) {
	if tradingDate == "" || source == "" {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive needs a trading date and a source")
	}
	if _, err := time.Parse("2006-01-02", tradingDate); err != nil {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive trading_date must be YYYY-MM-DD, got %q",
			tradingDate)
	}
	if dir == "" {
		dir = DefaultArchiveDir
	}
	hash := HashPayload(string(body))
	dayDir := filepath.Join(dir, tradingDate)
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive dir: %w", err)
	}
	path := filepath.Join(dayDir, archiveName(source, hash))

	if _, err := os.Stat(path); err == nil {
		// Already preserved. Not rewritten: identical content at an identical path is the
		// same observation, and rewriting it would only put a good file at risk.
		return ArchiveEntry{Path: path, ContentHash: hash, Written: false}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive stat: %w", err)
	}

	tmp, err := os.CreateTemp(dayDir, ".tmp-"+source+"-*")
	if err != nil {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has succeeded
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return ArchiveEntry{}, fmt.Errorf("derivatives: archive rename: %w", err)
	}
	return ArchiveEntry{Path: path, ContentHash: hash, Written: true}, nil
}

// archiveName is the deterministic filename. The endpoint names are long but they are the
// source identity stored on every row, so the file says the same thing the database does.
func archiveName(source, hash string) string {
	return fmt.Sprintf("%s.%s.json", source, hash[:16])
}

// ReadArchived returns the bytes preserved for one (date, source, content hash).
//
// It takes the hash rather than guessing "the latest", because "the latest" is precisely the
// question a point-in-time read is not allowed to ask of the filesystem: which revision a
// reading dated A may see is decided by §7.3 against derivative_snapshots, and the snapshot
// carries the hash. This is the byte store; the database is the index.
func ReadArchived(dir, tradingDate, source, contentHash string) ([]byte, error) {
	if len(contentHash) < 16 {
		return nil, fmt.Errorf("derivatives: content hash %q is too short", contentHash)
	}
	if _, err := hex.DecodeString(contentHash); err != nil {
		return nil, fmt.Errorf("derivatives: content hash %q is not hex", contentHash)
	}
	if dir == "" {
		dir = DefaultArchiveDir
	}
	return os.ReadFile(filepath.Join(dir, tradingDate, archiveName(source, contentHash)))
}

// ArchivedSources lists what was preserved for one trading date, sorted.
func ArchivedSources(dir, tradingDate string) ([]string, error) {
	if dir == "" {
		dir = DefaultArchiveDir
	}
	entries, err := os.ReadDir(filepath.Join(dir, tradingDate))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
