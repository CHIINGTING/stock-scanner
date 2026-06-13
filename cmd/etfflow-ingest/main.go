// Command etfflow-ingest converts a manually exported ETF holdings CSV into an R8
// snapshot JSON (data/etf_holdings/<etf>_<as_of>.json) that the scanner's R8-4 ETF
// flow can read via etfflow.LoadHistory.
//
// It is a maintenance / data-entry tool, intentionally separate from cmd/scanner so it
// never touches the main scan flow. MANUAL ingest only — no crawler, no auto-download,
// no broker, no order placement. See docs/SPEC_R8_5_MANUAL_INGEST.md.
//
//	go run ./cmd/etfflow-ingest \
//	  --etf-code 00981A --etf-name "統一台股增長主動式 ETF" --etf-type active \
//	  --date 2026-06-10 --input ./incoming/00981A_20260610.csv \
//	  --snapshot-dir data/etf_holdings --source-url "manual"
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
)

// ingestOpts holds the CLI inputs; isolating run() from flag parsing keeps it testable.
type ingestOpts struct {
	etfCode     string
	etfName     string
	etfType     string
	date        string
	input       string
	snapshotDir string
	sourceURL   string
}

func main() {
	var o ingestOpts
	flag.StringVar(&o.etfCode, "etf-code", "", "ETF 代號（如 00981A）")
	flag.StringVar(&o.etfName, "etf-name", "", "ETF 名稱（寫入 snapshot）")
	flag.StringVar(&o.etfType, "etf-type", "", "active | passive")
	flag.StringVar(&o.date, "date", "", "as_of_date，YYYY-MM-DD（持股對應交易日）")
	flag.StringVar(&o.input, "input", "", "manual holdings CSV 路徑")
	flag.StringVar(&o.snapshotDir, "snapshot-dir", "data/etf_holdings", "snapshot 輸出目錄")
	flag.StringVar(&o.sourceURL, "source-url", "manual", "provenance 字串")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "etfflow-ingest: "+err.Error())
		os.Exit(1)
	}
}

// run validates inputs, ingests the CSV into a snapshot, and persists it. now is fixed
// to time.Now() here; tests call the underlying etfflow functions for determinism.
func run(o ingestOpts) error {
	required := []struct{ name, val string }{
		{"--etf-code", o.etfCode},
		{"--etf-name", o.etfName},
		{"--etf-type", o.etfType},
		{"--date", o.date},
		{"--input", o.input},
	}
	var missing []string
	for _, r := range required {
		if strings.TrimSpace(r.val) == "" {
			missing = append(missing, r.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
	}
	if o.etfType != etfflow.TypeActive && o.etfType != etfflow.TypePassive {
		return fmt.Errorf("--etf-type must be %q or %q, got %q", etfflow.TypeActive, etfflow.TypePassive, o.etfType)
	}
	if o.snapshotDir == "" {
		o.snapshotDir = "data/etf_holdings"
	}

	snap, err := etfflow.Ingest(etfflow.CSVFileSource{Path: o.input}, etfflow.SnapshotMeta{
		ETFCode:   o.etfCode,
		ETFName:   o.etfName,
		ETFType:   o.etfType,
		AsOfDate:  o.date, // invalid dates are rejected by Snapshot.Validate inside Ingest
		SourceURL: o.sourceURL,
	}, time.Now())
	if err != nil {
		return err
	}
	if err := etfflow.Save(o.snapshotDir, snap); err != nil {
		return err
	}
	fmt.Printf("wrote %s/%s_%s.json (%d holdings)\n", o.snapshotDir, o.etfCode, o.date, len(snap.Holdings))
	return nil
}
