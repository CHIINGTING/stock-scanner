// Command candidate-research assembles the deterministic evidence the scanner already
// computes, for the hand-maintained universe in candidate.yaml.
//
// It reaches no verdict. Everything it prints is either an existing scanner value (labelled
// as such) or an availability status — there is no candidate score, rating or recommendation,
// and adding one would make this a second decision engine competing with the scan.
//
// It calls no AI and needs no API key.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/deep-huang/stock-scanner/internal/candidate"
	"github.com/deep-huang/stock-scanner/internal/fetcher"
	marketservice "github.com/deep-huang/stock-scanner/internal/market/service"
	"github.com/deep-huang/stock-scanner/internal/report"
	"github.com/deep-huang/stock-scanner/internal/research"
	"github.com/deep-huang/stock-scanner/internal/scanner"
	"gopkg.in/yaml.v3"
)

// exitConfigError separates "your candidate file is wrong" from "a stock could not be
// fetched". The first is a mistake to fix now; the second is a normal outcome of a network
// and must not fail a research run that produced results for everything else.
const exitConfigError = 2

type appConfig struct {
	Fetcher  fetcher.Config  `yaml:"fetcher"`
	Scanner  scanner.Config  `yaml:"scanner"`
	Report   report.Config   `yaml:"report"`
	Research research.Config `yaml:"research"`
}

func main() {
	log.SetFlags(log.Ltime)

	configPath := flag.String("config", "configs/config.yaml", "scanner config file")
	candidatesPath := flag.String("candidates", "candidate.yaml", "candidate research universe")
	dateStr := flag.String("date", "", "analysis date YYYY-MM-DD (default: today)")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	date := time.Now()
	if *dateStr != "" {
		if date, err = time.Parse("2006-01-02", *dateStr); err != nil {
			log.Fatalf("parse date %q: %v", *dateStr, err)
		}
	}

	list, err := candidate.Load(*candidatesPath)
	if err != nil {
		if candidate.IsNotFound(err) {
			// Not configured is not a crash. Say what is missing and how to create it.
			fmt.Fprintf(os.Stderr, "%v\n", err)
			fmt.Fprintln(os.Stderr, "建立一份 candidate.yaml，內容只需要 symbol（與選填的 note）。")
			os.Exit(exitConfigError)
		}
		fmt.Fprintf(os.Stderr, "candidate config: %v\n", err)
		os.Exit(exitConfigError)
	}

	rc := cfg.Research.Defaulted()
	marketCtx := loadMarket(rc.MarketSnapshotDir, date)
	fxCtx := candidate.LoadFXContext(rc.FXDir, date.Format("2006-01-02"))

	res := candidate.NewResolver(
		fetcher.New(cfg.Fetcher),
		scanner.New(cfg.Scanner),
		candidate.Config{
			Scanner: cfg.Scanner, ReportDate: date,
			FundamentalDir: rc.FundamentalDir,
			// Enabled whenever the archive is configured. This command is opt-in and has no
			// scanner side effects, so gating it behind a second flag would only add a way
			// to be surprised by an empty section.
			EnableFundamental: true,
			ValuationDir:      rc.ValuationDir,
			EnableValuation:   true,
			MacroDir:          rc.MacroDir,
			EnableMacro:       true,
		},
		log.Printf,
	)

	fmt.Printf("candidate research  date=%s  universe=%s (%d 檔)\n\n",
		date.Format("2006-01-02"), *candidatesPath, len(list.Candidates))

	evidence := res.Resolve(list, marketCtx, fxCtx)
	candidate.RenderText(os.Stdout, candidate.BuildViews(evidence))
}

func loadConfig(path string) (appConfig, error) {
	var cfg appConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// loadMarket reads the dashboard snapshot for the date, or reports it unavailable.
//
// The same contract R13-M3 established: a missing snapshot is "we could not look", never a
// neutral regime.
func loadMarket(dir string, date time.Time) candidate.MarketContext {
	snap, err := marketservice.LoadSnapshot(dir, date.Format("2006-01-02"))
	if err != nil || snap == nil {
		return candidate.LoadMarketContext("", nil, "")
	}
	score := snap.Score
	return candidate.LoadMarketContext(string(snap.Regime), &score, snap.Date)
}
