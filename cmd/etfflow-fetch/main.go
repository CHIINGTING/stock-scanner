// Command etfflow-fetch auto-fetches an ETF's LATEST publicly-disclosed holdings from a
// public page and writes it as an R8-1 snapshot JSON (data/etf_holdings/<etf>_<as_of>.json)
// for the scanner's R8 ETF flow.
//
// It is a maintenance / data-acquisition tool, intentionally separate from cmd/scanner
// so it never touches the main scan flow. Public pages only — NO login, NO captcha
// bypass, NO private API, NO browser automation, NO order placement.
//
// Sources (--source):
//   - ezmoney : 統一投信 ezMoney issuer page → FULL daily holdings (coverage full_holdings,
//     eligible for R8 flow). Needs an issuer fund code, resolved from the registry by ETF
//     code (e.g. 00981A→49YTW) or overridden with --fund-code.
//   - moneydj : MoneyDJ holdings-detail page → top-10 only (coverage top_n_only,
//     context-only; PARTIAL, never eligible for R8 flow).
//
// R8-6 coverage guardrail: a source whose coverage is NOT full daily holdings yields
// status PARTIAL and exits non-zero without writing. --allow-partial saves a TAGGED probe
// that LoadHistory still skips, so partial data can never feed R8 flow.
//
//	# ezMoney full holdings (fund code auto-resolved from the registry):
//	go run ./cmd/etfflow-fetch --source ezmoney --etf 00981A --snapshot-dir data/etf_holdings
//	# manual fund-code override:
//	go run ./cmd/etfflow-fetch --source ezmoney --etf 00981A --fund-code 49YTW
//	# fetch + validate only, do not write a snapshot:
//	go run ./cmd/etfflow-fetch --source ezmoney --etf 00981A --dry-run
//
// See docs/SPEC_R8_6D_EZMONEY_FULL_HOLDINGS_PIPELINE.md.
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
	etf          string // listed ETF code, e.g. 00981A (preferred flag)
	etfCode      string // deprecated alias for --etf
	etfName      string
	etfType      string
	source       string
	fundCode     string
	snapshotDir  string
	requireDate  string
	allowStale   bool
	allowPartial bool
	dryRun       bool
	timeout      time.Duration
}

func main() {
	var o fetchOpts
	flag.StringVar(&o.etf, "etf", "", "ETF 代號（如 00981A）")
	flag.StringVar(&o.etfCode, "etf-code", "", "（已棄用，等同 --etf）")
	flag.StringVar(&o.etfName, "etf-name", "", "ETF 名稱（預設依 registry 帶入已知值）")
	flag.StringVar(&o.etfType, "etf-type", "", "active | passive（預設依 registry 帶入已知值）")
	flag.StringVar(&o.source, "source", "ezmoney", "資料源：ezmoney（官方完整每日持股，可進 flow）| moneydj（top10，context-only）")
	flag.StringVar(&o.fundCode, "fund-code", "", "覆蓋 ezmoney 投信內部基金代號（如 49YTW）；省略則依 --etf 查 registry")
	flag.StringVar(&o.snapshotDir, "snapshot-dir", "data/etf_holdings", "snapshot 輸出目錄")
	flag.StringVar(&o.requireDate, "require-date", "", "要求的持股資料日 YYYY-MM-DD；抓到的較舊則標 STALE 且 exit≠0")
	flag.BoolVar(&o.allowStale, "allow-stale", false, "允許接受比 require-date 舊的最新資料（仍會寫檔）")
	flag.BoolVar(&o.allowPartial, "allow-partial", false, "允許寫出非完整持股（top_n_only/季度）probe；仍標記為不可進 R8 flow")
	flag.BoolVar(&o.dryRun, "dry-run", false, "只抓取 + 驗證，不寫入 snapshot")
	flag.DurationVar(&o.timeout, "timeout", 20*time.Second, "HTTP 逾時")
	flag.Parse()

	if err := run(o, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "etfflow-fetch: "+err.Error())
		os.Exit(1)
	}
}

// run resolves options against the ETF registry, picks a parser, fetches, prints the
// structured report, and returns an error (→ exit 1) for FAILED / PARTIAL-without-allow /
// STALE-without-allow-stale. It performs ALL argument/registry validation BEFORE any
// network call, so misconfiguration is caught offline.
func run(o fetchOpts, out *os.File) error {
	etf := strings.TrimSpace(o.etf)
	if etf == "" {
		etf = strings.TrimSpace(o.etfCode) // accept the deprecated alias
	}
	if etf == "" {
		return fmt.Errorf("missing required flag --etf (ETF code, e.g. 00981A)")
	}

	// Provider-agnostic name/type defaults from the registry.
	if m, ok := etfflow.LookupETF(etf, ""); ok {
		if o.etfName == "" {
			o.etfName = m.Name
		}
		if o.etfType == "" {
			o.etfType = m.Type
		}
	}

	if o.requireDate != "" {
		if _, err := time.Parse("2006-01-02", o.requireDate); err != nil {
			return fmt.Errorf("--require-date must be YYYY-MM-DD, got %q", o.requireDate)
		}
	}

	provider := strings.ToLower(strings.TrimSpace(o.source))
	var parser etfflow.HoldingsParser
	fundCode := ""
	switch provider {
	case etfflow.ProviderMoneyDJ:
		parser = etfflow.MoneyDJ{}
	case etfflow.ProviderEzMoney:
		fundCode = strings.TrimSpace(o.fundCode)
		if fundCode == "" { // resolve from registry
			if m, ok := etfflow.LookupETF(etf, etfflow.ProviderEzMoney); ok {
				fundCode = m.FundCode
			}
		}
		if fundCode == "" {
			return fmt.Errorf("no ezmoney mapping for --etf %s; add it to the registry or pass --fund-code (issuer fund id, e.g. 49YTW)", etf)
		}
		parser = etfflow.EzMoney{FundCode: fundCode}
	default:
		return fmt.Errorf("unknown --source %q (supported: ezmoney, moneydj)", o.source)
	}

	if o.etfType != etfflow.TypeActive && o.etfType != etfflow.TypePassive {
		return fmt.Errorf("--etf-type must be %q or %q (got %q); pass it explicitly for unregistered ETFs",
			etfflow.TypeActive, etfflow.TypePassive, o.etfType)
	}
	if o.etfName == "" {
		return fmt.Errorf("missing --etf-name for unregistered etf %s", etf)
	}

	f := etfflow.NewFetcher(parser, o.timeout)
	f.DryRun = o.dryRun
	res := f.Fetch(o.snapshotDir, etfflow.SnapshotMeta{
		ETFCode:  etf,
		ETFName:  o.etfName,
		ETFType:  o.etfType,
		Source:   provider,
		FundCode: fundCode,
	}, o.requireDate, o.allowStale, o.allowPartial)

	printReport(out, o, res, etf, provider, fundCode)
	return statusError(res, o)
}

// statusError maps a FetchResult to the CLI's exit decision (nil → exit 0). It is a
// pure function so the exit semantics are unit-tested offline (no network):
//   - FAILED                          → error.
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
func printReport(out *os.File, o fetchOpts, res etfflow.FetchResult, etf, provider, fundCode string) {
	fmt.Fprintf(out, "etf: %s\n", etf)
	fmt.Fprintf(out, "source: %s\n", emptyDash(provider))
	if fundCode != "" {
		fmt.Fprintf(out, "fund_code: %s\n", fundCode)
	}
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
	if v := res.Snapshot; v != nil && v.Validation != nil {
		fmt.Fprintf(out, "validation: %s holdings=%d sum_amount=%.0f category_value=%.0f matched=%t\n",
			v.Validation.StockCategory, v.Validation.HoldingCount,
			v.Validation.SumAmount, v.Validation.CategoryValue, v.Validation.AmountMatched)
	}
	switch {
	case o.dryRun:
		fmt.Fprintf(out, "output: (dry-run: not written)\n")
	case res.OutputPath != "":
		fmt.Fprintf(out, "output: %s\n", o.snapshotDir+"/"+res.OutputPath)
	default:
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
