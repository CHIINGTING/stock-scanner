package valuation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Historical valuation, from the exchanges' OWN per-date endpoints.
//
// The live datasets this package started with take no parameters and serve only the current
// session, which is why history had to accrue one day at a time. That was a limitation of
// the ENDPOINTS CHOSEN, not of the source: both exchanges publish per-date ratios, and this
// file uses them.
//
//	TWSE  one stock, one month per request
//	      /rwd/zh/afterTrading/BWIBBU?date=YYYYMM01&stockNo=2330&response=json
//	TPEx  one session, the whole market per request
//	      .../peratio_analysis/pera_result.php?l=zh-tw&d=<ROC date>
//
// The shapes are opposite, so the two are fetched differently and the caller pays a very
// different request count for each. Neither is scraped: both return JSON from an official
// endpoint, with no access control to work around.
//
// # What this does NOT change
//
// A record fetched here is stamped Backfilled with the real FetchedAt. It is archived under
// the day the BACKFILL RAN, never under the session it describes, so LoadHistory's
// point-in-time bound is untouched: a check dated before the backfill still cannot see it.
// Relaxing that is a separate design (OFFICIAL_HISTORICAL_REPLAY) and is deliberately not
// attempted here.

const (
	twseHistoricalURL = "https://www.twse.com.tw/rwd/zh/afterTrading/BWIBBU"
	tpexHistoricalURL = "https://www.tpex.org.tw/web/stock/aftertrading/peratio_analysis/pera_result.php"
)

// HistoricalProvider fetches per-session ratios from the exchanges' historical endpoints.
//
// Throttle bounds the request rate. The exchanges are a shared public resource and a
// backfill is the one operation here that issues hundreds of requests, so the delay is
// applied BEFORE every call rather than being left to the caller to remember.
type HistoricalProvider struct {
	Client   *http.Client
	Throttle time.Duration
	// Retries is how many times a failed request is retried, with exponential backoff.
	Retries int
	// RateLimitBackoff is the BASE wait after a rate-limit refusal, doubled per attempt.
	// Zero → 30s. It is separate from the ordinary backoff because the two failures need
	// waits an order of magnitude apart.
	RateLimitBackoff time.Duration
	Logf             func(string, ...any)

	twseURL, tpexURL string
	last             time.Time
}

// NewHistoricalProvider builds a provider with conservative defaults.
func NewHistoricalProvider(logf func(string, ...any)) *HistoricalProvider {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &HistoricalProvider{
		Client:   &http.Client{Timeout: 30 * time.Second},
		Throttle: 900 * time.Millisecond,
		Retries:  3,
		Logf:     logf,
		twseURL:  twseHistoricalURL,
		tpexURL:  tpexHistoricalURL,
	}
}

// MonthsBack lists the first-of-month dates covering the last n months, oldest first.
//
// Deterministic: derived from the caller's `now` with the day pinned to the 1st, so a run at
// any hour of any day of the month asks for exactly the same months.
func MonthsBack(now time.Time, n int) []time.Time {
	if n <= 0 {
		return nil
	}
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	out := make([]time.Time, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, first.AddDate(0, -i, 0))
	}
	return out
}

// TWSEMonth fetches one stock's ratios for the calendar month containing `month`.
func (p *HistoricalProvider) TWSEMonth(ctx context.Context, code string, month time.Time) ([]Ratios, error) {
	q := fmt.Sprintf("?date=%s&stockNo=%s&response=json", month.Format("20060102"), code)
	body, err := p.get(ctx, p.twseURL+q)
	if err != nil {
		return nil, err
	}
	var env struct {
		Stat   string              `json:"stat"`
		Fields []string            `json:"fields"`
		Data   [][]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("twse %s %s: decode: %w", code, month.Format("2006-01"), err)
	}
	if !strings.EqualFold(env.Stat, "ok") {
		// "很抱歉，沒有符合條件的資料" for a month with no sessions is NOT an error: a
		// newly listed stock genuinely has none, and failing the whole backfill over it
		// would lose every month that did work.
		return nil, nil
	}
	idx := fieldIndex(env.Fields, map[string]string{
		"date": "日期", "pe": "本益比", "pb": "股價淨值比", "yield": "殖利率",
	})
	// An unresolvable P/E column is a PARSE FAILURE, not a month of companies with no
	// earnings.
	//
	// This distinction did not exist before FU-11. A nil P/E used to mean only "no sample",
	// and a row carrying one was harmless — it was dropped from the distribution and nothing
	// else read it. FU-11 made an absent P/E a FINANCIAL FACT: the exchange withholds the
	// ratio precisely when trailing earnings are non-positive, so a nil now says "this
	// company was not profitable that session".
	//
	// A renamed header would therefore manufacture that fact out of a schema change. One bad
	// month inside a good series reads as P P [renamed month] P P → INTERMITTENT →
	// "the earnings denominator crossed zero", about a company that never did.
	//
	// So it fails loudly. Losing a month to an error a human will see is recoverable; writing
	// a false earnings history into the archive is not, because nothing downstream can tell
	// afterwards which nils were real.
	if idx["pe"] < 0 {
		return nil, fmt.Errorf("twse %s %s: no 本益比 column in %v — refusing to archive rows "+
			"whose missing P/E would read as non-positive earnings", code,
			month.Format("2006-01"), env.Fields)
	}
	out := make([]Ratios, 0, len(env.Data))
	for _, row := range env.Data {
		date, err := rocRowDate(pick(row, idx["date"]))
		if err != nil {
			continue // a row whose date cannot be placed in time is unusable, not zero
		}
		out = append(out, Ratios{
			Symbol:        code,
			Date:          date,
			PERatio:       ratio(pick(row, idx["pe"])),
			PBRatio:       ratio(pick(row, idx["pb"])),
			DividendYield: ratio(pick(row, idx["yield"])),
			Backfilled:    true,
			Source:        SourceTWSEHistorical,
		})
	}
	return out, nil
}

// TPEXSession fetches EVERY listed stock's ratios for one trading session.
//
// The whole market comes back in one response, so a caller backfilling several stocks should
// fetch each session once and fan the result out — see BackfillTPEX.
func (p *HistoricalProvider) TPEXSession(ctx context.Context, day time.Time) (map[string]Ratios, error) {
	q := fmt.Sprintf("?l=zh-tw&d=%s&c=&s=0,asc", rocDate(day))
	body, err := p.get(ctx, p.tpexURL+q)
	if err != nil {
		return nil, err
	}
	var env struct {
		Tables []struct {
			Date       string              `json:"date"`
			Fields     []string            `json:"fields"`
			Data       [][]json.RawMessage `json:"data"`
			TotalCount int                 `json:"totalCount"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("tpex %s: decode: %w", day.Format("2006-01-02"), err)
	}
	if len(env.Tables) == 0 || len(env.Tables[0].Data) == 0 {
		return nil, nil // a non-trading day; not an error
	}
	t := env.Tables[0]

	// The endpoint ignores a date it does not recognise and answers with the latest session
	// instead. Silently archiving that would stamp today's ratios onto the date we asked
	// for — a fabricated observation, and exactly the class of defect this file exists to
	// avoid. So the answer's own date is checked against the request.
	got, err := rocRowDate(t.Date)
	if err != nil {
		return nil, fmt.Errorf("tpex: unparseable response date %q", t.Date)
	}
	if want := day.Format("2006-01-02"); got != want {
		return nil, fmt.Errorf("tpex: asked for %s, got %s — refusing to archive it as %s",
			want, got, want)
	}

	idx := fieldIndex(t.Fields, map[string]string{
		"code": "股票代號", "pe": "本益比", "pb": "股價淨值比", "yield": "殖利率",
	})
	// Same refusal as TWSEMonth, and it matters more here: this response is the WHOLE MARKET,
	// so an unresolved column would archive a false "no earnings" for every listed stock at
	// once. See the comment there.
	if idx["pe"] < 0 {
		return nil, fmt.Errorf("tpex %s: no 本益比 column in %v — refusing to archive rows "+
			"whose missing P/E would read as non-positive earnings", got, t.Fields)
	}
	// The code column gets the same treatment, for a different false fact.
	//
	// Without it, an unmatched header makes every row's code empty, every row is skipped, and
	// the caller receives an empty map — which it reads as a non-trading day. A renamed column
	// would quietly report the entire backfill window as holidays: no error, no rows, and a
	// summary that looks like the exchange simply was not open all year.
	if idx["code"] < 0 {
		return nil, fmt.Errorf("tpex %s: no 股票代號 column in %v — refusing to report a "+
			"parse failure as a non-trading day", got, t.Fields)
	}
	out := make(map[string]Ratios, len(t.Data))
	for _, row := range t.Data {
		code := strings.TrimSpace(pick(row, idx["code"]))
		if code == "" {
			continue
		}
		out[code] = Ratios{
			Symbol:        code,
			Date:          got,
			PERatio:       ratio(pick(row, idx["pe"])),
			PBRatio:       ratio(pick(row, idx["pb"])),
			DividendYield: ratio(pick(row, idx["yield"])),
			Backfilled:    true,
			Source:        SourceTPEXHistorical,
		}
	}
	return out, nil
}

// get issues one throttled, retried request.
func (p *HistoricalProvider) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= p.Retries; attempt++ {
		if attempt > 0 {
			// Exponential backoff. A backfill is not urgent, and hammering an exchange that
			// is already refusing is how an IP gets blocked for everyone using it.
			//
			// A RATE LIMIT gets a far longer wait than an ordinary error: the CDN's cooldown
			// is measured in tens of seconds, so retrying after 1s simply spends the budget
			// confirming the block.
			wait := time.Duration(1<<uint(attempt-1)) * time.Second
			if IsRateLimited(lastErr) {
				wait = time.Duration(1<<uint(attempt-1)) * p.rateLimitBackoff()
				p.Logf("rate limited — waiting %s before retry %d/%d", wait, attempt, p.Retries)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		p.wait(ctx)
		body, err := p.once(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (p *HistoricalProvider) once(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if rateLimited(resp.StatusCode) {
		return nil, &rateLimitError{code: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// rateLimitError marks a refusal that means "slow down", not "this does not exist".
//
// TWSE's CDN answers a burst with 307 rather than 429 — a redirect to an error page, which
// Go's client does not follow because the response carries no usable Location. Treating it as
// an ordinary failure burns the retry budget in seconds and then marks a perfectly good month
// as permanently unavailable, which is exactly what happened on the first 12-month run: five
// stocks lost every month after roughly the 66th request.
type rateLimitError struct{ code int }

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("HTTP %d (rate limited — the exchange is refusing bursts)", e.code)
}

func rateLimited(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusFound:
		return true
	}
	return false
}

// RateLimitedError builds the refusal the exchanges produce.
//
// It exists because IsRateLimited is exported and callers branch on it — cmd/valuation-backfill
// decides from it whether to tell the reader "come back later" — and without a way to construct
// one, that branch is untestable from outside this package. The RECOGNITION itself is tested
// here against real HTTP responses; this is only for exercising what callers do with it.
func RateLimitedError(code int) error { return &rateLimitError{code: code} }

// IsRateLimited reports whether an error came from an exchange refusing the request rate.
func IsRateLimited(err error) bool {
	var e *rateLimitError
	return errors.As(err, &e)
}

// wait enforces the throttle between requests.
func (p *HistoricalProvider) wait(ctx context.Context) {
	if p.Throttle <= 0 {
		return
	}
	if !p.last.IsZero() {
		if d := p.Throttle - time.Since(p.last); d > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}
	}
	p.last = time.Now()
}

// rateLimitBackoff is the base wait after a rate-limit refusal. Zero → 30s.
func (p *HistoricalProvider) rateLimitBackoff() time.Duration {
	if p.RateLimitBackoff > 0 {
		return p.RateLimitBackoff
	}
	return 30 * time.Second
}

func (p *HistoricalProvider) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ── parsing ───────────────────────────────────────────────────────────────────────────

// ratio parses a published figure, returning nil for every form of "not published".
//
// The exchanges write "-", "N/A", "--" or an empty cell for a company with non-positive
// earnings. Each of those is a FACT about the company, and turning it into 0 would put a
// loss-making stock at the cheap end of every percentile it appears in.
func ratio(s string) *float64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	switch s {
	case "", "-", "--", "---", "N/A", "n/a", "NA", "null":
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	// A published zero is not a usable ratio either; the exchanges use it as a placeholder
	// where they have nothing, and a P/E of exactly 0 has no financial meaning.
	if v == 0 {
		return nil
	}
	return &v
}

// rocRowDate converts the exchanges' Republic-of-China dates to YYYY-MM-DD.
//
// Both "115年08月03日" and "115/08/03" appear, and TPEx uses the second in its own response
// header. Year 115 is 2026.
func rocRowDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("年", "/", "月", "/", "日", "").Replace(s)
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("unrecognised ROC date %q", s)
	}
	y, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	d, err3 := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err1 != nil || err2 != nil || err3 != nil {
		return "", fmt.Errorf("unrecognised ROC date %q", s)
	}
	if y < 1 || m < 1 || m > 12 || d < 1 || d > 31 {
		return "", fmt.Errorf("out-of-range ROC date %q", s)
	}
	return fmt.Sprintf("%04d-%02d-%02d", y+1911, m, d), nil
}

// rocDate renders a Gregorian date as the ROC form TPEx expects: 115/08/03.
func rocDate(t time.Time) string {
	return fmt.Sprintf("%d/%02d/%02d", t.Year()-1911, int(t.Month()), t.Day())
}

// fieldIndex maps logical names to column positions by matching the header row.
//
// Positional indexing would break the day an exchange inserts a column, and it would break
// SILENTLY — the P/E would simply start reading the P/B. Matching on the header means an
// unexpected shape yields a missing column, which `pick` turns into an absent value.
func fieldIndex(fields []string, want map[string]string) map[string]int {
	idx := make(map[string]int, len(want))
	for key, label := range want {
		idx[key] = -1
		for i, f := range fields {
			if strings.Contains(strings.TrimSpace(f), label) {
				idx[key] = i
				break
			}
		}
	}
	return idx
}

// pick reads one cell as text, whatever JSON type the exchange used for it.
//
// The rows are heterogeneous: 股利年度 arrives as a bare number while every neighbouring
// column is a quoted string, so decoding into [][]string fails on the whole response — and
// it fails for the entire month, losing 21 good sessions to one column nothing here reads.
func pick(row []json.RawMessage, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	raw := row[i]
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strings.Trim(string(raw), `"`)
}

// MergeHistory folds new sessions into existing ones, keyed by session date.
//
// Later wins on a genuine conflict, but the operation is IDEMPOTENT for identical input:
// re-running a backfill produces the same set, not duplicates. The result is sorted by date
// so an archive file's contents do not churn between runs.
func MergeHistory(existing, incoming []Ratios) []Ratios {
	by := make(map[string]Ratios, len(existing)+len(incoming))
	for _, r := range existing {
		by[r.Date] = r
	}
	for _, r := range incoming {
		by[r.Date] = r
	}
	out := make([]Ratios, 0, len(by))
	for _, r := range by {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
