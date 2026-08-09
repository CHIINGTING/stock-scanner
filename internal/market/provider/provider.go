// Package provider is the data-acquisition layer of the Market Dashboard: HTTP, parsing,
// field normalisation, basic validation and provenance. Nothing here decides what a number
// means — whether -87,911 net short contracts is bearish, crowded, or about to squeeze is
// the analyzer's problem.
//
// Two kinds of source live here:
//
//	CacheFeed  — the scanner's local .cache, supplying the benchmark series and the breadth
//	             universe. No network, two years of history from day one.
//	Provider   — an exchange over HTTP (TWSE, TAIFEX), each producing a PARTIAL RawSnapshot
//	             that the pipeline merges.
//
// Isolation contract, shared with internal/news and internal/institution: a method MUST NOT
// panic and MUST respect ctx. A source that is unreachable or has no data for the date
// records that in SourceMeta and leaves its dataset nil; it never returns a zero-valued
// dataset, because "the exchange was down" and "the market printed zero" must stay
// distinguishable all the way into the archive.
package provider

import (
	"context"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// Provider fetches one exchange's datasets for a trading date.
//
// The signature is deliberately coarse — one call returning a partial RawSnapshot rather
// than one method per dataset. The pipeline merges the parts, so adding a dataset to an
// existing source, or a whole new source, changes no interface and no call site.
//
// date is YYYY-MM-DD; the implementation formats it for its own endpoint and must never
// look ahead of it.
type Provider interface {
	// Name identifies the source in provenance records and logs.
	Name() string
	// Fetch returns whatever this source could supply for date. It returns an error only
	// when the request itself was invalid (a malformed date); a source being down is
	// reported inside the snapshot's SourceMeta, not as an error, so one dead exchange
	// cannot take the rest of the day with it.
	Fetch(ctx context.Context, date string) (*model.RawSnapshot, error)
}

// Compile-time proof that both real providers satisfy the interface.
var (
	_ Provider = (*TWSEProvider)(nil)
	_ Provider = (*TAIFEXProvider)(nil)
)
