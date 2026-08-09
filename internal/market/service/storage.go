package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// StoragePath is <dir>/<date>.json — one file per day (date is YYYY-MM-DD). Historical
// snapshots enable charting, backtesting, and strategy validation off saved dashboards.
func StoragePath(dir, date string) string {
	return filepath.Join(dir, date+".json")
}

// Save writes the dashboard to <dir>/<date>.json (creating dir) and returns the path. Unlike
// the per-run news snapshot, the dashboard is one-per-day: a re-run overwrites the same file.
func Save(dir string, d *model.Dashboard) (string, error) {
	if d == nil {
		return "", fmt.Errorf("market: nil dashboard")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	path := StoragePath(dir, d.Date)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads back the dashboard saved for date. Used by a future charter/validator — never
// re-computing, just replaying what was observed that day.
func Load(dir, date string) (*model.Dashboard, error) {
	b, err := os.ReadFile(StoragePath(dir, date))
	if err != nil {
		return nil, err
	}
	var d model.Dashboard
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("decode %s: %w", StoragePath(dir, date), err)
	}
	return &d, nil
}
