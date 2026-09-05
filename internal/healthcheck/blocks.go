package healthcheck

import (
	"fmt"
	"time"

	"github.com/deep-huang/stock-scanner/internal/institution"
	"github.com/deep-huang/stock-scanner/internal/news"
)

// Presentation projections.
//
// These turn evidence into blocks a UI can render without deciding anything. Nothing here
// computes; every number was produced upstream by an existing module, and every absent number
// becomes a Value whose Display says why rather than showing 0.

// ── price ─────────────────────────────────────────────────────────────────────────────

// PriceBlock is the current market read.
type PriceBlock struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	Source string `json:"source,omitempty"`
	// BarDate is the date of the LAST bar actually used — which, for a historical check, is
	// the last trading day at or before asOf and is usually not asOf itself.
	BarDate string `json:"bar_date,omitempty"`
	// BarCount is how much history the reading had. It is the honest answer to "how much
	// should I trust the indicators below".
	BarCount int `json:"bar_count"`

	Close       Value `json:"close"`
	ChangePct   Value `json:"change_pct"`
	ChangeATR   Value `json:"change_atr"`
	Volume      Value `json:"volume"`
	VolumeRatio Value `json:"volume_ratio"`
}

const priceSourceNote = "internal/fetcher price cache"

func priceBlock(ev *Evidence) PriceBlock {
	b := PriceBlock{
		Status:   ev.PriceStatus,
		Reason:   ev.PriceReason,
		Source:   ev.PriceSource,
		BarCount: len(ev.Bars),
	}
	insufficient := Missing(StatusInsufficientData,
		fmt.Sprintf("only %d bars on or before %s", len(ev.Bars), ev.AsOf))
	b.Close, b.ChangePct, b.ChangeATR = insufficient, insufficient, insufficient
	b.Volume, b.VolumeRatio = insufficient, insufficient

	if len(ev.Bars) == 0 {
		none := Missing(StatusUnavailable, ev.PriceReason)
		b.Close, b.ChangePct, b.ChangeATR, b.Volume, b.VolumeRatio = none, none, none, none, none
		return b
	}

	last := ev.Bars[len(ev.Bars)-1]
	b.BarDate = last.Date.Format(dateLayout)
	// Close and volume are RAW FACTS from the bar, available even when the series is too
	// short for any analyzer to run. Reporting them is strictly better than a blank panel.
	b.Close = Num(last.Close, "TWD", 2, priceSourceNote).WithAsOf(b.BarDate)
	b.Volume = Num(float64(last.Volume), "股", 0, priceSourceNote).WithAsOf(b.BarDate)

	if ev.Entry == nil {
		// The derived readings need the scanner's analyzers, which need 30 bars. They are
		// deliberately NOT recomputed here: a second implementation would drift.
		return b
	}
	a := ev.Entry.A
	b.ChangePct = Num(a.PriceChangePct, "%", 2, "scanner.StockAnalysis").WithAsOf(b.BarDate)
	if a.PriceChangeATR != 0 {
		b.ChangeATR = Num(a.PriceChangeATR, "ATR", 2, "scanner.StockAnalysis").WithAsOf(b.BarDate)
	} else {
		b.ChangeATR = Missing(StatusInsufficientData, "ATR was unavailable for this bar")
	}
	if a.VolumeRatio > 0 {
		b.VolumeRatio = Num(a.VolumeRatio, "x", 2, "scanner.StockAnalysis").WithAsOf(b.BarDate)
	} else {
		b.VolumeRatio = Missing(StatusInsufficientData, "no 20-day average volume yet")
	}
	return b
}

// ── market ────────────────────────────────────────────────────────────────────────────

// MarketBlock is the market-wide regime for the date.
type MarketBlock struct {
	Status Status `json:"status"`
	Reason string `json:"reason,omitempty"`
	Regime string `json:"regime,omitempty"`
	Score  Value  `json:"score"`
	AsOf   string `json:"as_of,omitempty"`
	// Macro carries the interpreted monetary / rate / inflation / labour / volatility view.
	// It is the domain's own Research: already presentation-safe, and re-projecting it here
	// would be a second place for "elevated" to be defined.
	Macro any `json:"macro,omitempty"`
}

func marketBlock(ev *Evidence) MarketBlock {
	b := MarketBlock{
		Status: ev.Market.Status,
		Reason: ev.Market.Reason,
		Regime: ev.Market.Regime,
		AsOf:   ev.Market.AsOf,
		Score:  Missing(StatusUnavailable, ev.Market.Reason),
	}
	if ev.Market.Score != nil {
		b.Score = Num(*ev.Market.Score, "", 1, "internal/market snapshot").WithAsOf(ev.Market.AsOf)
	}
	if ev.Macro != nil {
		b.Macro = ev.Macro
	}
	return b
}

// ── institution ───────────────────────────────────────────────────────────────────────

// InstitutionLeg is one investor category's flow.
//
// Every net is a Value, so "no data" cannot render as a flat 0 — which for chip data is the
// single most misleading number a page could show: it reads as "the foreigners did nothing".
type InstitutionLeg struct {
	Name                string `json:"name"`
	TodayNet            Value  `json:"today_net"`
	Sum5D               Value  `json:"sum_5d"`
	Sum10D              Value  `json:"sum_10d"`
	Sum20D              Value  `json:"sum_20d"`
	ConsecutiveBuyDays  int    `json:"consecutive_buy_days"`
	ConsecutiveSellDays int    `json:"consecutive_sell_days"`
	Transition          string `json:"transition,omitempty"`
	NetBuyRatio         Value  `json:"net_buy_ratio"`
	// StreakComplete is false when the counted streak ran into a gap in the record, so a
	// "5 days of buying" that is really "5 observed days out of 8" is visibly weaker.
	StreakComplete bool `json:"streak_complete"`
}

// InstitutionBlock is the chip picture.
type InstitutionBlock struct {
	Status   Status           `json:"status"`
	Reason   string           `json:"reason,omitempty"`
	AsOfDate string           `json:"as_of_date,omitempty"`
	Legs     []InstitutionLeg `json:"legs,omitempty"`
	// ExpectedDays / ObservedDays say how complete the lookback was.
	ExpectedDays int `json:"expected_days,omitempty"`
	ObservedDays int `json:"observed_days,omitempty"`
	// TodayComplete is false when the latest session's own record is missing.
	TodayComplete bool `json:"today_complete"`
	// MissingDates are the expected sessions with no record for this stock. They are the
	// reason a cumulative can be PARTIAL, so the page can show the cause and not only the
	// caveat.
	MissingDates []string `json:"missing_dates,omitempty"`
}

const instSource = "internal/institution (TWSE/TPEx 三大法人)"

func institutionBlock(ev *Evidence) InstitutionBlock {
	b := InstitutionBlock{Status: ev.InstitutionStatus, Reason: ev.InstitutionReason}
	v := ev.Institution
	if v == nil {
		return b
	}
	b.AsOfDate = v.AsOfDate
	b.ExpectedDays = v.Completeness.ExpectedDays
	b.ObservedDays = v.Completeness.ObservedDays
	b.TodayComplete = v.Completeness.DataComplete
	b.MissingDates = append([]string(nil), v.Completeness.MissingDates...)
	today := v.Completeness.DataComplete
	b.Legs = []InstitutionLeg{
		institutionLeg("外資", v.Foreign, today),
		institutionLeg("投信", v.Trust, today),
		institutionLeg("自營商", v.Dealer, today),
		institutionLeg("三大法人合計", v.Total, today),
	}
	return b
}

// institutionLeg projects one leg.
//
// todayPresent is ChipCompleteness.DataComplete — whether this stock has a record for the
// as-of session at all. It has to be threaded in because LegStats cannot say so itself:
// legStats leaves TodayNet at its zero value when the day's record is absent, so a leg with
// no data and a leg that genuinely netted zero are the same int64 by the time they arrive
// here. Reading the raw field would put "0 股" on the page under an AVAILABLE status —
// "the foreigners did nothing today" — which is the single most misleading thing this block
// could say, and it would be wrong precisely on the days the source failed.
//
// The cumulatives get the same treatment one level down: LegStats.CumulativeComplete[n] is
// false when the n-day window was short or spanned a gap, so those sums stay printable but
// are marked PARTIAL rather than passed off as a clean n-day figure.
func institutionLeg(name string, s institution.LegStats, todayPresent bool) InstitutionLeg {
	leg := InstitutionLeg{
		Name:                name,
		ConsecutiveBuyDays:  s.ConsecutiveBuyDays,
		ConsecutiveSellDays: s.ConsecutiveSellDays,
		Transition:          s.Transition,
		StreakComplete:      s.StreakComplete,
		Sum5D:               cumulative(s.Sum5d, s.CumulativeComplete[5], 5),
		Sum10D:              cumulative(s.Sum10d, s.CumulativeComplete[10], 10),
		Sum20D:              cumulative(s.Sum20d, s.CumulativeComplete[20], 20),
	}
	if !todayPresent {
		leg.TodayNet = Missing(StatusUnavailable, "當日無此股法人買賣超紀錄")
		leg.NetBuyRatio = Missing(StatusUnavailable, "當日無此股法人買賣超紀錄")
		return leg
	}
	leg.TodayNet = Num(float64(s.TodayNet), "股", 0, instSource)
	leg.NetBuyRatio = Missing(StatusUnavailable, "同日成交量不可得，無法計算佔比")
	if s.NetBuyRatio != nil {
		leg.NetBuyRatio = Num(*s.NetBuyRatio, "%", 2, instSource)
	}
	return leg
}

// cumulative renders an n-day sum, marked PARTIAL when the window was incomplete.
func cumulative(sum int64, complete bool, window int) Value {
	v := Num(float64(sum), "股", 0, instSource)
	if complete {
		return v
	}
	return v.Qualified(StatusPartial, fmt.Sprintf("%d 日視窗內有缺漏交易日，此累計未涵蓋完整區間", window))
}

// ── news ──────────────────────────────────────────────────────────────────────────────

// NewsItem is one signal, flattened for display.
//
// PublishedAt and ObservedAt are BOTH carried: the first is when the world learned it, the
// second when this repo did, and a validator that confused them would build look-ahead into
// every backtest.
type NewsItem struct {
	EventID     string    `json:"event_id,omitempty"`
	Signal      string    `json:"signal"`
	Strength    int       `json:"strength"`
	Confidence  int       `json:"confidence"`
	Title       string    `json:"title,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Sectors     []string  `json:"sectors,omitempty"`
	Sources     []string  `json:"sources,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	ObservedAt  time.Time `json:"observed_at"`
	Conflict    bool      `json:"conflict,omitempty"`
	// Level is STOCK or SECTOR — a headline about the whole sector is weaker evidence about
	// this company than one that names it, and collapsing the two would hide that.
	Level string `json:"level"`
}

// NewsBlock is the news read.
type NewsBlock struct {
	Status Status     `json:"status"`
	Reason string     `json:"reason,omitempty"`
	Items  []NewsItem `json:"items,omitempty"`
	// Divergence is set when the stock-level and sector-level signals point opposite ways.
	Divergence bool `json:"divergence"`
	StockCount int  `json:"stock_count"`
	// SectorCount counts signals that reached this stock only through its sector.
	SectorCount int `json:"sector_count"`
}

func newsBlock(ev *Evidence) NewsBlock {
	b := NewsBlock{Status: ev.NewsStatus, Reason: ev.NewsReason}
	v := ev.News
	if v == nil {
		return b
	}
	b.Divergence = v.Divergence
	b.StockCount = len(v.StockItems)
	b.SectorCount = len(v.SectorItems)
	for _, sig := range v.StockItems {
		b.Items = append(b.Items, newsItem(sig, "STOCK"))
	}
	for _, sig := range v.SectorItems {
		b.Items = append(b.Items, newsItem(sig, "SECTOR"))
	}
	return b
}

func newsItem(sig news.NewsSignal, level string) NewsItem {
	it := NewsItem{
		EventID:     sig.EventID,
		Signal:      string(sig.Signal),
		Strength:    sig.Strength,
		Confidence:  sig.Confidence,
		Title:       sig.RawTitle,
		Summary:     sig.Summary,
		Sectors:     sig.Sectors,
		PublishedAt: sig.PublishedAt,
		ObservedAt:  sig.ObservedAt,
		Conflict:    sig.Conflict,
		Level:       level,
	}
	for _, s := range sig.Sources {
		it.Sources = append(it.Sources, s.Provider)
	}
	return it
}
