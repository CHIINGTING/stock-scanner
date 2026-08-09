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

// twseT86URL is the live-verified 三大法人買賣超日報 endpoint (SPEC §3). date is
// YYYYMMDD; selectType=ALL returns every security for the day (we prefilter to 4-digit).
const twseT86URL = "https://www.twse.com.tw/rwd/zh/fund/T86"

// TWSE column indices (0-based), from the verified 19-field contract (§3).
const (
	twCode          = 0
	twName          = 1
	twForExclBuy    = 2
	twForExclSell   = 3
	twForExclNet    = 4
	twForDealerBuy  = 5
	twForDealerSell = 6
	twForDealerNet  = 7
	twTrustBuy      = 8
	twTrustSell     = 9
	twTrustNet      = 10
	twDealerTotNet  = 11
	twDealerSelfBuy = 12
	twDealerSelfSel = 13
	twDealerSelfNet = 14
	twDealerHedBuy  = 15
	twDealerHedSell = 16
	twDealerHedNet  = 17
	twOfficialNet   = 18
	twNumFields     = 19
)

// twseResponse mirrors the T86 JSON. On a no-data day (holiday) stat != "OK" and
// fields/data are absent (§3).
type twseResponse struct {
	Stat   string     `json:"stat"`
	Date   string     `json:"date"`
	Fields []string   `json:"fields"`
	Data   [][]string `json:"data"`
}

// TWSEProvider fetches the TWSE (上市) T86 snapshot.
type TWSEProvider struct {
	client *http.Client
}

// NewTWSEProvider builds a provider with the given timeout (0 → 30s).
func NewTWSEProvider(timeout time.Duration) *TWSEProvider {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &TWSEProvider{client: &http.Client{Timeout: timeout}}
}

func (p *TWSEProvider) Market() string { return MarketTWSE }

// Fetch downloads and parses the T86 snapshot for date (YYYY-MM-DD). Returns ErrNoData
// on a holiday/no-data day; any other error is a transport/parse failure.
func (p *TWSEProvider) Fetch(ctx context.Context, date string) (*Snapshot, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("twse: bad date %q: %w", date, err)
	}
	url := fmt.Sprintf("%s?date=%s&selectType=ALL&response=json", twseT86URL, d.Format("20060102"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stock-scanner/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twse: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twse: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("twse: read body: %w", err)
	}
	return ParseTWSE(body, date, url, time.Now())
}

// ParseTWSE parses a T86 response body into a Snapshot. It is pure (no network) so it can
// be unit-tested against fixtures matching the verified contract. asOfDate is YYYY-MM-DD.
func ParseTWSE(body []byte, asOfDate, sourceURL string, fetchedAt time.Time) (*Snapshot, error) {
	var r twseResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("twse: parse json: %w", err)
	}
	// Valid-data gate: stat == "OK" (§3). A holiday returns "沒有符合條件"; other
	// non-OK stats (e.g. out-of-range date) are surfaced as errors so a bug isn't masked.
	if r.Stat != "OK" {
		if strings.Contains(r.Stat, "沒有符合條件") {
			return nil, ErrNoData
		}
		return nil, fmt.Errorf("twse: %s: %w", strings.TrimSpace(r.Stat), ErrNoData)
	}

	snap := &Snapshot{
		SchemaVersion: SchemaVersion,
		Market:        MarketTWSE,
		AsOfDate:      asOfDate,
		Unit:          UnitShares,
		Source:        SourceTWSE,
		SourceURL:     sourceURL,
		FetchedAt:     fetchedAt,
	}

	for _, row := range r.Data {
		if len(row) < twNumFields {
			continue // malformed row — skip, never fatal
		}
		code := strings.TrimSpace(row[twCode])
		if !isFourDigitCode(code) { // syntax prefilter only (§24)
			continue
		}
		chip, ok := parseTWSERow(row)
		if !ok {
			log.Printf("twse: skip malformed row %s (%s)", code, asOfDate)
			continue
		}
		snap.Stocks = append(snap.Stocks, chip)
	}
	return snap, nil
}

// parseTWSERow maps one verified TWSE row to a StockChip. ForeignTotal is computed
// (col4+col7); its Buy/Sell stay nil because TWSE does not provide them (§5).
func parseTWSERow(row []string) (StockChip, bool) {
	nets := make(map[int]int64, 8)
	for _, idx := range []int{twForExclNet, twForDealerNet, twTrustNet, twDealerTotNet, twDealerSelfNet, twDealerHedNet, twOfficialNet} {
		v, ok := parseShares(row[idx])
		if !ok {
			return StockChip{}, false
		}
		nets[idx] = v
	}
	optBuy := func(idx int) *int64 {
		if v, ok := parseShares(row[idx]); ok {
			return ptr(v)
		}
		return nil
	}
	c := StockChip{
		Code:              strings.TrimSpace(row[twCode]),
		Name:              strings.TrimSpace(row[twName]),
		ForeignExclDealer: Leg{Net: nets[twForExclNet], Buy: optBuy(twForExclBuy), Sell: optBuy(twForExclSell)},
		ForeignDealer:     Leg{Net: nets[twForDealerNet], Buy: optBuy(twForDealerBuy), Sell: optBuy(twForDealerSell)},
		ForeignTotal:      Leg{Net: nets[twForExclNet] + nets[twForDealerNet]}, // computed; Buy/Sell nil
		Trust:             Leg{Net: nets[twTrustNet], Buy: optBuy(twTrustBuy), Sell: optBuy(twTrustSell)},
		DealerSelf:        Leg{Net: nets[twDealerSelfNet], Buy: optBuy(twDealerSelfBuy), Sell: optBuy(twDealerSelfSel)},
		DealerHedge:       Leg{Net: nets[twDealerHedNet], Buy: optBuy(twDealerHedBuy), Sell: optBuy(twDealerHedSell)},
		DealerTotal:       Leg{Net: nets[twDealerTotNet]}, // net only; Buy/Sell nil
		OfficialTotalNet:  ptr(nets[twOfficialNet]),
	}
	c.Reconciled = c.reconciled()
	return c, true
}
