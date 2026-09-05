package institution

import (
	"context"
	"errors"
	"fmt"
)

// Acquiring one date's institutional flows for every market.
//
// This exists so the loop has ONE implementation. It was written twice — once in
// cmd/institution-fetch and once in the daily pipeline — and the two had already drifted:
// one counted a refused downgrade as a successful acquisition and the other did not, so the
// same day could be reported differently depending on which entry point ran.

// MarketResult is one exchange's outcome for one date.
type MarketResult struct {
	Market string
	// Saved is true when the archive holds this market's data for the date afterwards —
	// including when an existing, more complete snapshot was kept.
	Saved bool
	// NoData is true when the exchange reported nothing for the date.
	//
	// It does NOT mean "holiday". ErrNoData is returned both for a genuine non-trading day
	// AND for a trading day whose data has not been published yet — TWSE T86 typically
	// appears several hours after the close. A caller that reads this as a session fact will
	// report "nothing to fetch" on every day it runs before publication.
	NoData bool
	// Kept is true when an existing snapshot was preserved rather than downgraded.
	Kept bool
	Err  error
}

// AcquireResult is the whole date.
type AcquireResult struct {
	Date    string
	Results []MarketResult
}

// Saved counts the markets whose data is in the archive afterwards.
func (r AcquireResult) Saved() int {
	n := 0
	for _, m := range r.Results {
		if m.Saved {
			n++
		}
	}
	return n
}

// NoData counts the markets that reported nothing.
func (r AcquireResult) NoData() int {
	n := 0
	for _, m := range r.Results {
		if m.NoData {
			n++
		}
	}
	return n
}

// Reasons renders one line per market that has something to say.
func (r AcquireResult) Reasons() []string {
	var out []string
	for _, m := range r.Results {
		switch {
		case m.Err != nil:
			out = append(out, m.Market+"："+m.Err.Error())
		case m.NoData:
			out = append(out, m.Market+"：來源尚無此日資料")
		case m.Kept:
			out = append(out, m.Market+"：保留既有較完整的快照")
		}
	}
	return out
}

// Acquire fetches and archives one date for every provider.
//
// One market's failure never stops another: the exchanges are independent, and losing TPEx
// because TWSE was slow would leave a permanent hole in half the universe. Every outcome is
// reported rather than returned as an error, because "TWSE published and TPEx has not yet"
// is a normal state that a caller has to be able to describe.
func Acquire(ctx context.Context, providers []Provider, dir, date string) AcquireResult {
	out := AcquireResult{Date: date}
	for _, p := range providers {
		m := MarketResult{Market: p.Market()}
		snap, err := p.Fetch(ctx, date)
		switch {
		case err != nil && errors.Is(err, ErrNoData):
			m.NoData = true
		case err != nil:
			m.Err = err
		default:
			if err := Save(dir, snap); err != nil {
				if errors.Is(err, ErrWouldDowngrade) {
					// The archive already holds a better snapshot for this date. Keeping it
					// is the correct outcome, and the market IS covered afterwards — which
					// is what Saved means.
					m.Saved, m.Kept = true, true
				} else {
					m.Err = fmt.Errorf("save: %w", err)
				}
			} else {
				m.Saved = true
			}
		}
		out.Results = append(out.Results, m)
	}
	return out
}
