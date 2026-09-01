package fundamental

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// The MOPS provider.
//
// Both datasets return EVERY listed company in one response, so the whole universe costs two
// requests per market — never one per stock. That is why Fetch takes no symbol: asking per
// symbol would issue a thousand identical downloads to answer a thousand questions that one
// download already answers.

// SourceName identifies this provider in every snapshot it produces.
const SourceName = "TWSE/TPEx MOPS OpenAPI"

// Dataset endpoints, by market. Parameterless: each returns the CURRENT period only.
const (
	twseRevenue    = "https://openapi.twse.com.tw/v1/opendata/t187ap05_L"
	twseIncome     = "https://openapi.twse.com.tw/v1/opendata/t187ap06_L_ci"
	tpexRevenue    = "https://www.tpex.org.tw/openapi/v1/mopsfin_t187ap05_OA"
	tpexIncome     = "https://www.tpex.org.tw/openapi/v1/mopsfin_t187ap06_O_ci"
	defaultTimeout = 45 * time.Second
	// maxBody bounds each download. The largest observed dataset is ~1.4 MB; 32 MB leaves
	// generous headroom while still refusing a response that has gone wrong.
	maxBody = 32 << 20
)

// endpointOverride redirects the datasets in tests. Empty in every real build.
var endpointOverride map[string]string

// Dataset is one market's pair of raw responses, indexed by stock code.
type Dataset struct {
	ObservedAt time.Time
	Revenue    map[string]MonthlyRevenue
	Financials map[string]Financials
	// Errors records per-dataset failures so a caller can tell "revenue is missing for this
	// market" from "this stock has no revenue row".
	RevenueErr   error
	FinancialErr error
}

// Provider fetches the MOPS datasets.
type Provider struct {
	HTTP *http.Client
	Logf func(string, ...any)
}

// NewProvider builds a provider with a bounded HTTP client.
func NewProvider(logf func(string, ...any)) *Provider {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Provider{HTTP: &http.Client{Timeout: defaultTimeout}, Logf: logf}
}

// FetchMarket downloads both datasets for one market.
//
// A failure in one dataset does not cost the other: monthly revenue and quarterly financials
// are published on different schedules by different pipelines, and losing both because one
// endpoint was briefly unavailable would be a self-inflicted outage.
func (p *Provider) FetchMarket(ctx context.Context, market string) (*Dataset, error) {
	revURL, incURL, err := endpointsFor(market)
	if err != nil {
		return nil, err
	}
	ds := &Dataset{
		ObservedAt: time.Now(),
		Revenue:    map[string]MonthlyRevenue{},
		Financials: map[string]Financials{},
	}

	rows, err := p.getJSON(ctx, revURL)
	if err != nil {
		ds.RevenueErr = err
	} else {
		ds.Revenue, ds.RevenueErr = parseRevenueRows(rows)
	}

	rows, err = p.getJSON(ctx, incURL)
	if err != nil {
		ds.FinancialErr = err
	} else {
		ds.Financials, ds.FinancialErr = parseIncomeRows(rows)
	}

	if ds.RevenueErr != nil && ds.FinancialErr != nil {
		return ds, fmt.Errorf("fundamental: both %s datasets failed: revenue=%v financials=%v",
			market, ds.RevenueErr, ds.FinancialErr)
	}
	p.Logf("fundamental: %s — %d revenue rows, %d financial rows",
		market, len(ds.Revenue), len(ds.Financials))
	return ds, nil
}

func endpointsFor(market string) (revenue, income string, err error) {
	switch market {
	case fetcher.MarketTWSE:
		revenue, income = twseRevenue, twseIncome
	case fetcher.MarketTPEX:
		revenue, income = tpexRevenue, tpexIncome
	default:
		return "", "", fmt.Errorf("fundamental: market %q is not supported (only %s and %s)",
			market, fetcher.MarketTWSE, fetcher.MarketTPEX)
	}
	if o, ok := endpointOverride[revenue]; ok {
		revenue = o
	}
	if o, ok := endpointOverride[income]; ok {
		income = o
	}
	return revenue, income, nil
}

// getJSON downloads one dataset as a slice of string maps.
//
// MOPS returns every cell as a string, including the numbers — which is convenient here,
// because it means a blank cell stays distinguishable from a zero all the way to parseNumber.
func (p *Provider) getJSON(ctx context.Context, url string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "stock-scanner/fundamental")

	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// A non-2xx must never become an empty dataset: "the server is rate-limiting us" and
		// "no company reported this month" are different facts.
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse %s: %w", url, err)
	}
	return rows, nil
}

// field reads a cell under any of the given names.
//
// TWSE and TPEx publish the SAME dataset under different key names — 公司代號 versus
// SecuritiesCompanyCode, 年度 versus Year — so the parser accepts both rather than existing
// twice.
func field(row map[string]string, names ...string) string {
	for _, n := range names {
		if v, ok := row[n]; ok {
			return v
		}
	}
	return ""
}

func parseRevenueRows(rows []map[string]string) (map[string]MonthlyRevenue, error) {
	out := make(map[string]MonthlyRevenue, len(rows))
	var firstErr error
	for _, r := range rows {
		code := field(r, "公司代號", "SecuritiesCompanyCode")
		ym := field(r, "資料年月", "Date")
		if code == "" || ym == "" {
			continue
		}
		year, month, err := parseROCYearMonth(ym)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pub, err := parseROCDate(field(r, "出表日期", "Date"))
		if err != nil {
			// PublishedAt is the point-in-time key. A row without a usable one is not safe
			// for historical work, so it is dropped rather than dated by guesswork.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		num := func(names ...string) *float64 {
			v, err := parseNumber(field(r, names...))
			if err != nil && firstErr == nil {
				firstErr = err
			}
			return v
		}
		rec := MonthlyRevenue{
			Symbol:      code,
			Period:      Period{Year: year, Month: month},
			PublishedAt: pub,
		}
		cur := num("營業收入-當月營收")
		if cur == nil {
			continue // no current revenue is no record
		}
		rec.Revenue = Money(*cur * thousandTWD)
		if v := toMoney(num("營業收入-上月營收")); v != nil {
			rec.PriorMonth = *v
		}
		if v := toMoney(num("營業收入-去年當月營收")); v != nil {
			rec.PriorYearMonth = *v
		}
		if v := toMoney(num("累計營業收入-當月累計營收")); v != nil {
			rec.YTD = *v
		}
		if v := toMoney(num("累計營業收入-去年累計營收")); v != nil {
			rec.PriorYearYTD = *v
		}
		out[code] = rec
	}
	if len(out) == 0 && firstErr != nil {
		return out, firstErr
	}
	return out, nil
}

func parseIncomeRows(rows []map[string]string) (map[string]Financials, error) {
	out := make(map[string]Financials, len(rows))
	var firstErr error
	for _, r := range rows {
		code := field(r, "公司代號", "SecuritiesCompanyCode")
		if code == "" {
			continue
		}
		year, err := parseROCYear(field(r, "年度", "Year"))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		q, err := parseNumber(field(r, "季別", "Season"))
		if err != nil || q == nil {
			continue
		}
		pub, err := parseROCDate(field(r, "出表日期", "Date"))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		num := func(names ...string) *float64 {
			v, err := parseNumber(field(r, names...))
			if err != nil && firstErr == nil {
				firstErr = err
			}
			return v
		}
		rec := Financials{
			Symbol: code,
			// Cumulative is TRUE, always, for this dataset. Verified against the monthly
			// series — see the package doc. It is set here rather than by the caller so
			// there is no path by which a consumer receives one of these without the flag.
			Period:      Period{Year: year, Quarter: int(*q), Cumulative: true},
			PublishedAt: pub,
		}
		rev := num("營業收入")
		if rev == nil {
			continue
		}
		rec.Revenue = Money(*rev * thousandTWD)
		// 營業毛利（毛損）淨額 is the post-elimination figure and the one that matches the
		// margin the company reports; the gross 營業毛利（毛損） is the fallback.
		if v := toMoney(num("營業毛利（毛損）淨額", "營業毛利（毛損）")); v != nil {
			rec.GrossProfit = *v
		}
		if v := toMoney(num("營業利益（損失）")); v != nil {
			rec.OperatingIncome = *v
		}
		if v := toMoney(num("本期淨利（淨損）")); v != nil {
			rec.NetIncome = *v
		}
		// EPS is per share in TWD — NOT thousands. It keeps its own scale.
		rec.CumulativeEPS = num("基本每股盈餘（元）")
		out[code] = rec
	}
	if len(out) == 0 && firstErr != nil {
		return out, firstErr
	}
	return out, nil
}
