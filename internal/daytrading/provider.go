package daytrading

import "context"

// Provider fetches one market's 當日沖銷 snapshot for a date. It mirrors
// internal/institution.Provider, including the isolation contract: a method MUST NOT
// panic and MUST respect ctx, and the fetch command isolates per-source failures so one
// market being down never blocks the other.
//
// A non-trading day is NOT an error here (unlike institution's ErrNoData): both endpoints
// answer a holiday with a well-formed, empty payload, so Fetch returns a valid snapshot
// whose Stocks are empty. Callers gate on Snapshot.HasData before persisting. An error
// means a genuine transport/parse failure.
//
// date is YYYY-MM-DD; the provider formats it for its own endpoint (TWSE YYYYMMDD, TPEx
// YYYY/MM/DD) and must never look ahead of it.
type Provider interface {
	Market() string // MarketTWSE | MarketTPEX
	Fetch(ctx context.Context, date string) (*Snapshot, error)
}
