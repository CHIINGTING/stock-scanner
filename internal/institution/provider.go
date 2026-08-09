package institution

import (
	"context"
	"errors"
)

// ErrNoData is the canonical "this date has no trading data" sentinel (holiday / market
// closed / before history floor). The two sources signal no-data differently — TWSE via
// stat != "OK", TPEx via an empty data array with stat == "ok" (§9) — and both parsers
// normalize that to ErrNoData so callers branch with errors.Is. Any OTHER error means a
// genuine fetch/parse failure.
var ErrNoData = errors.New("institution: no trading data for date")

// Provider fetches one market's institutional snapshot for a date. Isolation contract
// (mirrors internal/news.NewsProvider / internal/market/provider): a method MUST NOT
// panic and MUST respect ctx. A no-data day returns ErrNoData; a transport/parse problem
// returns any other error. The prefetch command isolates per-source failures so one
// market being down never blocks the other.
//
// date is YYYY-MM-DD; the provider formats it for its own endpoint (TWSE YYYYMMDD, TPEx
// YYYY/MM/DD) and must never look ahead of it.
type Provider interface {
	Market() string // MarketTWSE | MarketTPEX
	Fetch(ctx context.Context, date string) (*Snapshot, error)
}
