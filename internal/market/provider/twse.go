package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// TWSE provider — the three market-level datasets the MVP takes from 臺灣證券交易所.
//
// Endpoints are the `rwd` family, not the OpenAPI one. That is not a preference: the OpenAPI
// endpoints accept no date parameter and only ever return the last few sessions, so nothing
// built on them could be backfilled or replayed. The rwd endpoints take a date and are the
// same family internal/institution already talks to successfully.
//
//	BFI82U    三大法人買賣金額統計表  → market-level foreign cash flow
//	MI_MARGN  信用交易統計            → market-level margin / short balances
//	MI_INDEX  漲跌證券數合計          → official advance/decline, SECONDARY validation only
//
// Everything here is transport and parsing. Whether -407 億 of foreign selling is bearish is
// the analyzer's problem (M6); this file has no opinion.

const (
	twseBFI82U  = "https://www.twse.com.tw/rwd/zh/fund/BFI82U"
	twseMIMargn = "https://www.twse.com.tw/rwd/zh/marginTrading/MI_MARGN"
	twseMIIndex = "https://www.twse.com.tw/rwd/zh/afterTrading/MI_INDEX"

	// The 單位名稱 row the MVP reads. 外資自營商 is reported separately and deliberately
	// excluded — it is dealer activity, not the foreign cash flow the analyzer wants.
	rowForeign = "外資及陸資(不含外資自營商)"
)

// ErrNoData means the source answered but has no trading data for that date (holiday, or
// before its history starts). Distinct from a transport failure: a holiday is not an outage,
// and the caller must not retry it or treat it as a degraded source.
var ErrNoData = errors.New("market: no trading data for date")

// ErrDateMismatch means the response is for a different trading date than requested. TWSE
// silently serves a neighbouring session for some inputs; accepting that would date-shift
// the whole snapshot, so it is refused rather than reconciled.
var ErrDateMismatch = errors.New("market: response date does not match request")

// TWSEProvider fetches the TWSE half of a RawSnapshot.
type TWSEProvider struct {
	Client *http.Client
	// BaseURLs overrides endpoints in tests. Empty entries fall back to the real ones.
	BaseURLs struct{ BFI82U, MIMargn, MIIndex string }
	// SkipBreadth omits the official advance/decline call. Breadth's primary source is the
	// scanner's own universe (see BreadthAnalyzer); this one is cross-check data, and a
	// caller that does not want a third request may drop it without losing a rule input.
	SkipBreadth bool
}

func NewTWSEProvider(timeout time.Duration) *TWSEProvider {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &TWSEProvider{Client: &http.Client{Timeout: timeout}}
}

func (p *TWSEProvider) Name() string { return "TWSE" }

func (p *TWSEProvider) url(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// Fetch collects the TWSE datasets for date (YYYY-MM-DD).
//
// Per-dataset failures are ISOLATED: each becomes a SourceMeta with status ERROR/NO_DATA and
// a nil dataset, and the rest still arrive. It returns an error only when the date itself is
// unusable, because that is the one case where a partial snapshot would be misleading rather
// than merely incomplete.
func (p *TWSEProvider) Fetch(ctx context.Context, date string) (*model.RawSnapshot, error) {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, fmt.Errorf("market: bad date %q: %w", date, err)
	}
	ymd := d.Format("20060102")
	now := time.Now()
	out := &model.RawSnapshot{Date: date, FetchedAt: now}

	// ── foreign cash ─────────────────────────────────────────────────────────────
	cashURL := fmt.Sprintf("%s?dayDate=%s&type=day&response=json", p.url(p.BaseURLs.BFI82U, twseBFI82U), ymd)
	body, err := p.get(ctx, cashURL)
	meta := model.SourceMeta{Name: "TWSE/BFI82U", URL: cashURL, FetchedAt: now}
	if err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
	} else if cash, asOf, err := ParseBFI82U(body, date); err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
	} else {
		out.Cash = cash
		meta.Status, meta.AsOfDate = model.SourceOK, asOf
		out.Sources = append(out.Sources, meta)
	}

	// ── margin ───────────────────────────────────────────────────────────────────
	marginURL := fmt.Sprintf("%s?date=%s&selectType=MS&response=json", p.url(p.BaseURLs.MIMargn, twseMIMargn), ymd)
	body, err = p.get(ctx, marginURL)
	meta = model.SourceMeta{Name: "TWSE/MI_MARGN", URL: marginURL, FetchedAt: now}
	if err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
	} else if m, asOf, err := ParseMIMargn(body, date); err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
	} else {
		out.Margin = m
		meta.Status, meta.AsOfDate = model.SourceOK, asOf
		out.Sources = append(out.Sources, meta)
	}

	// ── official breadth (secondary) ─────────────────────────────────────────────
	if p.SkipBreadth {
		out.Sources = append(out.Sources, model.SourceMeta{
			Name: "TWSE/MI_INDEX", Status: model.SourceSkipped, FetchedAt: now,
		})
		return out, nil
	}
	breadthURL := fmt.Sprintf("%s?date=%s&type=MS&response=json", p.url(p.BaseURLs.MIIndex, twseMIIndex), ymd)
	body, err = p.get(ctx, breadthURL)
	meta = model.SourceMeta{Name: "TWSE/MI_INDEX", URL: breadthURL, FetchedAt: now}
	if err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
	} else if b, asOf, err := ParseMIIndexBreadth(body, date); err != nil {
		out.Sources = append(out.Sources, failed(meta, err))
	} else {
		out.Breadth = b
		meta.Status, meta.AsOfDate = model.SourceOK, asOf
		out.Sources = append(out.Sources, meta)
	}
	return out, nil
}

func failed(m model.SourceMeta, err error) model.SourceMeta {
	m.Err = err.Error()
	if errors.Is(err, ErrNoData) {
		m.Status = model.SourceNoData
	} else {
		m.Status = model.SourceError
	}
	return m
}

func (p *TWSEProvider) get(ctx context.Context, url string) ([]byte, error) {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// TWSE rejects the default Go user agent on some of these paths.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; stock-scanner/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// ── parsers (exported so tests drive them from fixtures, never from the network) ─────

type rwdFlat struct {
	Stat   string     `json:"stat"`
	Date   string     `json:"date"`
	Fields []string   `json:"fields"`
	Data   [][]string `json:"data"`
}

type rwdTables struct {
	Stat   string `json:"stat"`
	Date   string `json:"date"`
	Tables []struct {
		Title  string     `json:"title"`
		Fields []string   `json:"fields"`
		Data   [][]string `json:"data"`
	} `json:"tables"`
}

// statusOK maps the rwd `stat` field onto our sentinels. Anything other than "OK" on these
// endpoints means "no data for this date" — TWSE returns a Chinese apology string.
func statusOK(stat string) error {
	if strings.EqualFold(strings.TrimSpace(stat), "OK") {
		return nil
	}
	return fmt.Errorf("%w: stat=%q", ErrNoData, stat)
}

// checkDate compares the response's own date against the request. The rwd endpoints echo
// `date` as YYYYMMDD; MI_MARGN also carries it in the table title as ROC.
func checkDate(got, wantISO string) (string, error) {
	want, err := time.Parse("2006-01-02", wantISO)
	if err != nil {
		return "", err
	}
	got = strings.TrimSpace(got)
	if got == "" {
		return wantISO, nil // endpoint did not echo a date; nothing to contradict
	}
	d, err := time.Parse("20060102", got)
	if err != nil {
		return "", fmt.Errorf("market: unparsable response date %q", got)
	}
	if !d.Equal(want) {
		return d.Format("2006-01-02"),
			fmt.Errorf("%w: requested %s, got %s", ErrDateMismatch, wantISO, d.Format("2006-01-02"))
	}
	return wantISO, nil
}

// ParseBFI82U extracts market-level institutional cash flow.
//
// ForeignNet comes from the official 買賣差額 column rather than Buy-Sell: the row carries
// its own net, and deriving it would paper over a column-layout change instead of surfacing
// one. Every 單位名稱 is kept in Others so a later phase needs no re-fetch.
func ParseBFI82U(body []byte, wantDate string) (*model.CashData, string, error) {
	var r rwdFlat
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, "", fmt.Errorf("BFI82U: decode: %w", err)
	}
	if err := statusOK(r.Stat); err != nil {
		return nil, "", fmt.Errorf("BFI82U: %w", err)
	}
	asOf, err := checkDate(r.Date, wantDate)
	if err != nil {
		return nil, asOf, fmt.Errorf("BFI82U: %w", err)
	}
	if len(r.Data) == 0 {
		return nil, asOf, fmt.Errorf("BFI82U: %w: empty data", ErrNoData)
	}

	out := &model.CashData{Others: map[string]float64{}}
	var foundForeign bool
	for _, row := range r.Data {
		if len(row) < 4 {
			return nil, asOf, fmt.Errorf("BFI82U: row has %d fields, want 4: %v", len(row), row)
		}
		name := strings.TrimSpace(row[0])
		net, err := parseAmount(row[3])
		if err != nil {
			return nil, asOf, fmt.Errorf("BFI82U: %s 買賣差額: %w", name, err)
		}
		out.Others[name] = net
		if name == rowForeign {
			buy, err1 := parseAmount(row[1])
			sell, err2 := parseAmount(row[2])
			if err1 != nil || err2 != nil {
				return nil, asOf, fmt.Errorf("BFI82U: 外資 buy/sell unparsable")
			}
			out.ForeignBuy, out.ForeignSell, out.ForeignNet = buy, sell, net
			foundForeign = true
		}
	}
	if !foundForeign {
		return nil, asOf, fmt.Errorf("BFI82U: row %q absent — layout changed", rowForeign)
	}
	return out, asOf, nil
}

// ParseMIMargn extracts market-level margin and short balances from 信用交易統計.
//
// The response carries both 前日餘額 and 今日餘額, so the daily change is a subtraction here
// rather than a second request against yesterday — one fewer date to get wrong.
func ParseMIMargn(body []byte, wantDate string) (*model.MarginBalanceData, string, error) {
	var r rwdTables
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, "", fmt.Errorf("MI_MARGN: decode: %w", err)
	}
	if err := statusOK(r.Stat); err != nil {
		return nil, "", fmt.Errorf("MI_MARGN: %w", err)
	}
	asOf, err := checkDate(r.Date, wantDate)
	if err != nil {
		return nil, asOf, fmt.Errorf("MI_MARGN: %w", err)
	}

	out := &model.MarginBalanceData{}
	var seenMargin, seenShort bool
	for _, tb := range r.Tables {
		for _, row := range tb.Data {
			if len(row) < 6 {
				continue
			}
			item := strings.TrimSpace(row[0])
			prev, err1 := parseAmount(row[4])
			today, err2 := parseAmount(row[5])
			if err1 != nil || err2 != nil {
				continue
			}
			switch {
			case strings.HasPrefix(item, "融資(交易單位)"):
				out.MarginPrevLots, out.MarginBalanceLots = prev, today
				seenMargin = true
			case strings.HasPrefix(item, "融券(交易單位)"):
				out.ShortPrevLots, out.ShortBalanceLots = prev, today
				seenShort = true
			case strings.HasPrefix(item, "融資金額(仟元)"):
				out.MarginPrevKTWD, out.MarginBalanceKTWD = prev, today
			}
		}
	}
	if !seenMargin || !seenShort {
		return nil, asOf, fmt.Errorf("MI_MARGN: 融資/融券 rows absent — layout changed")
	}
	return out, asOf, nil
}

// ParseMIIndexBreadth extracts the official advance/decline counts.
//
// Two traps, both load-bearing:
//   - The table is located by TITLE, never by index. The `tables` array position is not
//     contractual and has already been observed to differ between query types.
//   - The 股票 column is used, never 整體市場: the latter counts warrants and ETFs and runs
//     about ten times larger, which would make any ratio built on it meaningless.
//
// Limit-up/limit-down counts are embedded in parentheses, e.g. "497(14)".
func ParseMIIndexBreadth(body []byte, wantDate string) (*model.BreadthData, string, error) {
	var r rwdTables
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, "", fmt.Errorf("MI_INDEX: decode: %w", err)
	}
	if err := statusOK(r.Stat); err != nil {
		return nil, "", fmt.Errorf("MI_INDEX: %w", err)
	}
	asOf, err := checkDate(r.Date, wantDate)
	if err != nil {
		return nil, asOf, fmt.Errorf("MI_INDEX: %w", err)
	}

	const title = "漲跌證券數合計"
	for _, tb := range r.Tables {
		if strings.TrimSpace(tb.Title) != title {
			continue
		}
		stockCol := indexOf(tb.Fields, "股票")
		if stockCol < 0 {
			return nil, asOf, fmt.Errorf("MI_INDEX: %q has no 股票 column: %v", title, tb.Fields)
		}
		out := &model.BreadthData{}
		for _, row := range tb.Data {
			if len(row) <= stockCol {
				continue
			}
			n, limit := parseCountWithLimit(row[stockCol])
			switch {
			case strings.HasPrefix(row[0], "上漲"):
				out.Advancing, out.LimitUp = n, limit
			case strings.HasPrefix(row[0], "下跌"):
				out.Declining, out.LimitDown = n, limit
			case strings.HasPrefix(row[0], "持平"):
				out.Unchanged = n
			}
		}
		if out.Advancing == 0 && out.Declining == 0 {
			return nil, asOf, fmt.Errorf("MI_INDEX: %q parsed to zero counts — layout changed", title)
		}
		return out, asOf, nil
	}
	return nil, asOf, fmt.Errorf("MI_INDEX: table %q absent", title)
}

func indexOf(list []string, want string) int {
	for i, s := range list {
		if strings.TrimSpace(s) == want {
			return i
		}
	}
	return -1
}

// parseCountWithLimit splits "497(14)" into 497 and 14. A plain "68" yields 68 and 0.
func parseCountWithLimit(s string) (count, limit int) {
	s = strings.TrimSpace(s)
	main := s
	if i := strings.Index(s, "("); i >= 0 {
		main = s[:i]
		inner := strings.TrimSuffix(s[i+1:], ")")
		limit, _ = strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(inner), ",", ""))
	}
	count, _ = strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(main), ",", ""))
	return count, limit
}

// parseAmount reads a thousands-separated number. An empty cell is 0 — TWSE writes "" for a
// genuine zero on these rows — but any other unparsable text is an error rather than a
// silent zero, because a layout change must not look like a quiet market.
func parseAmount(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" || s == "-" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}
