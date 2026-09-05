package valuation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The 307 that started all this.
//
// TWSE's CDN answers a burst with a redirect to an error page rather than 429, and Go's client
// does not follow it because the response carries no usable Location. On the first 12-month
// run that surfaced as an ordinary failure: the 1s retry budget was spent in three seconds
// while the CDN's cooldown was still tens of minutes away, and five stocks had every remaining
// month marked permanently unavailable. Nothing anywhere said "throttled".
//
// So the status codes are load-bearing, and the distinction they carry — come back later vs.
// this does not exist — is what the whole retry and reporting path branches on.

func provider(t *testing.T, h http.Handler) (*HistoricalProvider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p := NewHistoricalProvider(nil)
	p.Throttle = 0
	p.Retries = 1
	p.RateLimitBackoff = time.Millisecond
	p.twseURL, p.tpexURL = srv.URL+"/twse", srv.URL+"/tpex"
	return p, srv
}

func TestRefusalStatusesAreRecognisedAsRateLimits(t *testing.T) {
	// Every code the exchanges have actually answered a burst with.
	for _, code := range []int{
		http.StatusTemporaryRedirect,  // 307 — TWSE's CDN
		http.StatusPermanentRedirect,  // 308
		http.StatusFound,              // 302
		http.StatusTooManyRequests,    // 429
		http.StatusServiceUnavailable, // 503
	} {
		p, _ := provider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		_, err := p.TWSEMonth(context.Background(), "2330", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
		if err == nil {
			t.Fatalf("HTTP %d produced no error", code)
		}
		if !IsRateLimited(err) {
			t.Errorf("HTTP %d not recognised as a rate limit: %v — the caller would mark the "+
				"month permanently unavailable instead of retrying later", code, err)
		}
	}
}

// The converse matters just as much: if an ordinary failure claimed to be a rate limit, every
// broken run would advise the reader to come back later.
func TestOrdinaryFailuresAreNotRateLimits(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError,
		http.StatusForbidden} {
		p, _ := provider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		_, err := p.TPEXSession(context.Background(), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
		if err == nil {
			t.Fatalf("HTTP %d produced no error", code)
		}
		if IsRateLimited(err) {
			t.Errorf("HTTP %d was reported as a rate limit", code)
		}
	}
}

// A refusal is retried, and a request that succeeds on the retry returns data rather than the
// refusal — the cooldown is real and waiting it out is the whole point.
func TestARateLimitedRequestIsRetried(t *testing.T) {
	var n int
	p, _ := provider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stat":"OK","fields":["日期","本益比"],` +
			`"data":[["115/08/03","26.5"]]}`))
	}))

	rows, err := p.TWSEMonth(context.Background(), "2330",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("the retry did not happen: %v", err)
	}
	if n != 2 {
		t.Fatalf("made %d requests, want 2 (the refusal and the retry)", n)
	}
	if len(rows) != 1 || rows[0].Date != "2026-08-03" || rows[0].PERatio == nil {
		t.Fatalf("the retry's data was lost: %+v", rows)
	}
}

// RateLimitedError is what lets a caller in another package test its own branch.
func TestRateLimitedErrorIsRecognised(t *testing.T) {
	if !IsRateLimited(RateLimitedError(http.StatusTemporaryRedirect)) {
		t.Fatal("RateLimitedError is not recognised by IsRateLimited")
	}
}
