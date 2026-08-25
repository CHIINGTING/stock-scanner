package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TWSE market-level foreign-flow historical backfill.
//
// The daily pipeline fetches BFI82U one trading date at a time into a RawSnapshot, which is
// the right shape for a dashboard and the wrong shape for research: the archive holds a
// handful of dates, and a study of foreign flow needs years of them. This file walks a date
// range and produces one SERIES, in the same spirit as taifex_backfill.go — a one-off path
// that fills the archive, after which nothing needs it again.
//
// The unit is NTD. ForeignNet is taken from the official 買賣差額 column and never derived
// as Buy − Sell: the exchange publishes its own net and the two can differ by rounding.

// ForeignFlowPoint is one trading date's market-level foreign cash flow.
type ForeignFlowPoint struct {
	Date       string  `json:"date"` // YYYY-MM-DD
	ForeignNet float64 `json:"foreign_net"`
	Buy        float64 `json:"buy"`
	Sell       float64 `json:"sell"`
}

// ForeignFlowSeries is the archived history.
type ForeignFlowSeries struct {
	Source string             `json:"source"` // "TWSE BFI82U"
	Unit   string             `json:"unit"`   // "NTD"
	Points []ForeignFlowPoint `json:"points"`
}

// foreignRowLabel is the 單位名稱 whose net is the headline foreign figure. 外資自營商 is a
// separate, much smaller row and is deliberately NOT added in: the scanner's own market
// analyzer uses 不含外資自營商 as ForeignNet, and the research must agree with it.
const foreignRowLabel = "外資及陸資(不含外資自營商)"

// FetchForeignFlowDay returns one date's figures, or (nil, nil) when the exchange had no
// data for it — a holiday is an absence, not an error, and must not abort a range walk.
func FetchForeignFlowDay(ctx context.Context, httpc *http.Client, date string) (*ForeignFlowPoint, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("twse: bad date %q: %w", date, err)
	}
	url := fmt.Sprintf("%s?dayDate=%s&type=day&response=json", twseBFI82U, d.Format("20060102"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// TWSE's rwd endpoints reject requests without a browser-like UA and a same-site
	// referer; without both they answer 200 with an empty body, which parses as "no data"
	// and would quietly backfill a year of holidays.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	req.Header.Set("Referer", "https://www.twse.com.tw/")

	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twse: %s: %w", date, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twse: %s: HTTP %d", date, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, fmt.Errorf("twse: %s: empty body (throttled or missing headers)", date)
	}

	var payload struct {
		Stat string     `json:"stat"`
		Date string     `json:"date"`
		Data [][]string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("twse: %s: parse: %w", date, err)
	}
	if !strings.EqualFold(payload.Stat, "OK") || len(payload.Data) == 0 {
		return nil, nil // holiday / no session
	}
	// The response must be for the date requested. TWSE serves the nearest trading day for
	// some holiday requests, and accepting that would date-shift the whole series.
	if payload.Date != "" && payload.Date != d.Format("20060102") {
		return nil, nil
	}

	for _, row := range payload.Data {
		if len(row) < 4 || strings.TrimSpace(row[0]) != foreignRowLabel {
			continue
		}
		net, err1 := parseTWSENumber(row[3])
		buy, _ := parseTWSENumber(row[1])
		sell, _ := parseTWSENumber(row[2])
		if err1 != nil {
			return nil, fmt.Errorf("twse: %s: unreadable net %q", date, row[3])
		}
		return &ForeignFlowPoint{Date: date, ForeignNet: net, Buy: buy, Sell: sell}, nil
	}
	return nil, fmt.Errorf("twse: %s: %q row absent — the response layout changed",
		date, foreignRowLabel)
}

// parseTWSENumber reads TWSE's comma-grouped, sometimes parenthesised numbers.
func parseTWSENumber(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	neg := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	s = strings.Trim(s, "()")
	if s == "" || s == "-" {
		return 0, fmt.Errorf("empty")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if neg {
		v = -v
	}
	return v, nil
}

// SaveForeignFlow / LoadForeignFlow archive the series, atomically and without shrinking it.
func SaveForeignFlow(dir string, s *ForeignFlowSeries) (string, error) {
	if s == nil || len(s.Points) == 0 {
		return "", fmt.Errorf("twse: refusing to save an empty foreign-flow series")
	}
	if dir == "" {
		dir = "data/market"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].Date < s.Points[j].Date })
	path := filepath.Join(dir, "foreign_flow.json")

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// LoadForeignFlow reads the archive. A missing file is an error, so "no foreign data" stays
// distinguishable from "foreign investors were flat".
func LoadForeignFlow(dir string) (*ForeignFlowSeries, error) {
	if dir == "" {
		dir = "data/market"
	}
	b, err := os.ReadFile(filepath.Join(dir, "foreign_flow.json"))
	if err != nil {
		return nil, err
	}
	var s ForeignFlowSeries
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if len(s.Points) == 0 {
		return nil, fmt.Errorf("twse: foreign-flow archive is empty")
	}
	sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].Date < s.Points[j].Date })
	return &s, nil
}
