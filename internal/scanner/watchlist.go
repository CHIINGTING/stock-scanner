package scanner

import (
	"sort"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────
// Watchlist = 飆股候選追蹤系統
//
// 觀察清單不是用來找股票（那是 Scanner 的事），而是回答：這檔已篩選標的是不是
// 正在準備發動？型態過去成功率高不高？要等突破還是拉回？失敗跌破哪裡移除？
// ──────────────────────────────────────────────────────────────────────────────

// ShadowSignals holds C6a "shadow" results: computed & attached for inspection,
// but NOT consumed by any score / stage / action / sort. Each field is nil unless
// its feature flag is on. Scoring integration (with double-count guardrails) is C6b.
type ShadowSignals struct {
	RS       *RSResult      `json:"rs,omitempty"`       // enable_rs_rank
	NewHigh  *NewHighResult `json:"new_high,omitempty"` // enable_new_high
	VCP      *VCPResult     `json:"vcp,omitempty"`      // enable_vcp
	Momentum *MomentumState `json:"momentum,omitempty"` // enable_momentum_flow
}

// WatchlistEntry is the full decision sheet for one watchlist stock.
type WatchlistEntry struct {
	A StockAnalysis // 重用既有現價/指標/評分

	// Shadow signals (C6a): nil unless at least one shadow flag is on; never scored here.
	Shadow *ShadowSignals `json:"shadow,omitempty"`

	// ── 族群輪動連動 ─────────────────────────────────────────────────────────
	Sector         string
	HasSector      bool
	SectorFlowDir  string      // INFLOW/OUTFLOW/NEUTRAL（短線流向）
	SectorMidLabel string      // 強/中/弱（20 日強度）
	SectorStage    RotationStage // 整體階段（EARLY/CONFIRMED/HOT/LATE）
	SectorNote     string      // 一句說明

	// ── 型態 / 回測 / 飆股 ───────────────────────────────────────────────────
	Consol   Consolidation
	Backtest Backtest

	RocketScore   int
	RocketStage   RocketStage
	ExplosionProb string
	DaysToWatch   string

	// ── 價位計畫 ─────────────────────────────────────────────────────────────
	BreakoutPrice  float64
	SupportPrice   float64
	StopLossPrice  float64
	EntryZone      string
	TakeProfitZone string

	// ── 操作 ─────────────────────────────────────────────────────────────────
	WatchAction WatchAction
	Reasons     []string
	RiskLabel   string
	RiskWarning string
}

// EnrichWatchlist turns raw watchlist OHLCV into rocket-candidate decision sheets,
// linking each stock to its sector's rotation state. Sorted by RocketScore desc.
func (s *Scanner) EnrichWatchlist(
	items []fetcher.StockData,
	sectorOf map[string]string, // code → sector name (highest-ranked sector)
	rot map[string]*SectorRotation, // sector name → rotation
	members map[string][]fetcher.StockData, // sector name → member candles
	rsTable map[string]RSResult, // C6a: full-market RS (nil when RS disabled); shadow-only
) []WatchlistEntry {
	out := make([]WatchlistEntry, 0, len(items))

	for _, item := range items {
		if len(item.Candles) < 30 {
			continue
		}
		ind := s.calcIndicators(item.Candles)
		a := s.analyze(item, ind)

		e := WatchlistEntry{A: a}

		// Sector linkage.
		flowDir := FlowNeutral
		var sectorAvg float64
		if name := sectorOf[item.Symbol]; name != "" {
			e.Sector = name
			if sr := rot[name]; sr != nil {
				e.HasSector = true
				e.SectorFlowDir = sr.ShortTermFlowDir
				e.SectorMidLabel = sr.MidTermLabel
				e.SectorStage = sr.Stage
				e.SectorNote = sr.ShortTermNote
				flowDir = sr.ShortTermFlowDir
				sectorAvg = sr.AvgReturn20
			}
		}
		if e.SectorFlowDir == "" {
			e.SectorFlowDir = FlowNeutral
		}

		// Consolidation + backtest.
		e.Consol = analyzeConsolidation(item.Candles, ind, flowDir == FlowInflow)
		e.Backtest = s.runBacktest(item.Candles, ind, members[e.Sector])

		// ── Shadow signals: computed BEFORE rocket so C6b guardrail scoring can
		// consume them. The whole container stays nil unless a shadow flag is on. ──
		var shadow *ShadowSignals
		if s.cfg.EnableRSRank || s.cfg.EnableNewHigh || s.cfg.EnableVCP || s.cfg.EnableMomentumFlow {
			shadow = &ShadowSignals{}
			if s.cfg.EnableRSRank && rsTable != nil {
				if r, ok := rsTable[item.Symbol]; ok {
					shadow.RS = &r
				}
			}
			if s.cfg.EnableNewHigh {
				nh := computeNewHigh(item.Candles, a.VolumeRatio, a.RSI, newHighConfigFrom(s.cfg))
				shadow.NewHigh = &nh
			}
			if s.cfg.EnableVCP {
				v := ComputeVCP(item.Candles, vcpConfigFrom(s.cfg))
				shadow.VCP = &v
			}
			if s.cfg.EnableMomentumFlow {
				m := ComputeMomentum(item.Candles, ind.RSI, a.VolumeRatio, momentumConfigFrom(s.cfg))
				shadow.Momentum = &m
			}
		}

		// C6b: shadow signals may influence scoring only when the master guardrail
		// flag is on (gating happens inside computeRocket).
		var vcpShadow *VCPResult
		var nhShadow *NewHighResult
		if shadow != nil {
			vcpShadow = shadow.VCP // C6b-1: corrects g3 base-quality
			nhShadow = shadow.NewHigh // C6b-2: replaces g3 NearPreviousHigh sub-score
		}

		rk := computeRocket(rocketInput{
			candles:           item.Candles,
			ind:               ind,
			consol:            e.Consol,
			bt:                e.Backtest,
			flowDir:           flowDir,
			sectorStage:       e.SectorStage,
			sectorAvgReturn20: sectorAvg,
			hasSector:         e.HasSector,
			guardrailScoring:  s.cfg.EnableSignalGuardrailScoring,
			vcp:               vcpShadow,
			newHigh:           nhShadow,
		})
		e.RocketScore = rk.Score
		e.RocketStage = rk.Stage
		e.ExplosionProb = rk.ExplosionProb
		e.DaysToWatch = rk.DaysToWatch
		e.BreakoutPrice = rk.BreakoutPrice
		e.SupportPrice = rk.SupportPrice
		e.StopLossPrice = rk.StopLossPrice
		e.EntryZone = rk.EntryZone
		e.TakeProfitZone = rk.TakeProfitZone
		e.WatchAction = rk.WatchAction
		e.Reasons = rk.Reasons
		e.RiskLabel = rk.RiskLabel
		e.RiskWarning = rk.RiskWarning

		// Attach shadow for inspection (still never scored beyond the gated VCP path).
		e.Shadow = shadow

		out = append(out, e)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].RocketScore > out[j].RocketScore
	})
	return out
}

// BuildRSTable computes full-market RS percentiles keyed by symbol, for C6a shadow
// attachment. Returns nil when RS is disabled. Shadow-only: callers must not feed
// the result into any score or ranking (that is C6b).
func (s *Scanner) BuildRSTable(stocks []fetcher.StockData) map[string]RSResult {
	if !s.cfg.EnableRSRank {
		return nil
	}
	inputs := make([]RSInput, 0, len(stocks))
	for _, st := range stocks {
		inputs = append(inputs, RSInput{Symbol: st.Symbol, Name: st.Name, Candles: st.Candles})
	}
	results := CalculateRSRanks(inputs, rsConfigFrom(s.cfg))
	out := make(map[string]RSResult, len(results))
	for _, r := range results {
		out[r.Symbol] = r
	}
	return out
}
