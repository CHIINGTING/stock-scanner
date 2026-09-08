package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The replay transport itself.
//
// It is tested rather than trusted because every acquisition test rests on it: a fixture that
// silently served the wrong dataset, or a fault that produced a different error from the one
// the live client raises, would make the whole package agree with an implementation nobody
// runs in production.

func TestEveryDatasetHasAReplayableFixture(t *testing.T) {
	tr := &FixtureTransport{}
	for _, ds := range All {
		resp, err := tr.Get(context.Background(), ds)
		if err != nil {
			t.Fatalf("%s: %v", ds.Name, err)
		}
		if resp.StatusCode != 200 || resp.Attempts != 1 {
			t.Fatalf("%s: status %d after %d attempt(s)", ds.Name, resp.StatusCode, resp.Attempts)
		}
		var rows []map[string]string
		if err := json.Unmarshal(resp.Body, &rows); err != nil {
			t.Fatalf("%s: the fixture is not the flat string envelope every TAIFEX feed uses: %v",
				ds.Name, err)
		}
		if len(rows) == 0 {
			t.Fatalf("%s: the fixture carries no rows", ds.Name)
		}
		if tr.Calls(ds.Name) != 1 {
			t.Fatalf("%s: %d calls recorded", ds.Name, tr.Calls(ds.Name))
		}
	}
	if tr.TotalCalls() != len(All) {
		t.Fatalf("%d calls in all, want %d", tr.TotalCalls(), len(All))
	}
}

// TestFaultsRaiseTheSameErrorsTheLiveClientDoes. The acquisition classifies on these types, so
// a fixture that returned a plain error would leave the classification untested.
func TestFaultsRaiseTheSameErrorsTheLiveClientDoes(t *testing.T) {
	cases := []struct {
		name  string
		fault Fault
		check func(*testing.T, error)
	}{
		{"a 5xx is a StatusError carrying its code", Fault{Status: 502}, func(t *testing.T, err error) {
			var se *StatusError
			if !errors.As(err, &se) || se.Status != 502 {
				t.Fatalf("want *StatusError 502, got %#v", err)
			}
		}},
		{"a 503 is read as a rate limit, not a server fault", Fault{Status: 503},
			func(t *testing.T, err error) {
				// http.go puts 503 in the rate-limit family deliberately: a CDN shedding a
				// burst answers 503 as readily as 429, and retrying it after 1s only spends
				// the budget confirming the block.
				if !IsRateLimited(err) {
					t.Fatalf("want a rate limit, got %v", err)
				}
			}},
		{"a 429 is a rate limit, not a 4xx", Fault{Status: 429}, func(t *testing.T, err error) {
			if !IsRateLimited(err) {
				t.Fatalf("want a rate limit, got %v", err)
			}
			var se *StatusError
			if errors.As(err, &se) {
				t.Fatal("a rate limit must not also read as an ordinary status refusal")
			}
		}},
		{"a 307 is a rate limit too", Fault{Status: 307}, func(t *testing.T, err error) {
			// The lesson internal/valuation paid for: TWSE's CDN answered a burst with 307
			// rather than 429, the retry budget burned in seconds, and five stocks lost every
			// month after the 66th request.
			if !IsRateLimited(err) {
				t.Fatalf("want a rate limit, got %v", err)
			}
		}},
		{"a 404 is a StatusError", Fault{Status: 404}, func(t *testing.T, err error) {
			var se *StatusError
			if !errors.As(err, &se) || se.Status != 404 {
				t.Fatalf("want *StatusError 404, got %#v", err)
			}
			if IsRateLimited(err) {
				t.Fatal("a 404 is not a rate limit")
			}
		}},
		{"an HTML maintenance page", Fault{ContentType: "text/html; charset=utf-8"},
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrContentType) {
					t.Fatalf("want ErrContentType, got %v", err)
				}
			}},
		{"an empty 200", Fault{Empty: true}, func(t *testing.T, err error) {
			if !errors.Is(err, ErrEmptyBody) {
				t.Fatalf("want ErrEmptyBody, got %v", err)
			}
		}},
		{"a body of whitespace is empty", Fault{Body: []byte("   \n")}, func(t *testing.T, err error) {
			if !errors.Is(err, ErrEmptyBody) {
				t.Fatalf("want ErrEmptyBody, got %v", err)
			}
		}},
		{"a transport failure with no response behind it",
			Fault{Err: errors.New("dial tcp: connection refused")}, func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "connection refused") {
					t.Fatalf("the error was rewritten: %v", err)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &FixtureTransport{Faults: map[string]Fault{Margin.Name: tc.fault}}
			_, err := tr.Get(context.Background(), Margin)
			if err == nil {
				t.Fatal("no error")
			}
			tc.check(t, err)
			// The other datasets are unaffected: a fault is keyed on one dataset, which is
			// what makes "one dataset failing does not mark the others failed" testable.
			if _, err := tr.Get(context.Background(), PutCallRatio); err != nil {
				t.Fatalf("the fault leaked onto another dataset: %v", err)
			}
		})
	}
}

// TestAnErrorNamesTheFixtureRatherThanTheExchange — an error message claiming taifex.com.tw
// refused a request that was never sent costs an hour to unpick.
func TestAnErrorNamesTheFixtureRatherThanTheExchange(t *testing.T) {
	tr := &FixtureTransport{Faults: map[string]Fault{Margin.Name: {Status: 500}}}
	_, err := tr.Get(context.Background(), Margin)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "taifex.com.tw") {
		t.Fatalf("a fixture error named the exchange: %v", err)
	}
	if !strings.Contains(err.Error(), "fixture://") {
		t.Fatalf("the error does not say where it came from: %v", err)
	}
}

// TestAnUnregisteredDatasetIsAMistakeNotAFetchOutcome — a hole in the fixture table must not
// look like a hole in the exchange, so it is a plain error and never something that could be
// classified as MISSING.
func TestAnUnregisteredDatasetIsAMistakeNotAFetchOutcome(t *testing.T) {
	tr := &FixtureTransport{}
	_, err := tr.Get(context.Background(), Dataset{Name: "invented", Endpoint: "Invented"})
	if err == nil {
		t.Fatal("an unregistered dataset was served")
	}
	var se *StatusError
	if errors.As(err, &se) {
		t.Fatalf("a missing fixture read as an HTTP refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "no fixture") {
		t.Fatalf("unclear error: %v", err)
	}
}

// TestBodiesOverrideFiles — this is how a test builds a response that does not exist as a
// capture without committing a fixture per scenario.
func TestBodiesOverrideFiles(t *testing.T) {
	body := []byte(`[{"Date":"20260904","Contract":"TXO"}]`)
	tr := &FixtureTransport{Bodies: map[string][]byte{Margin.Name: body}}
	resp, err := tr.Get(context.Background(), Margin)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != string(body) {
		t.Fatalf("served %q", resp.Body)
	}
}

// TestACancelledContextIsNotAFetch.
func TestACancelledContextIsNotAFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr := &FixtureTransport{}
	if _, err := tr.Get(ctx, Margin); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if tr.TotalCalls() != 0 {
		t.Fatalf("%d call(s) recorded for a cancelled context", tr.TotalCalls())
	}
}
