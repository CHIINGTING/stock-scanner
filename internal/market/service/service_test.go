package service

import (
	"context"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/market/model"
	"github.com/deep-huang/stock-scanner/internal/market/provider/mock"
)

func silent(string, ...any) {}

func fullMock() *mock.Provider {
	return &mock.Provider{
		FuturesData:  model.FuturesData{NetPosition: -83390, DailyChange: -2324},
		CashData:     model.ForeignCashData{Buy: 480, Sell: 250},
		MarginData:   model.MarginData{PriceChangePct: 0.8, MarginBalanceChg: -30},
		OptionsData:  model.OptionsData{PutCallRatio: 0.65},
		CurrencyData: model.CurrencyData{USDTWDChg: -0.15},
		VIXData:      model.VIXData{Value: 16.2},
		SectorData: []model.SectorScore{
			{Name: "Memory", CycleScore: 88, NewsScore: 45},
			{Name: "Steel", CycleScore: 35, NewsScore: -30},
		},
	}
}

func TestBuildFullDashboard(t *testing.T) {
	svc := NewService(fullMock(), model.MarketConfig{}.Defaulted(), silent)
	d := svc.Build(context.Background(), "2026-07-14")
	if d.MarketScore.AvailableCount != 7 {
		t.Errorf("AvailableCount = %d, want 7", d.MarketScore.AvailableCount)
	}
	if d.Regime.Regime == model.RegimeUnknown {
		t.Error("full-data dashboard should not be Unknown")
	}
	if len(d.Sectors) != 2 {
		t.Errorf("Sectors = %d, want 2", len(d.Sectors))
	}
	if len(d.TopOpportunities) == 0 || len(d.TopRisks) == 0 {
		t.Errorf("expected opportunities and risks, got %d/%d", len(d.TopOpportunities), len(d.TopRisks))
	}
}

// Every source down → Unknown, zero modules, no panic, empty sectors.
func TestBuildAllMissing(t *testing.T) {
	p := &mock.Provider{
		FuturesMissing: true, CashMissing: true, MarginMissing: true, OptionsMissing: true,
		CurrencyMissing: true, VIXMissing: true, SectorMissing: true,
	}
	svc := NewService(p, model.MarketConfig{}.Defaulted(), silent)
	d := svc.Build(context.Background(), "2026-07-14")
	if d.MarketScore.AvailableCount != 0 {
		t.Errorf("AvailableCount = %d, want 0", d.MarketScore.AvailableCount)
	}
	if d.Regime.Regime != model.RegimeUnknown {
		t.Errorf("Regime = %q, want Unknown", d.Regime.Regime)
	}
	if len(d.Sectors) != 0 || len(d.TopOpportunities) != 0 || len(d.TopRisks) != 0 {
		t.Error("no sector data → empty sectors/opportunities/risks")
	}
}

// One source down must be isolated: the module is unavailable, the rest still build.
func TestBuildIsolatesOneFailure(t *testing.T) {
	p := fullMock()
	p.FuturesMissing = true
	svc := NewService(p, model.MarketConfig{}.Defaulted(), silent)
	d := svc.Build(context.Background(), "2026-07-14")
	if d.MarketScore.AvailableCount != 6 {
		t.Errorf("AvailableCount = %d, want 6", d.MarketScore.AvailableCount)
	}
	var fut *model.ModuleScore
	for i := range d.MarketScore.Modules {
		if d.MarketScore.Modules[i].Name == model.ModuleForeignFutures {
			fut = &d.MarketScore.Modules[i]
		}
	}
	if fut == nil || fut.Available {
		t.Errorf("ForeignFutures module = %+v, want present & unavailable", fut)
	}
}
