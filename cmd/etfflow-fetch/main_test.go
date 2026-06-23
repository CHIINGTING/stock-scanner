package main

import (
	"testing"

	"github.com/deep-huang/stock-scanner/internal/etfflow"
)

// statusError is the CLI's exit decision and is pure, so the R8-6 coverage guardrail's
// exit semantics are tested offline (no network): a PARTIAL source must exit non-zero
// unless --allow-partial is given, mirroring STALE / --allow-stale.
func TestStatusError(t *testing.T) {
	cases := []struct {
		name         string
		status       etfflow.FetchStatus
		coverage     string
		allowStale   bool
		allowPartial bool
		wantErr      bool
	}{
		{"ok", etfflow.StatusOK, etfflow.CoverageFullHoldings, false, false, false},
		{"failed", etfflow.StatusFailed, "", false, false, true},
		{"stale blocks", etfflow.StatusStale, etfflow.CoverageFullHoldings, false, false, true},
		{"stale allowed", etfflow.StatusStale, etfflow.CoverageFullHoldings, true, false, false},
		{"partial blocks by default", etfflow.StatusPartial, etfflow.CoverageTopNOnly, false, false, true},
		{"partial allowed writes probe", etfflow.StatusPartial, etfflow.CoverageTopNOnly, false, true, false},
		// --allow-stale must NOT unlock a PARTIAL source; only --allow-partial does.
		{"partial not unlocked by allow-stale", etfflow.StatusPartial, etfflow.CoverageTopNOnly, true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := etfflow.FetchResult{Status: c.status, Coverage: etfflow.Coverage{Type: c.coverage}}
			if c.status == etfflow.StatusFailed {
				res.Err = errStub
			}
			o := fetchOpts{allowStale: c.allowStale, allowPartial: c.allowPartial}
			err := statusError(res, o)
			if (err != nil) != c.wantErr {
				t.Errorf("statusError(%s)=%v wantErr=%v", c.status, err, c.wantErr)
			}
		})
	}
}

// errStub stands in for a FAILED result's parse/fetch error.
var errStub = stubError("boom")

type stubError string

func (e stubError) Error() string { return string(e) }
