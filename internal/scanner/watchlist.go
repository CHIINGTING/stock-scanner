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

// WatchlistEntry is the full decision sheet for one watchlist stock.
type WatchlistEntry struct {
	A StockAnalysis // 重用既有現價/指標/評分

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

		// Consolidation + backtest + rocket.
		e.Consol = analyzeConsolidation(item.Candles, ind, flowDir == FlowInflow)
		e.Backtest = s.runBacktest(item.Candles, ind, members[e.Sector])

		rk := computeRocket(rocketInput{
			candles:           item.Candles,
			ind:               ind,
			consol:            e.Consol,
			bt:                e.Backtest,
			flowDir:           flowDir,
			sectorStage:       e.SectorStage,
			sectorAvgReturn20: sectorAvg,
			hasSector:         e.HasSector,
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

		out = append(out, e)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].RocketScore > out[j].RocketScore
	})
	return out
}
