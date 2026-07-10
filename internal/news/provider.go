package news

import "context"

// NewsProvider is one external source of market notes. Implementations live in
// sub-packages (socialworkerdaily, twetq) and import this package for the shared
// types. Fetch must be self-contained and side-effect free apart from network I/O.
//
// A provider MUST NOT panic and MUST respect ctx cancellation/deadline. When a source
// is unreachable, blocked (e.g. Cloudflare), or its structure cannot be parsed, it
// returns an error; the Service isolates that failure and continues with the others.
type NewsProvider interface {
	// Name is the stable provider identifier used in logs, dedup sources, and config.
	Name() string
	// Fetch returns the most recent items. An error means "this source is unavailable
	// right now" — the scanner run continues regardless (provider-isolated failure).
	Fetch(ctx context.Context) ([]RawNewsItem, error)
}
