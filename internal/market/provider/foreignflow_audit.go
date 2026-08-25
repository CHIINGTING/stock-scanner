package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Foreign-flow coverage audit.
//
// The first attempt at this archive concluded that TWSE refuses specific historical dates,
// because 2025-03-10 answered 307 while 2024-10-15 answered 200 seconds apart. That
// conclusion was WRONG: the same dates later answered 200 with complete data. The 307 is a
// transient edge state (the responses carry x-cache: MISS, MISS, EXPIRED and no Location
// header), not a property of the date.
//
// The cost of that mistake was a research verdict — "the interaction cannot be validated" —
// built on a repairable gap. Hence this file: every attempt is classified from what the
// response actually contained, and "failed" is never a category on its own.

// FetchOutcome classifies one attempt at one date. A missing date must always land in
// exactly one of these, so a gap can never be explained away as "the API was flaky".
type FetchOutcome string

const (
	OutcomeAvailable      FetchOutcome = "AVAILABLE"       // parsed, foreign row present
	OutcomeNoSession      FetchOutcome = "NO_SESSION"      // upstream says no data (holiday)
	OutcomeNotFetched     FetchOutcome = "NOT_FETCHED"     // never attempted
	OutcomeHTTPRedirect   FetchOutcome = "HTTP_REDIRECT"   // 3xx
	OutcomeHTTPError      FetchOutcome = "HTTP_ERROR"      // 4xx / 5xx
	OutcomeEmptyResponse  FetchOutcome = "EMPTY_RESPONSE"  // 200, zero-length body
	OutcomeParserFailure  FetchOutcome = "PARSER_FAILURE"  // 200, body present, not JSON
	OutcomeInvalidPayload FetchOutcome = "INVALID_PAYLOAD" // JSON, but wrong date or no foreign row
	OutcomeTransport      FetchOutcome = "TRANSPORT_ERROR" // connection / timeout
	OutcomeUnknown        FetchOutcome = "UNKNOWN"
)

// FetchDiagnostic is everything one attempt observed. Recorded in full because "failed" is
// not a diagnosis — an audit has to be able to distinguish a holiday from a blocked edge
// from a layout change months after the fact.
type FetchDiagnostic struct {
	Date        string       `json:"date"`
	Outcome     FetchOutcome `json:"outcome"`
	HTTPStatus  int          `json:"http_status,omitempty"`
	HasLocation bool         `json:"has_location,omitempty"`
	Location    string       `json:"location,omitempty"`
	BodyBytes   int          `json:"body_bytes"`
	Attempts    int          `json:"attempts"`
	Err         string       `json:"err,omitempty"`
	ObservedAt  time.Time    `json:"observed_at"`
}

// FetchForeignFlowDiagnosed fetches one date and reports exactly what happened.
//
// It retries only on outcomes that are known to be transient (3xx, 5xx, transport, empty
// body). It does NOT retry a genuine "no session" or an invalid payload: repeating a request
// whose answer is a fact wastes the endpoint's patience and hides the fact.
func FetchForeignFlowDiagnosed(ctx context.Context, httpc *http.Client, date string,
	maxAttempts int, backoff time.Duration) (*ForeignFlowPoint, FetchDiagnostic) {

	diag := FetchDiagnostic{Date: date, Outcome: OutcomeUnknown, ObservedAt: time.Now()}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		diag.Outcome, diag.Err = OutcomeUnknown, err.Error()
		return nil, diag
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	url := fmt.Sprintf("%s?dayDate=%s&type=day&response=json", twseBFI82U, d.Format("20060102"))

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		diag.Attempts = attempt
		pt, retry := attemptForeignFlow(ctx, httpc, url, date, d, &diag)
		if !retry {
			return pt, diag
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				diag.Outcome, diag.Err = OutcomeTransport, ctx.Err().Error()
				return nil, diag
			case <-time.After(backoff * time.Duration(attempt)):
			}
		}
	}
	return nil, diag
}

// attemptForeignFlow performs one request. The bool says whether the outcome is worth
// retrying — true only for states that are genuinely transient.
func attemptForeignFlow(ctx context.Context, httpc *http.Client, url, date string,
	d time.Time, diag *FetchDiagnostic) (*ForeignFlowPoint, bool) {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		diag.Outcome, diag.Err = OutcomeUnknown, err.Error()
		return nil, false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	req.Header.Set("Referer", "https://www.twse.com.tw/")

	resp, err := httpc.Do(req)
	if err != nil {
		diag.Outcome, diag.Err = OutcomeTransport, err.Error()
		return nil, true
	}
	defer resp.Body.Close()

	diag.HTTPStatus = resp.StatusCode
	if loc := resp.Header.Get("Location"); loc != "" {
		diag.HasLocation, diag.Location = true, loc
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	diag.BodyBytes = len(body)
	if err != nil {
		diag.Outcome, diag.Err = OutcomeTransport, err.Error()
		return nil, true
	}

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		diag.Outcome = OutcomeHTTPRedirect
		return nil, true // transient edge state — the same date answers 200 later
	case resp.StatusCode >= 400:
		diag.Outcome = OutcomeHTTPError
		return nil, resp.StatusCode >= 500
	}

	if len(strings.TrimSpace(string(body))) == 0 {
		diag.Outcome = OutcomeEmptyResponse
		return nil, true
	}

	var payload struct {
		Stat string     `json:"stat"`
		Date string     `json:"date"`
		Data [][]string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		diag.Outcome, diag.Err = OutcomeParserFailure, err.Error()
		return nil, false
	}
	if !strings.EqualFold(payload.Stat, "OK") || len(payload.Data) == 0 {
		diag.Outcome = OutcomeNoSession
		return nil, false
	}
	// The response must be for the date requested; TWSE serves the nearest trading day for
	// some holiday requests and accepting that would date-shift the archive.
	if payload.Date != "" && payload.Date != d.Format("20060102") {
		diag.Outcome, diag.Err = OutcomeInvalidPayload,
			fmt.Sprintf("response is for %s", payload.Date)
		return nil, false
	}

	for _, row := range payload.Data {
		if len(row) < 4 || strings.TrimSpace(row[0]) != foreignRowLabel {
			continue
		}
		net, err1 := parseTWSENumber(row[3])
		buy, _ := parseTWSENumber(row[1])
		sell, _ := parseTWSENumber(row[2])
		if err1 != nil {
			diag.Outcome, diag.Err = OutcomeInvalidPayload, "unreadable net "+row[3]
			return nil, false
		}
		diag.Outcome = OutcomeAvailable
		return &ForeignFlowPoint{Date: date, ForeignNet: net, Buy: buy, Sell: sell}, false
	}
	diag.Outcome, diag.Err = OutcomeInvalidPayload, foreignRowLabel+" row absent"
	return nil, false
}

// ── coverage audit ────────────────────────────────────────────────────────────────────

// CoverageAudit compares an expected trading calendar against the archive.
type CoverageAudit struct {
	Expected  int
	Available int
	Missing   []string
	// ByOutcome counts what the diagnostics said about each missing date. NOT_FETCHED means
	// no attempt was ever recorded — the honest answer when a gap has simply never been
	// investigated.
	ByOutcome map[FetchOutcome]int
	ByMonth   map[string]struct{ Have, Miss int }
}

// AuditCoverage classifies every expected trading date.
func AuditCoverage(expected []string, series *ForeignFlowSeries, diags map[string]FetchDiagnostic) CoverageAudit {
	have := map[string]bool{}
	if series != nil {
		for _, p := range series.Points {
			have[p.Date] = true
		}
	}
	a := CoverageAudit{
		Expected:  len(expected),
		ByOutcome: map[FetchOutcome]int{},
		ByMonth:   map[string]struct{ Have, Miss int }{},
	}
	for _, d := range expected {
		m := d[:7]
		e := a.ByMonth[m]
		if have[d] {
			a.Available++
			e.Have++
		} else {
			a.Missing = append(a.Missing, d)
			e.Miss++
			out := OutcomeNotFetched
			if dg, ok := diags[d]; ok {
				out = dg.Outcome
			}
			a.ByOutcome[out]++
		}
		a.ByMonth[m] = e
	}
	sort.Strings(a.Missing)
	return a
}
