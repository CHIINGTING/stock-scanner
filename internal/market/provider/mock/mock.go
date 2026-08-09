// Package mock is a deterministic, fixture-driven Provider used by tests and by the
// cmd/market-dump demo. It never touches the network. Any field left as its zero
// "unavailable" toggle returns provider.ErrUnavailable so the isolation path is testable.
package mock

import (
	"context"

	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider"
)

// Provider holds one canned datum per feed. A *Missing flag forces the matching method to
// return provider.ErrUnavailable, so a test (or the demo) can simulate a down source.
type Provider struct {
	FuturesData  model.FuturesData
	CashData     model.ForeignCashData
	MarginData   model.MarginData
	OptionsData  model.OptionsData
	CurrencyData model.CurrencyData
	VIXData      model.VIXData
	SectorData   []model.SectorScore

	FuturesMissing  bool
	CashMissing     bool
	MarginMissing   bool
	OptionsMissing  bool
	CurrencyMissing bool
	VIXMissing      bool
	SectorMissing   bool
}

// compile-time assertion that the mock satisfies the full Provider interface.
var _ provider.Provider = (*Provider)(nil)

func (p *Provider) Futures(_ context.Context, _ string) (model.FuturesData, error) {
	if p.FuturesMissing {
		return model.FuturesData{}, provider.ErrUnavailable
	}
	return p.FuturesData, nil
}

func (p *Provider) ForeignBuySell(_ context.Context, _ string) (model.ForeignCashData, error) {
	if p.CashMissing {
		return model.ForeignCashData{}, provider.ErrUnavailable
	}
	return p.CashData, nil
}

func (p *Provider) Margin(_ context.Context, _ string) (model.MarginData, error) {
	if p.MarginMissing {
		return model.MarginData{}, provider.ErrUnavailable
	}
	return p.MarginData, nil
}

func (p *Provider) Options(_ context.Context, _ string) (model.OptionsData, error) {
	if p.OptionsMissing {
		return model.OptionsData{}, provider.ErrUnavailable
	}
	return p.OptionsData, nil
}

func (p *Provider) Currency(_ context.Context, _ string) (model.CurrencyData, error) {
	if p.CurrencyMissing {
		return model.CurrencyData{}, provider.ErrUnavailable
	}
	return p.CurrencyData, nil
}

func (p *Provider) VIX(_ context.Context, _ string) (model.VIXData, error) {
	if p.VIXMissing {
		return model.VIXData{}, provider.ErrUnavailable
	}
	return p.VIXData, nil
}

func (p *Provider) Sectors(_ context.Context, _ string) ([]model.SectorScore, error) {
	if p.SectorMissing {
		return nil, provider.ErrUnavailable
	}
	return p.SectorData, nil
}
