// Command backtest-panel generates the 區間策略回測面板 — a single self-contained HTML
// page for replaying "what if I had bought here and sold there" across several
// capital-deployment rules (單筆、定期定額、逢跌狙擊、停利接回、落袋為安).
//
// It reads the scanner's existing price cache, so it needs no network access and no
// API key; prices are exactly as fresh as the last scanner run.
//
//	go run ./cmd/backtest-panel                 # 全部快取標的 → reports/ + backtest.html
//	go run ./cmd/backtest-panel -days 250       # 只嵌最近 250 個交易日, 檔案小一半
//	go run ./cmd/backtest-panel -only 2330,2317 # 只嵌指定標的
//
// The page is decision-support only: it replays historical closes, it does not predict
// and it does not place orders.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/segbacktest"
)

func main() {
	log.SetFlags(0)

	cacheDir := flag.String("cache", ".cache", "price cache directory written by the scanner")
	out := flag.String("out", "backtest.html", "output path — the stable URL served by GitHub Pages")
	// The page embeds every close it can query, so it runs a few MB. A dated archive
	// copy per day would add that much to git history every time, hence opt-in.
	archiveDir := flag.String("archive", "", "also write a dated copy into this directory (e.g. reports); empty to skip")
	days := flag.Int("days", 0, "keep only the most recent N trading days (0 = full cache history)")
	adjusted := flag.Bool("adjusted", false, "use 還原收盤價 (AdjClose) instead of raw Close")
	only := flag.String("only", "", "comma-separated stock codes to embed (default: everything cached)")
	capital := flag.Int("capital", 100000, "default 初始本金 pre-filled in the form")
	reserve := flag.Int("reserve", 0, "default 預留現金 pre-filled in the form")
	addAmount := flag.Int("add", 10000, "default 每次定期定額金額")
	addCount := flag.Int("add-count", 6, "default 定期定額次數")
	addDay := flag.Int("add-day", 5, "default 每月扣款日")
	discount := flag.Int("discount", 100, "default 手續費折數 in percent (28 = 2.8 折)")
	costs := flag.Bool("costs", true, "pre-check 計入交易成本")
	date := flag.String("date", "", "date stamp for the output filename (default today)")
	flag.Parse()

	stamp := time.Now()
	if *date != "" {
		d, err := time.Parse("2006-01-02", *date)
		if err != nil {
			log.Fatalf("backtest-panel: bad -date %q: %v", *date, err)
		}
		stamp = d
	}

	var codes []string
	if s := strings.TrimSpace(*only); s != "" {
		for _, c := range strings.Split(s, ",") {
			if c = strings.TrimSpace(c); c != "" {
				codes = append(codes, c)
			}
		}
	}

	ds, err := segbacktest.LoadDataset(segbacktest.LoadOptions{
		Dir:         *cacheDir,
		Days:        *days,
		UseAdjusted: *adjusted,
		Only:        codes,
	})
	if err != nil {
		log.Fatalf("backtest-panel: %v", err)
	}
	log.Printf("backtest-panel: %d 檔標的、%d 個交易日 (%s ~ %s)、共 %d 根收盤價",
		len(ds.Stocks), len(ds.Axis), ds.Start(), ds.End(), ds.Bars())

	opts := segbacktest.RenderOptions{
		GeneratedAt: time.Now(),
		Capital:     *capital,
		Reserve:     *reserve,
		AddAmount:   *addAmount,
		AddCount:    *addCount,
		AddDay:      *addDay,
		Discount:    *discount,
		CostsOn:     *costs,
	}

	if dir := filepath.Dir(*out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("backtest-panel: mkdir %s: %v", dir, err)
		}
	}
	if err := write(*out, ds, opts); err != nil {
		log.Fatalf("backtest-panel: %v", err)
	}
	report(*out)

	if *archiveDir != "" {
		if err := os.MkdirAll(*archiveDir, 0o755); err != nil {
			log.Fatalf("backtest-panel: mkdir %s: %v", *archiveDir, err)
		}
		dated := filepath.Join(*archiveDir, fmt.Sprintf("backtest_%s.html", stamp.Format("20060102")))
		if err := write(dated, ds, opts); err != nil {
			log.Fatalf("backtest-panel: %v", err)
		}
		report(dated)
	}
}

func write(path string, ds *segbacktest.Dataset, opts segbacktest.RenderOptions) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if err := segbacktest.Render(f, ds, opts); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	return nil
}

func report(path string) {
	if st, err := os.Stat(path); err == nil {
		log.Printf("backtest-panel: wrote %s (%.1f MB)", path, float64(st.Size())/(1<<20))
	}
}
