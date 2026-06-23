package etfflow

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// ──────────────────────────────────────────────────────────────────────────────
// ezMoney (統一投信) official full-holdings adapter (R8-6c).
//
// Source of truth: the issuer's own public fund page
//
//	https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=<fundCode>
//
// for an active ETF (e.g. 00981A → fundCode 49YTW). Taiwan's fund regulations require
// active ETFs to disclose their FULL portfolio DAILY (全透明), and the issuer renders
// that disclosure server-side, inlined into the page as a JSON blob on
//
//	<div id="DataAsset" data-content="[ ... ]">
//
// The JSON is an array of asset categories (NAV / OUT_UNIT / 股票(ST) / 現金(CASH) /
// 期貨(GD) …). The 股票 (AssetCode == "ST") category carries a nested "Details" array of
// every stock position with: DetailCode (股票代號), DetailName (股票名稱), Share (股數),
// NavRate (持股權重 %) and TranDate (資料日期). Because it lists EVERY constituent (not a
// top-N slice) WITH share counts and a daily as-of date, this is a genuine
// full_daily_holdings source — and crucially it is obtained with a SINGLE plain HTTP
// GET, NOT browser automation. See docs/SPEC_R8_6C_FULL_HOLDINGS_DISCOVERY.md.
//
// Constraints honoured (R8-6 spec §4.7 / hard-stop): public page only, single GET,
// honest User-Agent + timeout, no login, no captcha, no signed/private API, no
// crawler, no browser automation. The only wrinkle is a first-request session cookie
// (__nxquid) the site sets via a 302 to itself; a standard cookie jar (added to
// defaultPager) handles it the way any browser would — it is not authentication.
//
// NO network here; fetching lives in the Pager. All parsing is pinned by fixtures.
// ──────────────────────────────────────────────────────────────────────────────

// EzMoney is the HoldingsParser for the統一投信 ezMoney fund page. FundCode is the
// issuer's internal fund identifier (e.g. "49YTW" for 00981A); it is REQUIRED and is
// supplied by config, never inferred from the ETF code.
type EzMoney struct {
	FundCode string
}

// Name implements HoldingsParser.
func (EzMoney) Name() string { return "ezMoney" }

// URL is the issuer's public fund info page for the configured fundCode. The etfCode
// argument is unused: ezMoney addresses funds by their internal fundCode, not the
// listed ETF code.
func (e EzMoney) URL(string) string {
	return "https://www.ezmoney.com.tw/ETF/Fund/Info?fundCode=" + e.FundCode
}

// Coverage marks ezMoney as full_holdings: the issuer's daily disclosure lists the
// complete stock portfolio (every constituent, with share counts and an as-of date),
// so it is eligible to feed R8 flow / delta_shares. This is the ONLY auto source that
// qualifies — MoneyDJ is top_n_only, WantGoo is quarterly_composition.
func (EzMoney) Coverage() Coverage {
	return Coverage{
		Type: CoverageFullHoldings,
		Note: "ezMoney issuer page: full daily stock holdings (DataAsset → ST → Details).",
	}
}

// reEzDataAsset captures the data-content attribute of the DataAsset div. Inner quotes
// in the JSON are HTML-escaped (&quot;), so the attribute value is delimited by the
// first real double-quote — [^"]* is exact, not greedy-fragile.
var reEzDataAsset = regexp.MustCompile(`id="DataAsset"[^>]*\sdata-content="([^"]*)"`)

// ezAssetCategory is one entry of the DataAsset array. Only AssetCode == "ST" carries
// stock holdings; Details is kept as RawMessage because non-ST categories render it as
// a non-array (empty string / null), which would otherwise break a typed decode.
type ezAssetCategory struct {
	AssetCode string          `json:"AssetCode"`
	Details   json.RawMessage `json:"Details"`
}

// ezStockDetail is one stock position inside the 股票 (ST) category.
type ezStockDetail struct {
	DetailCode string  `json:"DetailCode"` // 股票代號 e.g. "2330"
	DetailName string  `json:"DetailName"` // 股票名稱 e.g. "台積電"
	Share      float64 `json:"Share"`      // 股數 (already in 股)
	NavRate    float64 `json:"NavRate"`    // 持股權重 (percent)
	TranDate   string  `json:"TranDate"`   // 資料日期 e.g. "2026-06-23T00:00:00"
}

// Parse extracts the full stock holdings and their as-of date from the ezMoney fund
// page HTML. It reads ONLY the 股票 (ST) category of DataAsset — never futures (GD),
// cash, or NAV summary rows — and returns an error rather than guess when the holdings
// blob, the ST category, the share counts, or the as-of date are absent (R8 flow needs
// real delta_shares, so a degraded snapshot is worse than none).
func (EzMoney) Parse(body string) (string, []RawHolding, error) {
	m := reEzDataAsset.FindStringSubmatch(body)
	if m == nil {
		return "", nil, fmt.Errorf("etfflow: ezMoney: DataAsset holdings blob not found")
	}
	raw := html.UnescapeString(m[1])

	var cats []ezAssetCategory
	if err := json.Unmarshal([]byte(raw), &cats); err != nil {
		return "", nil, fmt.Errorf("etfflow: ezMoney: decode DataAsset: %w", err)
	}

	var stRaw json.RawMessage
	for _, c := range cats {
		if c.AssetCode == "ST" {
			stRaw = c.Details
			break
		}
	}
	if len(stRaw) == 0 {
		return "", nil, fmt.Errorf("etfflow: ezMoney: no 股票 (ST) asset category in DataAsset")
	}

	var details []ezStockDetail
	if err := json.Unmarshal(stRaw, &details); err != nil {
		return "", nil, fmt.Errorf("etfflow: ezMoney: decode ST details: %w", err)
	}
	if len(details) == 0 {
		return "", nil, fmt.Errorf("etfflow: ezMoney: ST category has no holdings")
	}

	holdings := make([]RawHolding, 0, len(details))
	asOf := ""
	for _, d := range details {
		code := strings.TrimSpace(d.DetailCode)
		if code == "" {
			continue // skip non-stock filler rows defensively
		}
		if asOf == "" {
			parsed, err := ezAsOfDate(d.TranDate)
			if err != nil {
				return "", nil, err
			}
			asOf = parsed
		}
		holdings = append(holdings, RawHolding{
			StockCode: code,
			StockName: strings.TrimSpace(d.DetailName),
			Quantity:  d.Share,
			RawUnit:   unitShare, // ezMoney Share is already in shares (schema: 股數)
			Weight:    d.NavRate,
		})
	}
	if len(holdings) == 0 {
		return "", nil, fmt.Errorf("etfflow: ezMoney: no parseable stock holdings")
	}
	if asOf == "" {
		return "", nil, fmt.Errorf("etfflow: ezMoney: holdings present but no as-of date (TranDate)")
	}
	return asOf, holdings, nil
}

// ezAsOfDate turns ezMoney's "2026-06-23T00:00:00" (or a bare "2026-06-23") TranDate
// into the canonical YYYY-MM-DD, erroring if it is empty or unparseable.
func ezAsOfDate(tranDate string) (string, error) {
	s := strings.TrimSpace(tranDate)
	if i := strings.IndexByte(s, 'T'); i >= 0 {
		s = s[:i]
	}
	if !reEzDate.MatchString(s) {
		return "", fmt.Errorf("etfflow: ezMoney: unparseable TranDate %q", tranDate)
	}
	return s, nil
}

var reEzDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
