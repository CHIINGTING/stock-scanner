package macro

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The four official providers.
//
// Each is a separate function rather than a shared abstraction: they answer different
// questions, in different formats, at different frequencies, and a common interface would
// only be a place to hide those differences.
//
// Every one of them treats a non-2xx as an ERROR. A 429 that returned an empty dataset would
// say "no company reported inflation this month", which is not a thing that happens.

const (
	nyFedEFFR      = "https://markets.newyorkfed.org/api/rates/unsecured/effr/last/%d.json"
	treasuryCurve  = "https://home.treasury.gov/resource-center/data-chart-center/interest-rates/daily-treasury-rates.csv/%d/all?type=daily_treasury_yield_curve&field_tdr_date_value=%d&page&_format=csv"
	blsAPI         = "https://api.bls.gov/publicAPI/v2/timeseries/data/"
	cboeVIX        = "https://cdn.cboe.com/api/global/us_indices/daily_prices/VIX_History.csv"
	defaultTimeout = 45 * time.Second
	maxBody        = 64 << 20
)

// BLS series, each verified against the live API rather than guessed.
//
//	CES0000000001   total nonfarm employment LEVEL, thousands of persons
//	CUUR0000SA0     CPI-U, all items
//	CUUR0000SA0L1E  CPI-U, all items less food and energy — the "core" measure
//	LNS14000000     unemployment rate, percent
const (
	seriesPayrolls     = "CES0000000001"
	seriesCPI          = "CUUR0000SA0"
	seriesCoreCPI      = "CUUR0000SA0L1E"
	seriesUnemployment = "LNS14000000"
)

// endpointOverride redirects the providers in tests. Empty in every real build.
var endpointOverride map[string]string

// Provider fetches the official datasets.
type Provider struct {
	HTTP *http.Client
	Logf func(string, ...any)
}

// NewProvider builds a provider with a bounded client.
func NewProvider(logf func(string, ...any)) *Provider {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Provider{HTTP: &http.Client{Timeout: defaultTimeout}, Logf: logf}
}

func (p *Provider) url(key, actual string) string {
	if o, ok := endpointOverride[key]; ok {
		return o
	}
	return actual
}

// get performs one bounded request and returns the body.
func (p *Provider) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "stock-scanner/macro")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// ── NY Fed ────────────────────────────────────────────────────────────────────────────

// FetchFed returns the last n daily EFFR readings with their target ranges.
func (p *Provider) FetchFed(ctx context.Context, n int) (FedSnapshot, error) {
	out := FedSnapshot{Status: Unavailable}
	if n <= 0 {
		n = 60
	}
	body, err := p.get(ctx, p.url(nyFedEFFR, fmt.Sprintf(nyFedEFFR, n)))
	if err != nil {
		out.Reason = err.Error()
		return out, fmt.Errorf("macro: NY Fed: %w", err)
	}
	var payload struct {
		RefRates []struct {
			EffectiveDate  string   `json:"effectiveDate"`
			PercentRate    *float64 `json:"percentRate"`
			TargetRateFrom *float64 `json:"targetRateFrom"`
			TargetRateTo   *float64 `json:"targetRateTo"`
		} `json:"refRates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		out.Reason = err.Error()
		return out, fmt.Errorf("macro: NY Fed payload: %w", err)
	}
	if len(payload.RefRates) == 0 {
		out.Reason = "no rate observations returned"
		return out, nil
	}

	hist := make([]FedObservation, 0, len(payload.RefRates))
	for _, r := range payload.RefRates {
		if r.EffectiveDate == "" {
			continue
		}
		hist = append(hist, FedObservation{
			Date: r.EffectiveDate, EFFR: r.PercentRate,
			TargetLower: r.TargetRateFrom, TargetUpper: r.TargetRateTo,
		})
	}
	if len(hist) == 0 {
		out.Reason = "no dated observations"
		return out, nil
	}
	// The API returns newest first; store oldest first so "the previous range" is the
	// element before, not after.
	sort.Slice(hist, func(i, j int) bool { return hist[i].Date < hist[j].Date })

	latest := hist[len(hist)-1]
	out.Status = Available
	out.Date, out.EFFR = latest.Date, latest.EFFR
	out.TargetLower, out.TargetUpper = latest.TargetLower, latest.TargetUpper
	out.History = hist
	return out, nil
}

// ── U.S. Treasury ─────────────────────────────────────────────────────────────────────

// FetchTreasury returns the most recent 2Y and 10Y par yields for the given year.
func (p *Provider) FetchTreasury(ctx context.Context, year int) (TreasurySnapshot, error) {
	out := TreasurySnapshot{Status: Unavailable}
	body, err := p.get(ctx, p.url(treasuryCurve, fmt.Sprintf(treasuryCurve, year, year)))
	if err != nil {
		out.Reason = err.Error()
		return out, fmt.Errorf("macro: Treasury: %w", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) < 2 {
		out.Reason = "unreadable yield curve CSV"
		return out, fmt.Errorf("macro: Treasury CSV: %v", err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimSpace(h)] = i
	}
	i2, ok2 := col["2 Yr"]
	i10, ok10 := col["10 Yr"]
	iDate, okD := col["Date"]
	if !ok2 || !ok10 || !okD {
		out.Reason = "yield curve CSV is missing the Date, 2 Yr or 10 Yr column"
		return out, nil
	}

	// The file is newest-first; take the first row that has both tenors.
	for _, r := range rows[1:] {
		if len(r) <= i10 {
			continue
		}
		y2 := parseFloat(r[i2])
		y10 := parseFloat(r[i10])
		if y2 == nil || y10 == nil {
			continue
		}
		d := normaliseUSDate(r[iDate])
		if d == "" {
			continue
		}
		out.Status = Available
		out.Date, out.Yield2Y, out.Yield10Y = d, y2, y10
		// Spread is a subtraction, computed here so there is one answer.
		s := *y10 - *y2
		out.Spread2Y10Y = &s
		return out, nil
	}
	out.Reason = "no row carried both a 2 Yr and a 10 Yr yield"
	return out, nil
}

// ── BLS ───────────────────────────────────────────────────────────────────────────────

// blsSeries is one series' observations, newest first, as BLS returns them.
type blsSeries map[string][]blsPoint

type blsPoint struct {
	Year   int
	Month  int
	Value  float64
	Period string
}

// FetchBLS requests every needed series in ONE call.
//
// The public API accepts a list, so four metrics cost one request rather than four. It also
// bounds the blast radius: either the whole economic block arrives or none of it does, which
// is easier to reason about than three-quarters of an inflation reading.
func (p *Provider) FetchBLS(ctx context.Context, startYear, endYear int) (blsSeries, error) {
	reqBody, err := json.Marshal(map[string]any{
		"seriesid":  []string{seriesCPI, seriesCoreCPI, seriesUnemployment, seriesPayrolls},
		"startyear": strconv.Itoa(startYear),
		"endyear":   strconv.Itoa(endYear),
	})
	if err != nil {
		return nil, err
	}
	url := p.url(blsAPI, blsAPI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("macro: BLS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("macro: BLS: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}

	var payload struct {
		Status  string `json:"status"`
		Results struct {
			Series []struct {
				SeriesID string `json:"seriesID"`
				Data     []struct {
					Year   string `json:"year"`
					Period string `json:"period"`
					Value  string `json:"value"`
				} `json:"data"`
			} `json:"series"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("macro: BLS payload: %w", err)
	}
	// BLS answers 200 with a status string even for a refused request, so the body is the
	// authority, not the HTTP code.
	if !strings.EqualFold(payload.Status, "REQUEST_SUCCEEDED") {
		return nil, fmt.Errorf("macro: BLS refused the request: %s", payload.Status)
	}

	out := blsSeries{}
	for _, s := range payload.Results.Series {
		var pts []blsPoint
		for _, d := range s.Data {
			// Only monthly periods; BLS also emits annual averages as M13, and treating one
			// of those as a month would put a year's average in a month's place.
			if !strings.HasPrefix(d.Period, "M") || d.Period == "M13" {
				continue
			}
			m, err := strconv.Atoi(strings.TrimPrefix(d.Period, "M"))
			if err != nil || m < 1 || m > 12 {
				continue
			}
			y, err := strconv.Atoi(d.Year)
			if err != nil {
				continue
			}
			v := parseFloat(d.Value)
			if v == nil {
				continue
			}
			pts = append(pts, blsPoint{Year: y, Month: m, Value: *v, Period: d.Period})
		}
		sort.Slice(pts, func(i, j int) bool {
			if pts[i].Year != pts[j].Year {
				return pts[i].Year < pts[j].Year
			}
			return pts[i].Month < pts[j].Month
		})
		out[s.SeriesID] = pts
	}
	return out, nil
}

// ── CBOE ──────────────────────────────────────────────────────────────────────────────

// FetchVIX returns the most recent VIX close.
func (p *Provider) FetchVIX(ctx context.Context) (VolatilitySnapshot, error) {
	out := VolatilitySnapshot{Status: Unavailable}
	body, err := p.get(ctx, p.url(cboeVIX, cboeVIX))
	if err != nil {
		out.Reason = err.Error()
		return out, fmt.Errorf("macro: CBOE: %w", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(rows) < 2 {
		out.Reason = "unreadable VIX CSV"
		return out, fmt.Errorf("macro: VIX CSV: %v", err)
	}
	// Newest last in this file; walk back to the first usable close. A weekend or holiday
	// simply has no row — it is never filled with a zero.
	for i := len(rows) - 1; i >= 1; i-- {
		r := rows[i]
		if len(r) < 5 {
			continue
		}
		v := parseFloat(r[4])
		d := normaliseUSDate(r[0])
		if v == nil || d == "" {
			continue
		}
		out.Status = Available
		out.VIX = Observation{Date: d, Value: v}
		return out, nil
	}
	out.Reason = "no usable close in the VIX history"
	return out, nil
}

// ── shared parsing ────────────────────────────────────────────────────────────────────

// parseFloat reads a numeric cell. A blank, dash or "N/A" is MISSING, never zero: these
// sources leave a cell empty when a tenor was not auctioned or a figure was withheld, and a
// zero yield would be a policy statement rather than an absence.
func parseFloat(s string) *float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	switch s {
	case "", "-", "--", "N/A", "n/a", ".":
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// normaliseUSDate turns MM/DD/YYYY into YYYY-MM-DD, passing an ISO date through.
func normaliseUSDate(s string) string {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse("01/02/2006", s); err == nil {
		return t.Format("2006-01-02")
	}
	return ""
}
