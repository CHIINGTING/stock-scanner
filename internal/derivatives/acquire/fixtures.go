package acquire

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The offline half of the Transport seam.
//
// http.go's doc comment promises two implementations and that no test in this package opens a
// socket. This is the second one, and it is not a stub: it replays the COMMITTED captures in
// internal/derivatives/testdata — the same bytes the parsers are tested against — through the
// same validation the live client applies, so a test exercises the real decode path rather
// than a hand-written approximation of it.
//
// Faults are part of the contract rather than an afterthought. Every status the acquisition
// records for a failure (§6, §6.2) is reachable only through a response that failed, so a
// transport that can only succeed leaves ERROR, MISSING and the content-type rejection
// untested — which is how the interesting half of the status vocabulary rots.

// DefaultFixtureDir is where the captures live, relative to this package's directory.
//
// `go test` runs with the working directory set to the package under test, so the default is
// correct for every test in internal/derivatives/acquire and wrong everywhere else. A caller
// outside this package sets Dir explicitly.
const DefaultFixtureDir = "../testdata"

// FixtureDate is the Taipei trading session every file in FixtureFiles describes.
//
// It is a constant rather than a literal repeated in each test because it is the TARGET
// SESSION the whole run is for: the date validation (§2.2) compares the feed's own date
// against it, so a test that passes a different date is testing the mismatch path on purpose.
const FixtureDate = "2026-09-04"

// FixtureFiles maps each dataset to the capture that stands in for its live response.
//
// Keyed on Dataset.Name and not on the endpoint, for the reason dataset.go gives: an endpoint
// can be renamed, and a fixture table keyed on the URL would then silently stop matching.
var FixtureFiles = map[string]string{
	OptionsByStrike.Name:        "options_by_strike_20260904.json",
	FuturesDaily.Name:           "futures_daily_20260904.json",
	InstitutionalFutures.Name:   "institutional_futures_20260904.json",
	InstitutionalCallsPuts.Name: "institutional_callsputs_20260904.json",
	PutCallRatio.Name:           "put_call_ratio_20260904.json",
	Margin.Name:                 "margin_20260904.json",
	FinalSettlement.Name:        "final_settlement_20260904.json",
	SettledPositions.Name:       "settled_positions_20260904.json",
	OptionsDelta.Name:           "options_delta_20260904.json",
}

// Fault is a response that went wrong, described the way the exchange would describe it.
//
// The zero value is not a fault: a Faults entry only takes effect through the fields it sets,
// so `Fault{Status: 500}` is a 500 carrying the ordinary fixture body, which is what a real
// gateway error looks like before anyone reads the status.
type Fault struct {
	// Status replaces 200. Rate-limit codes become the same *rateLimitError HTTPTransport
	// raises — including the redirect family, which is the shape valuation learned the hard
	// way (see http.go) — and everything else outside 2xx becomes a *StatusError.
	Status int
	// ContentType replaces application/json. "text/html" is the maintenance page.
	ContentType string
	// Body replaces the fixture's bytes. Use Empty for a zero-length body, because a nil
	// Body here means "no override" and cannot express one.
	Body []byte
	// Empty serves a zero-length 200 — ErrEmptyBody, which is retryable live and is never a
	// reason to store an empty snapshot.
	Empty bool
	// Err short-circuits everything and is returned verbatim: a transport failure with no
	// response behind it (connection refused, TLS, a deadline).
	Err error
}

// FixtureTransport replays captures, and records what was asked of it.
//
// The call counts matter to more than curiosity: "NO_SESSION is decided before any request is
// made" (§6) is only testable by asserting that no request was made.
type FixtureTransport struct {
	// Dir defaults to DefaultFixtureDir.
	Dir string
	// Files overrides FixtureFiles per dataset, by file name within Dir.
	Files map[string]string
	// Bodies serves literal bytes for a dataset, ahead of any file. This is how a test builds
	// a response that does not exist as a capture — a single-session by-strike response, a
	// feed carrying the wrong trading date — without committing a fixture per scenario.
	Bodies map[string][]byte
	// ContentType is the header served when no fault says otherwise. Empty → application/json.
	ContentType string
	// Faults injects failures, keyed on Dataset.Name.
	Faults map[string]Fault

	mu    sync.Mutex
	calls map[string]int
}

// FixtureURL is what an error names when a fixture failed.
//
// Deliberately not a taifex.com.tw URL: an error message claiming the exchange refused a
// request that was never sent is a lie that costs an hour to unpick at 3am.
func FixtureURL(ds Dataset) string { return "fixture://" + ds.Name + "/" + ds.Endpoint }

// Get replays one dataset.
func (f *FixtureTransport) Get(ctx context.Context, ds Dataset) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	f.record(ds.Name)

	fault := f.Faults[ds.Name]
	if fault.Err != nil {
		return Response{Attempts: 1}, fault.Err
	}

	out := Response{StatusCode: 200, ContentType: f.contentType(), Attempts: 1}
	if fault.ContentType != "" {
		out.ContentType = fault.ContentType
	}
	if fault.Status != 0 {
		out.StatusCode = fault.Status
	}

	// Status first, then content type, then emptiness — the same order once() applies, and
	// for the same reason: a 500 serving an HTML error page is a 500, which is retryable,
	// not a layout change, which is not.
	if rateLimited(out.StatusCode) {
		return out, &rateLimitError{code: out.StatusCode}
	}
	if out.StatusCode < 200 || out.StatusCode >= 300 {
		return out, &StatusError{Status: out.StatusCode, URL: FixtureURL(ds)}
	}

	switch {
	case fault.Empty:
		out.Body = nil
	case fault.Body != nil:
		out.Body = fault.Body
	default:
		body, err := f.body(ds)
		if err != nil {
			return out, err
		}
		out.Body = body
	}

	// The same validation the live client applies, so a fixture cannot deliver a payload an
	// HTTPTransport would have rejected.
	if err := validatePayload(FixtureURL(ds), out.ContentType, out.Body); err != nil {
		return out, err
	}
	return out, nil
}

func (f *FixtureTransport) contentType() string {
	if f.ContentType != "" {
		return f.ContentType
	}
	return "application/json; charset=utf-8"
}

// body resolves the bytes for one dataset: an explicit body, then an overridden file, then
// the committed capture.
//
// A dataset with none of the three is a MISTAKE, not a fetch outcome, and is returned as a
// plain error rather than something that could be mapped onto MISSING — a fixture table with
// a hole in it must not look like an exchange with a hole in it.
func (f *FixtureTransport) body(ds Dataset) ([]byte, error) {
	if b, ok := f.Bodies[ds.Name]; ok {
		return b, nil
	}
	name, ok := f.Files[ds.Name]
	if !ok {
		name, ok = FixtureFiles[ds.Name]
	}
	if !ok {
		return nil, fmt.Errorf("acquire: no fixture registered for dataset %q", ds.Name)
	}
	dir := f.Dir
	if dir == "" {
		dir = DefaultFixtureDir
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, fmt.Errorf("acquire: read fixture for %s: %w", ds.Name, err)
	}
	return b, nil
}

func (f *FixtureTransport) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

// Calls reports how many times one dataset was requested.
func (f *FixtureTransport) Calls(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

// TotalCalls reports how many requests were issued in all.
func (f *FixtureTransport) TotalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, c := range f.calls {
		n += c
	}
	return n
}

// ReadFixture returns one committed capture by file name, for a test that needs the bytes
// rather than a transport.
func ReadFixture(dir, name string) ([]byte, error) {
	if dir == "" {
		dir = DefaultFixtureDir
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// compile-time proof that the replay satisfies the same seam the live client does.
var _ Transport = (*FixtureTransport)(nil)
