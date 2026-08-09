package model

import (
	"fmt"
	"time"
)

// SnapshotSchemaVersion is bumped whenever the persisted shape changes in a way a reader
// must know about. Stored snapshots are the substrate for future validation runs, so a
// reader from 2027 has to be able to tell what it is looking at.
//
//	1 — initial: score + three-layer regime + evidence + raw observation
const SnapshotSchemaVersion = 1

// Flags are conditions that are ORTHOGONAL to the regime rather than competing with it.
// R9 originally modelled CRASH and RECOVERY as regimes; they are events, not structures,
// and giving them regime slots made them fight DISTRIBUTION for the same day. As flags they
// can coexist with any regime ("BEAR + crash event", "BULL_PULLBACK + high volatility").
type Flags struct {
	HighVolatility bool `json:"high_volatility"` // realized vol20 in its own top percentile
	CrashEvent     bool `json:"crash_event"`     // sharp drop with collapsed breadth
	RecoveryEvent  bool `json:"recovery_event"`  // reclaimed MA20 shortly after a crash
}

// Snapshot is one trading day's complete market read — the artifact written to
// data/market/market_YYYY-MM-DD.json and the unit of all future backtesting.
//
// It stores CONCLUSIONS (score/regime/confidence), the REASONING (structure view + rule id
// + evidence), and the OBSERVATION (raw). Keeping all three means a later validation run
// can ask "would a different threshold have called this day differently?" and answer it
// offline, without re-fetching anything from TWSE or TAIFEX.
//
// Score and Regime are produced by SEPARATE engines and never pass through each other:
// there is no code path where a score threshold selects a regime.
type Snapshot struct {
	Date        string    `json:"date"` // YYYY-MM-DD
	GeneratedAt time.Time `json:"generated_at"`
	SchemaVer   int       `json:"schema_version"`

	Score      float64 `json:"score"`      // 0..100, higher = more bullish
	Confidence float64 `json:"confidence"` // 0..1, how much to trust today's read

	Regime    Regime        `json:"regime"`
	RuleID    string        `json:"rule_id"`   // which decision-table row fired
	Structure StructureView `json:"structure"` // layers 1 and 2, with their metrics
	Flags     Flags         `json:"flags"`

	Evidence []Evidence  `json:"evidence"`
	Raw      RawSnapshot `json:"raw"`

	Reasons []string `json:"reasons,omitempty"`
	Caveats []string `json:"caveats,omitempty"`
}

// NewSnapshot returns a snapshot pre-filled with the fields every writer must set, in the
// UNKNOWN state. Starting from UNKNOWN rather than the zero value matters: the zero value
// of Regime is "", which would serialize as a regime nobody defined.
func NewSnapshot(date string, now time.Time) *Snapshot {
	return &Snapshot{
		Date:        date,
		GeneratedAt: now,
		SchemaVer:   SnapshotSchemaVersion,
		Regime:      RegimeUnknown,
		RuleID:      RuleUnknown,
		Structure: StructureView{
			Structure: StructureUnknown,
			Breadth:   BreadthUnknown,
			Posture:   PostureUnknown,
		},
	}
}

// ApplyDecision copies a layer-3 verdict into the snapshot. Kept as a method so the regime
// engine (M3) never has to know the snapshot's field names.
func (s *Snapshot) ApplyDecision(d RegimeDecision) {
	s.Regime = d.Regime
	s.RuleID = d.RuleID
	s.Structure = d.View
	s.Reasons = append(s.Reasons, d.Reasons...)
	s.Caveats = append(s.Caveats, d.Caveats...)
}

// UsableEvidence is the subset the score/confidence engines may consume.
func (s *Snapshot) UsableEvidence() []Evidence { return UsableEvidence(s.Evidence) }

// Validate catches the mistakes that would silently corrupt the historical record. It is
// called before every write: a malformed snapshot must never reach disk, because the whole
// point of the archive is that old files stay trustworthy.
func (s *Snapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("market: nil snapshot")
	}
	if s.Date == "" {
		return fmt.Errorf("market: snapshot has no date")
	}
	if _, err := time.Parse("2006-01-02", s.Date); err != nil {
		return fmt.Errorf("market: snapshot date %q is not YYYY-MM-DD", s.Date)
	}
	if s.SchemaVer <= 0 {
		return fmt.Errorf("market: snapshot %s has no schema version", s.Date)
	}
	if !s.Regime.Valid() {
		return fmt.Errorf("market: snapshot %s has undefined regime %q", s.Date, s.Regime)
	}
	if s.Score < 0 || s.Score > 100 {
		return fmt.Errorf("market: snapshot %s score %.2f out of range", s.Date, s.Score)
	}
	if s.Confidence < 0 || s.Confidence > 1 {
		return fmt.Errorf("market: snapshot %s confidence %.3f out of range", s.Date, s.Confidence)
	}
	if !s.Structure.Structure.Valid() || !s.Structure.Breadth.Valid() || !s.Structure.Posture.Valid() {
		return fmt.Errorf("market: snapshot %s has an undefined structural state", s.Date)
	}
	// A known regime must name the rule that produced it, otherwise the call is unauditable.
	if s.Regime != RegimeUnknown && s.RuleID == "" {
		return fmt.Errorf("market: snapshot %s has regime %s but no rule id", s.Date, s.Regime)
	}
	for i, e := range s.Evidence {
		if e.Source == "" {
			return fmt.Errorf("market: snapshot %s evidence[%d] has no source", s.Date, i)
		}
		if !e.Direction.Valid() || !e.Strength.Valid() {
			return fmt.Errorf("market: snapshot %s evidence[%d] (%s) has an undefined direction/strength",
				s.Date, i, e.Source)
		}
		if e.Confidence < 0 || e.Confidence > 1 {
			return fmt.Errorf("market: snapshot %s evidence[%d] (%s) confidence %.3f out of range",
				s.Date, i, e.Source, e.Confidence)
		}
		// Unavailable evidence must be UNKNOWN on both axes — this is the invariant that
		// stops missing data from being recorded as a neutral reading.
		if !e.Available && (e.Direction != DirUnknown || e.Strength != StrengthUnknown) {
			return fmt.Errorf("market: snapshot %s evidence[%d] (%s) is unavailable but not UNKNOWN",
				s.Date, i, e.Source)
		}
	}
	return nil
}
