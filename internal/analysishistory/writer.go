package analysishistory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultDir is where sidecar files live (under data/, not reports/).
const DefaultDir = "data/analysis_history"

// Write serializes one scan date's structured history to <dir>/<date>.json (atomic
// temp+rename). It stamps the current SchemaVersion. dir "" → DefaultDir. Returns the path.
// Pure serialization — it performs no analysis, so the report template and the structured
// history can never drift (SPEC §17).
func Write(dir string, snap Snapshot) (string, error) {
	if dir == "" {
		dir = DefaultDir
	}
	if snap.Date == "" {
		return "", fmt.Errorf("analysishistory: snapshot missing date")
	}
	snap.SchemaVersion = SchemaVersion
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("analysishistory: create dir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("analysishistory: marshal: %w", err)
	}
	path := filepath.Join(dir, snap.Date+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", fmt.Errorf("analysishistory: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("analysishistory: replace %s: %w", path, err)
	}
	return path, nil
}
