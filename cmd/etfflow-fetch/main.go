// Command etfflow-fetch auto-fetches an ETF's LATEST publicly-disclosed holdings
// detail (持股明細) from a public page and writes it as an R8-1 snapshot JSON
// (data/etf_holdings/<etf>_<as_of>.json) for the scanner's R8 ETF flow.
//
// It is a maintenance / data-acquisition tool, intentionally separate from cmd/scanner
// so it never touches the main scan flow. Public pages only — NO login, NO captcha
// bypass, NO private API, NO browser automation, NO order placement. The first source
// is MoneyDJ's holdings-detail page. See docs/SPEC_R8_6B_SOURCE_DISCOVERY.md.
//
// R8-6 coverage guardrail: a source whose coverage is NOT full daily holdings (e.g.
// MoneyDJ exposes only the top 10) yields status PARTIAL and exits non-zero without
// writing. --allow-partial saves a TAGGED probe (coverage_type=top_n_only) that
// LoadHistory still skips, so partial data can never feed R8 flow.
//
//	go run ./cmd/etfflow-fetch --etf-code 00981A --snapshot-dir data/etf_holdings \
//	  --require-date 2026-06-10            # full source: fail (STALE/exit≠0) if not disclosed
//	go run ./cmd/etfflow-fetch --etf-code 00981A --allow-stale   # full source: accept latest
//	go run ./cmd/etfflow-fetch --etf-code 00981A --allow-partial # save top-10 probe (NOT flow)
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
)

type fetchOpts struct {
	etfCode      string
	etfName      string
	etfType      string
	source       string
	fundCode     string
	snapshotDir  string
	requireDate  string
	allowStale   bool
	allowPartial bool
	timeout      time.Duration
}

// knownETFs supplies sensible name/type/fundCode defaults so the common 00981A
// invocation needs no extra flags. Anything not listed must pass --etf-name /
// --etf-type explicitly (and --fund-code for the ezmoney source). fundCode is the
// issuer's internal id used by ezMoney (00981A → 49YTW).
var knownETFs = map[string]struct{ name, etfType, fundCode string }{
	"00981A": {"主動統一台股增長", etfflow.TypeActive, "49YTW"},
	"00403A": {"主動統一升級50", etfflow.TypeActive, "63YTW"},
	"00988A": {"主動統一全球創新", etfflow.TypeActive, "61YTW"},
}

func main() {
	var o fetchOpts
	flag.StringVar(&o.etfCode, "etf-code", "", "ETF 代號（如 00981A）")
	flag.StringVar(&o.etfName, "etf-name", "", "ETF 名稱（預設依 etf-code 帶入已知值）")
	flag.StringVar(&o.etfType, "etf-type", "", "active | passive（預設依 etf-code 帶入已知值）")
	flag.StringVar(&o.source, "source", "moneydj", "資料源：moneydj（top10，context-only）| ezmoney（官方完整每日持股，可進 flow）")
	flag.StringVar(&o.fundCode, "fund-code", "", "ezmoney 來源所需的投信內部基金代號（如 49YTW；00981A 已內建）")
	flag.StringVar(&o.snapshotDir, "snapshot-dir", "data/etf_holdings", "snapshot 輸出目錄")
	flag.StringVar(&o.requireDate, "require-date", "", "要求的持股明細資料日 YYYY-MM-DD；抓到的較舊則標 STALE 且 exit≠0")
	flag.BoolVar(&o.allowStale, "allow-stale", false, "允許接受比 require-date 舊的最新資料（仍會寫檔）")
	flag.BoolVar(&o.allowPartial, "allow-partial", false, "允許寫出非完整持股（top_n_only/季度）probe；仍標記為不可進 R8 flow")
	flag.DurationVar(&o.timeout, "timeout", 20*time.Second, "HTTP 逾時")
	flag.Parse()

	if err := run(o, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "etfflow-fetch: "+err.Error())
		os.Exit(1)
	}
}

// run resolves options, picks a parser, fetches, prints the structured report, and
// returns an error (→ exit 1) for FAILED or for STALE-without-allow-stale.
func run(o fetchOpts, out *os.File) error {
	if strings.TrimSpace(o.etfCode) == "" {
		return fmt.Errorf("missing required flag --etf-code")
	}
	if k, ok := knownETFs[o.etfCode]; ok {
		if o.etfName == "" {
			o.etfName = k.name
		}
		if o.etfType == "" {
			o.etfType = k.etfType
		}
		if o.fundCode == "" {
			o.fundCode = k.fundCode
		}
	}
	if o.etfType != etfflow.TypeActive && o.etfType != etfflow.TypePassive {
		return fmt.Errorf("--etf-type must be %q or %q (got %q); pass it explicitly for unknown ETFs",
			etfflow.TypeActive, etfflow.TypePassive, o.etfType)
	}
	if o.etfName == "" {
		return fmt.Errorf("missing --etf-name for unknown etf-code %s", o.etfCode)
	}
	if o.requireDate != "" {
		if _, err := time.Parse("2006-01-02", o.requireDate); err != nil {
			return fmt.Errorf("--require-date must be YYYY-MM-DD, got %q", o.requireDate)
		}
	}

	var parser etfflow.HoldingsParser
	switch strings.ToLower(o.source) {
	case "moneydj":
		parser = etfflow.MoneyDJ{}
	case "ezmoney":
		if strings.TrimSpace(o.fundCode) == "" {
			return fmt.Errorf("--source ezmoney requires --fund-code (issuer fund id, e.g. 49YTW for 00981A)")
		}
		parser = etfflow.EzMoney{FundCode: o.fundCode}
	default:
		return fmt.Errorf("unknown --source %q (supported: moneydj, ezmoney)", o.source)
	}

	f := etfflow.NewFetcher(parser, o.timeout)
	res := f.Fetch(o.snapshotDir, etfflow.SnapshotMeta{
		ETFCode: o.etfCode,
		ETFName: o.etfName,
		ETFType: o.etfType,
	}, o.requireDate, o.allowStale, o.allowPartial)

	printReport(out, o, res)
	return statusError(res, o)
}

// statusError maps a FetchResult to the CLI's exit decision (nil → exit 0). It is a
// pure function so the exit semantics are unit-tested offline (no network):
//   - FAILED                       → error.
//   - PARTIAL without --allow-partial → error (non-full source, nothing written).
//   - PARTIAL with --allow-partial    → nil (tagged probe written; NOT flow-eligible).
//   - STALE without --allow-stale     → error.
//   - STALE with --allow-stale / OK   → nil.
func statusError(res etfflow.FetchResult, o fetchOpts) error {
	switch res.Status {
	case etfflow.StatusFailed:
		if res.Err != nil {
			return res.Err
		}
		return fmt.Errorf("fetch failed")
	case etfflow.StatusPartial:
		if !o.allowPartial {
			return fmt.Errorf("source coverage is PARTIAL (%s): not full daily holdings; "+
				"refusing to write a production snapshot — pass --allow-partial to save a "+
				"context-only probe (still NOT eligible for R8 flow)", coverageType(res))
		}
	case etfflow.StatusStale:
		if !o.allowStale {
			return fmt.Errorf("holdings detail is STALE: latest disclosed %s < require-date %s",
				res.AsOfDate, o.requireDate)
		}
	}
	return nil
}

// coverageType returns the result's coverage type for messages, defaulting to a dash.
func coverageType(res etfflow.FetchResult) string {
	if res.Coverage.Type == "" {
		return "-"
	}
	return res.Coverage.Type
}

// printReport emits the spec §6 structured block.
func printReport(out *os.File, o fetchOpts, res etfflow.FetchResult) {
	fmt.Fprintf(out, "source: %s\n", res.Source)
	fmt.Fprintf(out, "source_url: %s\n", res.SourceURL)
	fmt.Fprintf(out, "as_of_date: %s\n", emptyDash(res.AsOfDate))
	if res.RequireDate != "" {
		fmt.Fprintf(out, "require_date: %s\n", res.RequireDate)
	}
	fmt.Fprintf(out, "holdings: %d\n", res.Holdings)
	fmt.Fprintf(out, "data_lag_days: %s\n", lagStr(res.DataLagDays))
	fmt.Fprintf(out, "coverage_type: %s\n", coverageType(res))
	if res.Coverage.TopN > 0 {
		fmt.Fprintf(out, "top_n: %d\n", res.Coverage.TopN)
	}
	if res.OutputPath != "" {
		fmt.Fprintf(out, "output: %s\n", o.snapshotDir+"/"+res.OutputPath)
	} else {
		fmt.Fprintf(out, "output: (none written)\n")
	}
	fmt.Fprintf(out, "status: %s\n", res.Status)
	if !res.Coverage.IsFull() {
		fmt.Fprintf(out, "WARNING: PARTIAL coverage (%s) — context only; NOT eligible for R8 flow. "+
			"LoadHistory will skip this snapshot.\n", coverageType(res))
	}
	if res.Err != nil {
		fmt.Fprintf(out, "error: %s\n", res.Err.Error())
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func lagStr(d int) string {
	if d < 0 {
		return "-"
	}
	return fmt.Sprintf("%d", d)
}
