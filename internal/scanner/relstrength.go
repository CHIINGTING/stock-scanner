package scanner

import (
	"sort"
	"strings"

	"github.com/deep-huang/stock-scanner/internal/fetcher"
)

// ──────────────────────────────────────────────────────────────────────────────
// RS Rank — Relative Strength Ranking (C2)
//
// 全市場普通股的相對強弱百分位。C2 只建立「資料模型 + 母體過濾 + 計算 helper + config」，
// 不接入既有 scoring / report / watchlist / rotation。所有函式皆為 pure，僅在被明確呼叫時
// 才運算；EnableRSRank=false 時 pipeline 不會呼叫它們（golden regression by construction）。
//
// 後續：C6 才決定 RS 如何納入 rocket_candidate_score。
// ──────────────────────────────────────────────────────────────────────────────

// Default knobs (used when config leaves them zero).
const (
	defaultRSLookbackDays   = 120
	defaultRSMinHistoryDays = 100
)

// RSConfig is the resolved RS configuration (defaults applied).
type RSConfig struct {
	Enable              bool
	LookbackDays        int
	MinHistoryDays      int
	ExcludeNonCommon    bool
	UseAdjustedClose    bool // global UseAdjustedClose OR rs_use_adjusted_close
	LeadershipThreshold float64
	WatchThreshold      float64
}

// rsConfigFrom resolves an RSConfig from the scanner Config, applying defaults.
// Note: price source per spec = PriceForCalc(candle, UseAdjustedClose || RSUseAdjustedClose).
func rsConfigFrom(cfg Config) RSConfig {
	rc := RSConfig{
		Enable:              cfg.EnableRSRank,
		LookbackDays:        cfg.RSLookbackDays,
		MinHistoryDays:      cfg.RSMinHistoryDays,
		ExcludeNonCommon:    cfg.RSUniverseExcludeNonCommonStock,
		UseAdjustedClose:    cfg.UseAdjustedClose || cfg.RSUseAdjustedClose,
		LeadershipThreshold: cfg.RSLeadershipThreshold,
		WatchThreshold:      cfg.RSWatchThreshold,
	}
	if rc.LookbackDays <= 0 {
		rc.LookbackDays = defaultRSLookbackDays
	}
	if rc.MinHistoryDays <= 0 {
		rc.MinHistoryDays = defaultRSMinHistoryDays
	}
	return rc
}

// RSInput is one candidate for RS ranking.
type RSInput struct {
	Symbol  string
	Name    string
	Candles []fetcher.Candle
}

// RSResult is the per-stock RS outcome.
type RSResult struct {
	Symbol             string
	RSUniverseEligible bool    // passed the common-stock universe filter
	Computed           bool    // RSReturnPct is valid (eligible + enough history + valid prices)
	RSReturnPct        float64 // lookback-window return (%)
	RSRankPercentile   float64 // 0–100 percentile across the eligible+computed pool (higher = stronger)
	RSScore            float64 // 0–100 standardized score for the scanner (v1: == percentile)
}

// IsRSUniverseEligible decides, best-effort, whether a symbol is an ordinary
// listed/OTC common stock fit for the RS universe.
//
// We currently have no security-type metadata on StockData (only code + name),
// so this is a heuristic on the Taiwan code/name conventions:
//   - common stocks are 4-digit numeric codes whose first digit is 1–9;
//   - ETF / ETN / REIT / 受益證券 / 權證 use a leading "0" and/or 5–6 digit codes → excluded;
//   - 特別股 carry a letter suffix (e.g. 2891B) → not all-digits → excluded;
//   - DR / TDR names carry a "-DR" marker (e.g. 巨騰-DR) → excluded;
//   - "-KY" issuers are foreign-registered COMMON stock → kept.
//
// TODO(C2): 全額交割股 / 停止交易 / 下市櫃 無法由代號或名稱判斷，需要真正的 security-type
// 來源（fetcher 尚未提供）。先以 history/volume 不足在計算階段排除，並可由 exclude list 補強。
func IsRSUniverseEligible(symbol, name string, cfg RSConfig) bool {
	if !cfg.ExcludeNonCommon {
		return true
	}
	code := strings.TrimSpace(symbol)
	// Must be exactly 4 ASCII digits (excludes 5–6 digit warrants/ETN/long-ETF/REIT
	// and letter-suffixed preferred shares).
	if len(code) != 4 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	// Leading 0 → ETF (0050/0056) / non-common series.
	if code[0] == '0' {
		return false
	}
	// DR / TDR by name convention.
	if strings.Contains(name, "-DR") {
		return false
	}
	return true
}

// rsReturnPct computes the lookback-window return for one stock.
// Returns (pct, true) only when: eligible history length, a valid bar exists
// `LookbackDays` ago, and both prices are > 0. Never panics; never returns a
// misleading 0 as a "valid" value (ok=false instead).
func rsReturnPct(candles []fetcher.Candle, cfg RSConfig) (float64, bool) {
	n := len(candles)
	// Need enough history overall, and a bar LookbackDays back.
	if n < cfg.MinHistoryDays || n <= cfg.LookbackDays {
		return 0, false
	}
	cur := fetcher.PriceForCalc(candles[n-1], cfg.UseAdjustedClose)
	past := fetcher.PriceForCalc(candles[n-1-cfg.LookbackDays], cfg.UseAdjustedClose)
	if cur <= 0 || past <= 0 {
		return 0, false
	}
	return (cur/past - 1) * 100, true
}

// CalculateRSRanks computes RS for a batch of candidates and ranks the eligible,
// computable ones into a 0–100 percentile (stronger return → higher percentile).
//
// Pure function: does not mutate inputs, no globals, deterministic. Order of the
// returned slice matches `items`.
func CalculateRSRanks(items []RSInput, cfg RSConfig) []RSResult {
	results := make([]RSResult, len(items))

	// 1. Eligibility + return.
	pool := make([]float64, 0, len(items))
	for i, it := range items {
		r := RSResult{Symbol: it.Symbol}
		r.RSUniverseEligible = IsRSUniverseEligible(it.Symbol, it.Name, cfg)
		if r.RSUniverseEligible {
			if pct, ok := rsReturnPct(it.Candles, cfg); ok {
				r.Computed = true
				r.RSReturnPct = pct
				pool = append(pool, pct)
			}
		}
		results[i] = r
	}

	// 2. Percentile over the computed+eligible pool.
	// 樣本不足（pool 為空）→ 不硬算，Computed 維持但不指派 percentile（留 0，呼叫端應看 Computed/pool）。
	n := len(pool)
	if n == 0 {
		return results
	}
	sorted := make([]float64, n)
	copy(sorted, pool)
	sort.Float64s(sorted)

	for i := range results {
		if !results[i].Computed {
			continue
		}
		v := results[i].RSReturnPct
		below := sort.SearchFloat64s(sorted, v)                       // count strictly < v
		hi := sort.Search(n, func(k int) bool { return sorted[k] > v }) // first > v
		equal := hi - below
		p := (float64(below) + 0.5*float64(equal)) / float64(n) * 100 // mid-rank
		results[i].RSRankPercentile = p
		results[i].RSScore = p // v1: standardized score == percentile
	}
	return results
}
