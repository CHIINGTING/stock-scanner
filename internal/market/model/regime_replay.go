package model

import (
	"fmt"
	"strings"
	"time"
)

// RegimeReplay is a regime POINT-IN-TIME REPLAYED from the local price cache. It is a
// different artifact from Snapshot, and that difference is the whole point of this file.
//
// # Why a separate type instead of a provenance flag on Snapshot
//
// A replay cannot fill a Snapshot honestly. Snapshot.Validate requires Score ∈ [0,100],
// and a replay HAS NO SCORE: score needs multi-source evidence (futures OI, foreign cash
// flow, margin), none of which has a historical producer. Writing 0 would persist "maximum
// bearishness" for every replayed day — the MISSING → ZERO failure this codebase exists to
// avoid. Adding a nullable score plus a provenance field would also force a
// SnapshotSchemaVersion bump on every reader of the archive.
//
// So the separation is PHYSICAL rather than advisory:
//
//   - a different Go type, which simply has no Score/Confidence/Evidence/Flags fields, so
//     there is nothing to fabricate (enforced by a reflection allowlist test);
//   - different JSON keys ("replay_date", "replayed_regime", "replay_schema_version"), so a
//     replay file fails Snapshot.Validate and a snapshot file fails RegimeReplay.Validate,
//     in several independent places each;
//   - a different filename prefix and directory (regime_<date>.json in data/market_replay,
//     never market_<date>.json in data/market), so a market_*.json glob cannot pick it up;
//   - a mandatory self-describing caveat, enforced in Validate.
//
// # What a replay is NOT
//
// A replayed regime MAY DIFFER from the regime a live run recorded for the same day, for TWO
// independent reasons. Both are announced on every row, because each alone is enough to make
// a mixed series wrong.
//
//  1. POSTURE. The live pipeline can see institutional posture; a replay always passes
//     PostureUnknown because nothing produces posture retroactively. Rules R4 and R5 read
//     Posture, so a day the live run called DISTRIBUTION can replay as BULL or SIDEWAYS.
//
//  2. UNIVERSE (survivorship). Breadth is measured over the symbols in the price cache
//     TODAY, not over the symbols that were listed on the replayed day. CacheFeed.Universe
//     globs *.json, so membership is a property of the cache's current contents: a stock
//     added to the watchlist last week contributes its own back-history to every replayed
//     day, and a stock that was delisted or dropped contributes to none. The breadth_* and
//     advancing_ratio metrics therefore DIFFER from the live snapshot for the same session.
//     Measured on 2026-08-31, the one overlapping day: breadth_above_ma20 46.65 live vs
//     50.56 replayed (+3.91pp), advancing_ratio 22.39 vs 30.60 (+8.21pp),
//     breadth_valid_count 1925 vs 1974, while all ten price metrics matched to the last
//     decimal. That gap is the same order of magnitude as the distance to a threshold —
//     BreadthHealthyPct is 55 and the replayed 50.56 sits 4.44pp away — so on a day nearer
//     the boundary it is enough to flip BreadthQuality, which feeds R3/R6/R7/R8/R9 and
//     therefore the regime itself. (On 2026-08-31 it did not: both sides read COOLING.)
//
// Treating the two archives as interchangeable would silently corrupt any study built on the
// mixture. InputExtent.BreadthSymbolsLoaded records the membership each run actually used, so
// two re-runs that disagree are visible rather than silent.
type RegimeReplay struct {
	// Kind is a constant discriminator. It exists so that a human or a tool looking at the
	// raw bytes cannot mistake the file for a live snapshot.
	Kind string `json:"kind"`

	Date   string `json:"replay_date"`     // YYYY-MM-DD — the as-of session being replayed
	Regime Regime `json:"replayed_regime"` // deliberately NOT "regime": see the type doc
	RuleID string `json:"rule_id"`         // which decision-table row fired

	// Structure is the layer-1 + layer-2 view the decision was made on. Posture is ALWAYS
	// PostureUnknown here, and Validate rejects anything else.
	Structure StructureView `json:"structure"`

	Reasons []string `json:"reasons,omitempty"`
	// Caveats always carries BOTH mandatory replay caveats — posture and universe. Validate
	// rejects a row missing either.
	Caveats []string `json:"caveats"`

	ReplayedAt time.Time `json:"replayed_at"`           // wall clock of the replay run, not of the session
	SchemaVer  int       `json:"replay_schema_version"` // ReplaySchemaVersion

	// InputExtent records exactly what this decision was allowed to see. It is the audit
	// trail for the point-in-time claim: BenchmarkLast and BreadthLast must both equal Date,
	// so a replay that peeked at a later bar cannot be persisted as if it had not.
	InputExtent ReplayExtent `json:"input_extent"`
}

// ReplayExtent is the observable window behind one replayed day.
type ReplayExtent struct {
	BenchmarkSymbol string `json:"benchmark_symbol"`
	BenchmarkBars   int    `json:"benchmark_bars"` // = len(bars[:i+1])
	BenchmarkFirst  string `json:"benchmark_first_date"`
	BenchmarkLast   string `json:"benchmark_last_date"` // must equal Date

	BreadthIndex    int    `json:"breadth_index"`      // position on the breadth axis
	BreadthFirst    string `json:"breadth_first_date"` // the axis start, for reproducibility
	BreadthLast     string `json:"breadth_last_date"`  // must equal Date
	BreadthUniverse int    `json:"breadth_universe"`   // stocks actually measurable that day

	// BreadthSymbolsLoaded is how many symbol series the breadth panel was built from — the
	// cache's membership at replay time, NOT the day's real listed universe.
	//
	// It is here because the survivorship bias in caveat 2 of the type doc is otherwise
	// invisible: two re-runs weeks apart produce identical price metrics and different
	// breadth, with nothing in the file to say why. This is the number that differs. Compare
	// it across runs before comparing their breadth.
	BreadthSymbolsLoaded int `json:"breadth_symbols_loaded"`
}

// ReplaySchemaVersion is bumped whenever the persisted replay shape changes in a way a
// reader must know about. It is INDEPENDENT of SnapshotSchemaVersion on purpose: replays and
// live snapshots are separate archives and must be free to evolve separately.
//
//	1 — initial: regime + rule + structure view (posture always UNKNOWN) + input extent
//	2 — added input_extent.breadth_symbols_loaded and the mandatory
//	    REPLAYED_REGIME_UNIVERSE_AS_CACHED caveat. A v1 file fails Validate on the missing
//	    caveat, which is intended: v1 rows do not disclose the survivorship bias, and
//	    data/market_replay is gitignored and regenerated in ~51ms.
const ReplaySchemaVersion = 2

// ReplayKind is the value of RegimeReplay.Kind. Stable string; never rename.
const ReplayKind = "MARKET_REGIME_REPLAY"

// CaveatReplayCode is the stable SCREAMING_SNAKE prefix of the posture caveat. Validate
// requires it, so a replay that does not announce itself cannot reach disk.
const CaveatReplayCode = "REPLAYED_REGIME_POSTURE_UNKNOWN"

// CaveatReplayUniverseCode is the stable prefix of the SECOND mandatory caveat. It exists
// because the posture caveat alone was misleading: it read as if posture were the only way a
// replay and a live snapshot can disagree, and on the one day both archives cover, posture
// was the one thing they AGREED on (both UNKNOWN) while breadth differed by 3.91pp. Validate
// requires this one exactly as strictly as the posture one.
const CaveatReplayUniverseCode = "REPLAYED_REGIME_UNIVERSE_AS_CACHED"

// CaveatReplayPostureUnknown is the full posture caveat text attached to every replayed day.
const CaveatReplayPostureUnknown = CaveatReplayCode +
	": 此判斷由本地價格快取重播 (point-in-time) 產生，法人籌碼態勢 (posture) 一律為 UNKNOWN，" +
	"且沒有 Market Score。與當日 live snapshot 記錄的 regime 可能不同，不可混用。"

// CaveatReplayUniverseAsCached is the full universe caveat text attached to every replayed
// day.
const CaveatReplayUniverseAsCached = CaveatReplayUniverseCode +
	": 廣度 (breadth_*、advancing_ratio) 是以價格快取「目前」的成員資格計算的，不是重播當日" +
	"市場上真實存在的 universe，因此含存活者偏差 (survivorship bias)：現在才加入快取的個股會" +
	"把自己的歷史算進每一個過去的日子，已下市或已移除的個股則完全不算。同一天的 live snapshot " +
	"其 breadth 數字可能不同（2026-08-31 實測 breadth_above_ma20 live 46.65 vs replay 50.56，" +
	"差 3.91pp），而 BreadthQuality 會餵給 R3/R6/R7/R8/R9，故靠近門檻的日子足以翻轉 regime。" +
	"input_extent.breadth_symbols_loaded 記錄本次實際使用的成員數。"

// NewRegimeReplay returns a replay pre-filled with the fields every writer must set, in the
// UNKNOWN state, with BOTH mandatory caveats already attached. Starting from UNKNOWN rather
// than the zero value matters: the zero value of Regime is "", a regime nobody defined.
func NewRegimeReplay(date string, now time.Time) *RegimeReplay {
	return &RegimeReplay{
		Kind:       ReplayKind,
		Date:       date,
		Regime:     RegimeUnknown,
		RuleID:     RuleUnknown,
		ReplayedAt: now,
		SchemaVer:  ReplaySchemaVersion,
		Structure: StructureView{
			Structure: StructureUnknown,
			Breadth:   BreadthUnknown,
			Posture:   PostureUnknown,
		},
		Caveats: []string{CaveatReplayPostureUnknown, CaveatReplayUniverseAsCached},
	}
}

// Validate catches the mistakes that would let a replay be mistaken for a live read, or let
// a malformed record enter the replay archive. It runs before every write and after every
// read, for the same reason Snapshot.Validate does: the archive's only value is that old
// entries stay trustworthy.
func (r *RegimeReplay) Validate() error {
	if r == nil {
		return fmt.Errorf("market: nil regime replay")
	}
	// Kind first: it is the cheapest way to reject a live snapshot that was decoded into
	// this type by mistake.
	if r.Kind != ReplayKind {
		return fmt.Errorf("market: regime replay kind %q, want %q (is this a live snapshot?)",
			r.Kind, ReplayKind)
	}
	if r.Date == "" {
		return fmt.Errorf("market: regime replay has no replay_date")
	}
	if _, err := time.Parse("2006-01-02", r.Date); err != nil {
		return fmt.Errorf("market: regime replay date %q is not YYYY-MM-DD", r.Date)
	}
	if r.SchemaVer <= 0 {
		return fmt.Errorf("market: regime replay %s has no replay schema version", r.Date)
	}
	if r.ReplayedAt.IsZero() {
		return fmt.Errorf("market: regime replay %s has no replayed_at", r.Date)
	}
	if !r.Regime.Valid() {
		return fmt.Errorf("market: regime replay %s has undefined regime %q", r.Date, r.Regime)
	}
	if !r.Structure.Structure.Valid() || !r.Structure.Breadth.Valid() || !r.Structure.Posture.Valid() {
		return fmt.Errorf("market: regime replay %s has an undefined structural state", r.Date)
	}
	// THE defining invariant of a replay. Nothing produces institutional posture
	// retroactively, so a replay claiming to know it is claiming to be a live read.
	if r.Structure.Posture != PostureUnknown {
		return fmt.Errorf("market: regime replay %s claims posture %s — a replay has no "+
			"institutional evidence and must be UNKNOWN", r.Date, r.Structure.Posture)
	}
	if !r.Structure.Benchmark.Valid() {
		return fmt.Errorf("market: regime replay %s has undefined benchmark %q",
			r.Date, r.Structure.Benchmark)
	}
	// A known regime must name the rule that produced it, otherwise the call is unauditable.
	if r.Regime != RegimeUnknown && r.RuleID == "" {
		return fmt.Errorf("market: regime replay %s has regime %s but no rule id", r.Date, r.Regime)
	}
	// Self-description is mandatory: a reader must not have to know the directory layout to
	// learn that posture was unknown, NOR to learn that breadth was measured over today's
	// cache membership rather than the replayed day's real universe. Both caveats are
	// required, because either omission on its own lets a reader believe the archives are
	// comparable in a way they are not.
	if !r.carriesCaveat(CaveatReplayCode) {
		return fmt.Errorf("market: regime replay %s carries no %s caveat", r.Date, CaveatReplayCode)
	}
	if !r.carriesCaveat(CaveatReplayUniverseCode) {
		return fmt.Errorf("market: regime replay %s carries no %s caveat — breadth is "+
			"computed over the cache's CURRENT membership and must say so", r.Date,
			CaveatReplayUniverseCode)
	}
	// The point-in-time audit trail. Both windows must END on the replayed date; a wider
	// extent means the decision saw a bar it had no right to.
	if r.InputExtent.BenchmarkBars <= 0 {
		return fmt.Errorf("market: regime replay %s records no benchmark bars", r.Date)
	}
	if r.InputExtent.BenchmarkLast != r.Date {
		return fmt.Errorf("market: regime replay %s saw benchmark up to %q — not point-in-time",
			r.Date, r.InputExtent.BenchmarkLast)
	}
	if r.InputExtent.BreadthLast != r.Date {
		return fmt.Errorf("market: regime replay %s saw breadth up to %q — not point-in-time",
			r.Date, r.InputExtent.BreadthLast)
	}
	if r.InputExtent.BreadthIndex < 0 {
		return fmt.Errorf("market: regime replay %s has a negative breadth index", r.Date)
	}
	return nil
}

// announcesItself reports whether BOTH mandatory caveats are present. "Self-describing"
// means disclosing every known way a replay differs from a live read, not just the first one
// that was written down.
func (r *RegimeReplay) announcesItself() bool {
	return r.carriesCaveat(CaveatReplayCode) && r.carriesCaveat(CaveatReplayUniverseCode)
}

func (r *RegimeReplay) carriesCaveat(code string) bool {
	for _, c := range r.Caveats {
		if strings.HasPrefix(c, code) {
			return true
		}
	}
	return false
}
