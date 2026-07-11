package validator

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WriteSnapshotsCSV writes the parsed-but-not-yet-evaluated signals.
func WriteSnapshotsCSV(path string, snaps []SignalSnapshot) error {
	w, closeFn, err := newCSV(path)
	if err != nil {
		return err
	}
	defer closeFn()

	_ = w.Write([]string{
		"signal_date", "code", "name", "action", "action_group", "stage",
		"score", "signal_price", "reason_tags", "source_tab", "source_report",
	})
	for _, s := range snaps {
		_ = w.Write([]string{
			s.SignalDate.Format("2006-01-02"),
			s.Code, s.Name, s.Action, string(s.ActionGroup), s.Stage,
			fmtF(s.Score), fmtF(s.SignalPrice),
			strings.Join(s.ReasonTags, ";"),
			s.SourceTab, s.SourceReport,
		})
	}
	w.Flush()
	return w.Error()
}

// WriteValidationCSV writes the evaluated results. Return columns are emitted for
// every requested horizon in ascending order.
func WriteValidationCSV(path string, results []ValidationResult, horizons []int) error {
	w, closeFn, err := newCSV(path)
	if err != nil {
		return err
	}
	defer closeFn()

	hs := SortedHorizons(horizons)
	header := []string{
		"signal_date", "code", "name", "action", "action_group",
		"signal_price", "entry_date", "entry_price",
	}
	for _, h := range hs {
		header = append(header, fmt.Sprintf("return_%dd", h))
	}
	header = append(header, "max_drawdown", "max_runup", "result", "reason", "source_report")
	_ = w.Write(header)

	for _, r := range results {
		row := []string{
			r.Signal.SignalDate.Format("2006-01-02"),
			r.Signal.Code, r.Signal.Name, r.Signal.Action, string(r.Signal.ActionGroup),
			fmtF(r.Signal.SignalPrice),
			fmtDate(r.EntryDate), fmtF(r.EntryPrice),
		}
		for _, h := range hs {
			if v, ok := r.ReturnByHorizon[h]; ok {
				row = append(row, fmtF(v))
			} else {
				row = append(row, "")
			}
		}
		row = append(row, fmtF(r.MaxDrawdown), fmtF(r.MaxRunup), string(r.Result), r.Reason, r.Signal.SourceReport)
		_ = w.Write(row)
	}
	w.Flush()
	return w.Error()
}

func newCSV(path string) (*csv.Writer, func(), error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	w := csv.NewWriter(f)
	// UTF-8 BOM so Excel opens the Chinese columns correctly.
	_, _ = f.WriteString("\xEF\xBB\xBF")
	return w, func() { f.Close() }, nil
}

func fmtF(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
