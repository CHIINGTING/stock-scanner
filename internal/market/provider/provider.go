// Package provider defines the pluggable data sources of the Market Dashboard. Phase 1
// ships only a mock; live sources (TWSE / TAIFEX / FinMind / Fugle / Yahoo) implement the
// same interface later without touching the analyzer or service — Provider Pattern,
// Interface First.
//
// Isolation contract (mirrors internal/news.NewsProvider): a method MUST NOT panic and
// MUST respect ctx. When a source is unreachable or not yet implemented it returns
// ErrUnavailable (or any error); the Service isolates that single failure — the module is
// marked unavailable and the rest of the dashboard still builds.
package provider

import (
	"context"
	"errors"

	"github.com/deep-huang/stock-scanner/internal/market/model"
)

// ErrUnavailable is the canonical "this source has no data right now / not implemented"
// sentinel. Providers should wrap or return it; the service treats ANY error the same way.
var ErrUnavailable = errors.New("market: data source unavailable")

// Provider is the aggregate of every market data source. The method names mirror the
// feature spec (Futures / ForeignBuySell / Margin / Options / Currency / VIX) plus Sectors
// for the sector-cycle reuse. A concrete provider that only serves some feeds returns
// ErrUnavailable from the rest.
//
// date is the analysis date (YYYY-MM-DD); providers must return as-of data for that date
// and never look ahead of it.
type Provider interface {
	Futures(ctx context.Context, date string) (model.FuturesData, error)
	ForeignBuySell(ctx context.Context, date string) (model.ForeignCashData, error)
	Margin(ctx context.Context, date string) (model.MarginData, error)
	Options(ctx context.Context, date string) (model.OptionsData, error)
	Currency(ctx context.Context, date string) (model.CurrencyData, error)
	VIX(ctx context.Context, date string) (model.VIXData, error)
	Sectors(ctx context.Context, date string) ([]model.SectorScore, error)
}
