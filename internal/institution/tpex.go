package institution

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// tpexDailyTradeURL is the live-verified dated per-stock 三大法人 endpoint (SPEC §4). The
// openapi variant is rejected (latest-only + mangled dealer split). date is YYYY/MM/DD.
const tpexDailyTradeURL = "https://www.tpex.org.tw/www/zh-tw/insti/dailyTrade"

// TPEx column indices (0-based), from the verified 24-field POSITIONAL contract (§4).
// Field names are generic/duplicated, so position is the only anchor — reconciliation
// (§7) guards that this mapping stays correct.
const (
	tpCode        = 0
	tpName        = 1
	tpForExclBuy  = 2
	tpForExclSell = 3
	tpForExclNet  = 4
	tpForDlrBuy   = 5
	tpForDlrSell  = 6
	tpForDlrNet   = 7
	tpForTotBuy   = 8
	tpForTotSell  = 9
	tpForTotNet   = 10
	tpTrustBuy    = 11
	tpTrustSell   = 12
	tpTrustNet    = 13
	tpDlrSelfBuy  = 14
	tpDlrSelfSell = 15
	tpDlrSelfNet  = 16
	tpDlrHedBuy   = 17
	tpDlrHedSell  = 18
	tpDlrHedNet   = 19
	tpDlrTotBuy   = 20
	tpDlrTotSell  = 21
	tpDlrTotNet   = 22
	tpOfficialNet = 23
	tpNumFields   = 24
)

type tpexResponse struct {
	Stat   string `json:"stat"`
	Tables []struct {
		Fields     []string   `json:"fields"`
		Data       [][]string `json:"data"`
		Date       string     `json:"date"`
		TotalCount int        `json:"totalCount"`
	} `json:"tables"`
}

// TPEXProvider fetches the TPEx (上櫃) dailyTrade snapshot.
type TPEXProvider struct {
	client *http.Client
}

func NewTPEXProvider(timeout time.Duration) *TPEXProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &TPEXProvider{client: &http.Client{Timeout: timeout}}
}

func (p *TPEXProvider) Market() string { return MarketTPEX }

// Fetch downloads and parses the dailyTrade snapshot for date (YYYY-MM-DD). Returns
// ErrNoData on a holiday (stat "ok" but empty data — §4/§9).
func (p *TPEXProvider) Fetch(ctx context.Context, date string) (*Snapshot, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("tpex: bad date %q: %w", date, err)
	}
	url := fmt.Sprintf("%s?type=Daily&sect=EW&date=%s&response=json", tpexDailyTradeURL, d.Format("2006/01/02"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stock-scanner/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tpex: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tpex: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tpex: read body: %w", err)
	}
	return ParseTPEX(body, date, url, time.Now())
}

// ParseTPEX parses a dailyTrade response body into a Snapshot. Pure (no network).
// No-data detection uses len(data) == 0 (stat is "ok" even on holidays — §4).
func ParseTPEX(body []byte, asOfDate, sourceURL string, fetchedAt time.Time) (*Snapshot, error) {
	var r tpexResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("tpex: parse json: %w", err)
	}
	if len(r.Tables) == 0 {
		return nil, ErrNoData
	}
	tbl := r.Tables[0]
	if len(tbl.Data) == 0 || tbl.TotalCount == 0 {
		return nil, ErrNoData // holiday: stat "ok" but empty (§4)
	}

	snap := &Snapshot{
		SchemaVersion: SchemaVersion,
		Market:        MarketTPEX,
		AsOfDate:      asOfDate,
		Unit:          UnitShares,
		Source:        SourceTPEX,
		SourceURL:     sourceURL,
		FetchedAt:     fetchedAt,
	}

	for _, row := range tbl.Data {
		if len(row) < tpNumFields {
			continue
		}
		code := strings.TrimSpace(row[tpCode])
		if !isFourDigitCode(code) { // syntax prefilter only (§24)
			continue
		}
		chip, ok := parseTPEXRow(row)
		if !ok {
			log.Printf("tpex: skip malformed row %s (%s)", code, asOfDate)
			continue
		}
		snap.Stocks = append(snap.Stocks, chip)
	}
	return snap, nil
}

// parseTPEXRow maps one verified TPEx row (positional) to a StockChip. TPEx provides
// buy/sell/net for ALL seven legs, so every Buy/Sell is populated.
func parseTPEXRow(row []string) (StockChip, bool) {
	leg := func(buy, sell, net int) (Leg, bool) {
		b, ok1 := parseShares(row[buy])
		s, ok2 := parseShares(row[sell])
		n, ok3 := parseShares(row[net])
		if !ok1 || !ok2 || !ok3 {
			return Leg{}, false
		}
		return Leg{Net: n, Buy: ptr(b), Sell: ptr(s)}, true
	}
	fx, ok1 := leg(tpForExclBuy, tpForExclSell, tpForExclNet)
	fd, ok2 := leg(tpForDlrBuy, tpForDlrSell, tpForDlrNet)
	ft, ok3 := leg(tpForTotBuy, tpForTotSell, tpForTotNet)
	tr, ok4 := leg(tpTrustBuy, tpTrustSell, tpTrustNet)
	ds, ok5 := leg(tpDlrSelfBuy, tpDlrSelfSell, tpDlrSelfNet)
	dh, ok6 := leg(tpDlrHedBuy, tpDlrHedSell, tpDlrHedNet)
	dt, ok7 := leg(tpDlrTotBuy, tpDlrTotSell, tpDlrTotNet)
	off, ok8 := parseShares(row[tpOfficialNet])
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8) {
		return StockChip{}, false
	}
	c := StockChip{
		Code:              strings.TrimSpace(row[tpCode]),
		Name:              strings.TrimSpace(row[tpName]),
		ForeignExclDealer: fx,
		ForeignDealer:     fd,
		ForeignTotal:      ft,
		Trust:             tr,
		DealerSelf:        ds,
		DealerHedge:       dh,
		DealerTotal:       dt,
		OfficialTotalNet:  ptr(off),
	}
	c.Reconciled = c.reconciled()
	return c, true
}
