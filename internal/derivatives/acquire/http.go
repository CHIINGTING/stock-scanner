// Package acquire is R15's ONLY networked package.
//
// It is a separate package from internal/derivatives and internal/derivatives/provider on
// purpose, and the separation is load-bearing rather than tidy. §2.4 allows exactly one stage
// of the pipeline to touch the network:
//
//	fetch → validate → normalize → persist immutable snapshot → compute → attach → report
//
// Report generation, scanner decisions and the AI judge must not. That is enforceable as a
// dependency fact only if the socket-opening code lives somewhere the report and the scanner
// do not import — hence this package, and hence architecture_test.go, which fails if
// internal/report or internal/scanner ever reaches it. Putting the HTTP client in
// internal/derivatives would have made the same rule unenforceable, because §10.3's report
// section imports internal/derivatives by design.
//
// See docs/SPEC_R15_TAIFEX_DERIVATIVES_RISK.md §2.2, §2.4, §6, §6.2, §10.2, §10.2b.
package acquire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// BaseURL is the TAIFEX OpenAPI root. Every R15 endpoint hangs off it, and every one of them
// was found by reading https://openapi.taifex.com.tw/swagger.json rather than guessed — §2.2
// makes the method part of the contract, because two earlier "no official source" conclusions
// came from probing invented names and collecting 302s.
const BaseURL = "https://openapi.taifex.com.tw/v1"

// maxBody bounds one response. DailyMarketReportOpt is the largest at roughly 3 MB for 12,012
// rows, so 64 MB is two orders of magnitude of headroom and still a bound: an endpoint that
// starts streaming without end must fail rather than exhaust the process.
const maxBody = 64 << 20

// Response is one completed HTTP attempt.
type Response struct {
	Body        []byte
	ContentType string
	StatusCode  int
	// Attempts counts every request issued, so data health can say "succeeded on the third
	// try" rather than presenting a struggling endpoint as healthy.
	Attempts int
}

// Transport is the seam between "get the bytes" and everything that interprets them.
//
// Two implementations ship: HTTPTransport, and the fixture replay in fixtures.go. Every test
// in this package uses one of those two, so no test opens a socket to the exchange.
type Transport interface {
	Get(ctx context.Context, ds Dataset) (Response, error)
}

// ── typed failures ────────────────────────────────────────────────────────────────────

// StatusError is a refusal with an HTTP status behind it.
type StatusError struct {
	Status int
	URL    string
}

func (e *StatusError) Error() string { return fmt.Sprintf("GET %s: HTTP %d", e.URL, e.Status) }

// ErrRateLimited marks a refusal that means "slow down", not "this does not exist".
//
// The distinction is not academic: internal/valuation learned it the hard way when TWSE's CDN
// answered a burst with 307 rather than 429, the retry budget burned in seconds, and five
// stocks lost every month after the 66th request. R15 inherits the recognition — including
// the redirect codes — rather than rediscovering it.
var ErrRateLimited = errors.New("acquire: rate limited")

type rateLimitError struct{ code int }

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("HTTP %d (rate limited — the exchange is refusing bursts)", e.code)
}
func (e *rateLimitError) Is(target error) bool { return target == ErrRateLimited }

// IsRateLimited reports whether an error came from an exchange refusing the request rate.
// Same name and same meaning as valuation.IsRateLimited, deliberately: one vocabulary.
func IsRateLimited(err error) bool {
	var e *rateLimitError
	return errors.As(err, &e)
}

func rateLimited(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusFound:
		return true
	}
	return false
}

// ErrContentType is a 200 that is not the JSON it claims to be — most often an HTML error or
// maintenance page, which is §6.2's first whole-response rejection.
//
// The check is here AND in the parser, and that duplication is deliberate: this one names the
// problem accurately ("the exchange served text/html"), while the parser's catches the case
// this one cannot see — a server that labels an HTML page application/json. Neither is
// sufficient alone, and the second is the one that must never be removed, because an
// unlabelled failure reaching the parser is what produces an empty snapshot.
var ErrContentType = errors.New("acquire: response is not JSON")

// ErrEmptyBody is a 200 with nothing in it. Transient often enough to retry, and never a
// reason to store an empty snapshot.
var ErrEmptyBody = errors.New("acquire: empty response body")

// ── the client ────────────────────────────────────────────────────────────────────────

// HTTPTransport is the live fetcher: bounded timeout, bounded retries, a throttle between
// requests, and validation of everything before the bytes are handed on.
//
// The shape follows internal/valuation's HistoricalProvider rather than inventing a second
// one — exponential backoff, a longer wait for a rate limit, a minimum interval between
// requests — because FU-10 records what two implementations of one acquisition path cost:
// they disagreed about whether a downgrade counted as success, and the same day reported
// differently depending on which entry point ran.
type HTTPTransport struct {
	// BaseURL defaults to the constant above. Tests point it at an httptest server.
	BaseURL string
	// Client defaults to one with Timeout applied. Supplying a client with no timeout is the
	// one way to get an unbounded request, so Timeout is applied to a default client only.
	Client *http.Client
	// Timeout bounds one attempt. Zero → 30s.
	Timeout time.Duration
	// MaxAttempts includes the first. Zero → 3, one → no retry.
	MaxAttempts int
	// Backoff is the first retry's wait, doubling thereafter. Zero → 1s.
	Backoff time.Duration
	// RateLimitBackoff is the first wait after a rate-limit refusal, doubling thereafter.
	// Zero → 20s: a CDN cooldown is measured in tens of seconds, so retrying after 1s only
	// spends the budget confirming the block.
	RateLimitBackoff time.Duration
	// Throttle is the minimum interval between requests. Zero → 500ms. Nine endpoints a day
	// is not a burst, but the exchange has no way to know that in advance.
	Throttle time.Duration
	// Logf is optional.
	Logf func(format string, args ...any)

	mu   sync.Mutex
	last time.Time
}

func (t *HTTPTransport) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(format, args...)
	}
}

func (t *HTTPTransport) client() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	d := t.Timeout
	if d <= 0 {
		d = 30 * time.Second
	}
	return &http.Client{Timeout: d}
}

// URL is where one dataset lives. Exported so a data-health record and an error message can
// name the exact resource that failed.
func (t *HTTPTransport) URL(ds Dataset) string {
	base := t.BaseURL
	if base == "" {
		base = BaseURL
	}
	return strings.TrimRight(base, "/") + "/" + ds.Endpoint
}

// Get fetches one dataset, retrying only what is worth retrying.
//
// Retried: transport failures, 5xx, the rate-limit family, and an empty 200. Not retried: any
// other 4xx, or a body that is not JSON. Repeating a request whose answer is a FACT wastes the
// endpoint's patience and hides the fact — the rule internal/market/provider's foreign-flow
// audit arrived at after "failed" turned out to hide four different outcomes.
func (t *HTTPTransport) Get(ctx context.Context, ds Dataset) (Response, error) {
	attempts := t.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	url := t.URL(ds)

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := t.sleep(ctx, t.waitFor(attempt-1, lastErr)); err != nil {
				return Response{Attempts: attempt - 1}, err
			}
		}
		t.throttle(ctx)
		resp, err := t.once(ctx, url)
		resp.Attempts = attempt
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return Response{Attempts: attempt}, ctx.Err()
		}
		if !retryable(err) {
			return Response{Attempts: attempt}, err
		}
		t.logf("acquire: %s attempt %d/%d: %v", ds.Name, attempt, attempts, err)
	}
	return Response{Attempts: attempts}, fmt.Errorf("acquire: %s: gave up after %d attempts: %w",
		ds.Name, attempts, lastErr)
}

func retryable(err error) bool {
	if IsRateLimited(err) || errors.Is(err, ErrEmptyBody) {
		return true
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status >= 500
	}
	// A transport failure: connection refused, TLS, timeout. Not a statement about the data.
	return !errors.Is(err, ErrContentType)
}

func (t *HTTPTransport) waitFor(n int, lastErr error) time.Duration {
	base := t.Backoff
	if base <= 0 {
		base = time.Second
	}
	if IsRateLimited(lastErr) {
		base = t.RateLimitBackoff
		if base <= 0 {
			base = 20 * time.Second
		}
	}
	return base * time.Duration(1<<uint(n-1))
}

func (t *HTTPTransport) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// throttle enforces the minimum interval between requests. It holds the lock across the wait,
// so two goroutines sharing a transport cannot both decide they are first.
func (t *HTTPTransport) throttle(ctx context.Context) {
	gap := t.Throttle
	if gap == 0 {
		gap = 500 * time.Millisecond
	}
	if gap < 0 {
		return // explicitly disabled, for tests
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.last.IsZero() {
		if d := gap - time.Since(t.last); d > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		}
	}
	t.last = time.Now()
}

// once performs a single request and validates everything a response can be wrong about
// BEFORE the bytes are handed on: status, then content type, then emptiness.
//
// Order matters. A 500 that serves an HTML error page must be reported as a 500, not as "not
// JSON", because the first is retryable and the second is a layout change.
func (t *HTTPTransport) once(ctx context.Context, url string) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; stock-scanner/1.0)")

	resp, err := t.client().Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	out := Response{StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type")}
	if rateLimited(resp.StatusCode) {
		return out, &rateLimitError{code: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &StatusError{Status: resp.StatusCode, URL: url}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return out, fmt.Errorf("GET %s: read: %w", url, err)
	}
	if len(body) > maxBody {
		return out, fmt.Errorf("GET %s: response exceeds %d bytes", url, maxBody)
	}
	out.Body = body
	if err := validatePayload(url, out.ContentType, body); err != nil {
		return out, err
	}
	return out, nil
}

// validatePayload is the content-type and emptiness half of §6.2's whole-response rejection.
func validatePayload(url, contentType string, body []byte) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("GET %s: %w", url, ErrEmptyBody)
	}
	if !jsonish(contentType) {
		return fmt.Errorf("GET %s: %w: Content-Type %q", url, ErrContentType, contentType)
	}
	return nil
}

// jsonish decides whether a Content-Type may carry a TAIFEX response.
//
// An ABSENT header passes: some proxies strip it, the body still has to survive the parser's
// structural check, and failing here would turn a working feed off for a header. A header that
// says text/html does NOT pass — that is the maintenance page, and it is the case this check
// exists for.
func jsonish(contentType string) bool {
	if strings.TrimSpace(contentType) == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch {
	case mt == "application/json", mt == "text/json":
		return true
	case strings.HasSuffix(mt, "+json"):
		return true
	case mt == "text/plain", mt == "application/octet-stream":
		// Tolerated: the body still faces the parser's structural rejection, which is the
		// check that actually decides.
		return true
	}
	return false
}
