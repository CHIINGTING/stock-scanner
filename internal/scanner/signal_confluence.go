package scanner

import (
	"strings"

	"github.com/deep-huang/stock-scanner/internal/institution"
)

// ──────────────────────────────────────────────────────────────────────────────
// Signal confluence (R10-1). This is the ONLY place cross-domain combination logic lives
// (Trend × Institutional Flow × BIAS). It is strictly shadow/display: AttachConfluence
// runs as a post-pass and writes only WatchlistEntry.Confluence — it never touches Score /
// Action / RocketScore / WatchAction / sort / stop (SPEC §15). institution/signal.go stays
// institution-only and must never depend on BIAS/trend; that separation lives here.
// ──────────────────────────────────────────────────────────────────────────────

// Confluence signal types (P0 set only).
const (
	ConfluenceHealthyStrength         = "HEALTHY_STRENGTH"
	ConfluenceDistributionRisk        = "DISTRIBUTION_RISK"
	ConfluenceOversoldChipImprovement = "OVERSOLD_CHIP_IMPROVEMENT"
)

// ConfluenceSignal is one cross-domain read with a human-readable note (Traditional Chinese).
type ConfluenceSignal struct {
	Type string
	Note string
}

// ConfluenceView is the set of confluence signals for one stock.
type ConfluenceView struct {
	Signals []ConfluenceSignal
}

// AttachConfluence computes confluence for each entry from the already-attached
// Institution + Bias + existing MA20 trend. Entries without both layers, or with
// incomplete institution data, produce nothing.
func AttachConfluence(entries []WatchlistEntry) {
	for i := range entries {
		if cv := computeConfluence(entries[i].Institution, entries[i].Bias, entries[i].A.MA20Trend); cv != nil {
			entries[i].Confluence = cv
		}
	}
}

// computeConfluence reads institution + bias + trend and returns display-only combos, or
// nil. It never emits on incomplete institution data (§8) — a combo on data we cannot
// trust would be worse than silence.
func computeConfluence(inst *institution.StockChipView, bias *BiasView, ma20Trend string) *ConfluenceView {
	if inst == nil || bias == nil || !inst.Completeness.DataComplete {
		return nil
	}
	uptrend := isUptrend(ma20Trend)
	overheated := bias.Risk == BiasRiskHigh || bias.Risk == BiasRiskExtreme
	oversold := bias.Risk == BiasRiskOversold || bias.Risk == BiasRiskDeeplyOversold

	var out []ConfluenceSignal

	// A. 健康強勢：多頭排列 + 外資連買 + 投信連買 + BIAS20 尚未過熱。
	if uptrend && contBuy(inst.Foreign, 3) && contBuy(inst.Trust, 3) && !overheated {
		out = append(out, ConfluenceSignal{
			Type: ConfluenceHealthyStrength,
			Note: "趨勢與法人籌碼共振，乖離尚屬可控。",
		})
	}

	// B. 高檔派發風險：BIAS20 高 + 法人由買轉賣 / 投信連續賣超。
	if overheated && (inst.Foreign.Transition == institution.TransitionBuyToSell || contSell(inst.Trust, 3)) {
		out = append(out, ConfluenceSignal{
			Type: ConfluenceDistributionRisk,
			Note: "價格維持強勢，但法人籌碼出現轉弱，需留意高檔派發風險。",
		})
	}

	// C. 超跌後籌碼轉折：BIAS20 大幅負乖離 + 外資由賣轉買 + 投信開始連買。
	if oversold && (inst.Foreign.Transition == institution.TransitionSellToBuy || contBuy(inst.Trust, 2)) {
		out = append(out, ConfluenceSignal{
			Type: ConfluenceOversoldChipImprovement,
			Note: "超跌後籌碼初步改善，列入觀察，但尚未確認趨勢反轉。",
		})
	}

	if len(out) == 0 {
		return nil
	}
	return &ConfluenceView{Signals: out}
}

func isUptrend(ma20Trend string) bool { return strings.HasPrefix(ma20Trend, "↑") }

func contBuy(s institution.LegStats, days int) bool {
	return s.StreakComplete && s.ConsecutiveBuyDays >= days
}

func contSell(s institution.LegStats, days int) bool {
	return s.StreakComplete && s.ConsecutiveSellDays >= days
}
