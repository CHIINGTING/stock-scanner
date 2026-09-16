package entryplanbacktest

import (
	"fmt"
	"time"
)

// ── Execution arms ────────────────────────────────────────────────────────────────────────

// ExecutionArm is HOW an observation is assumed to have been executed. It is part of the
// observation's identity because the same plan under two arms is two different measurements,
// and averaging them would answer neither question.
type ExecutionArm string

const (
	// ArmNextOpen — fill at the OPEN of the earliest executable session (T+1). The
	// unconditional arm: it always fills, so its N equals the number of eligible plans and
	// it is the denominator every conditional arm is read against.
	//
	// It is the same entry rule internal/validator/price.go:44 already grades signals on
	// ("next trading day's open"), which is why it is the baseline here.
	ArmNextOpen ExecutionArm = "NEXT_OPEN"

	// ArmZoneLimit — fill only if the market trades into the plan's IdealEntry zone during
	// the wait window, from T+1 onward. This is the arm that actually tests EntryPlan: the
	// zone is the thing the layer computes, and NEXT_OPEN ignores it entirely.
	ArmZoneLimit ExecutionArm = "ZONE_LIMIT"

	// ArmChaseCeiling — ZONE_LIMIT plus the plan's MaxChasePrice as an upper bound on the
	// fill. Separate from ZONE_LIMIT because the ceiling is a separate rule with its own
	// multiplier, and a study that cannot switch it off cannot measure it.
	ArmChaseCeiling ExecutionArm = "CHASE_CEILING"
)

// AllExecutionArms is every arm this contract admits, in a fixed order.
//
// THERE IS NO SIGNAL_CLOSE ARM, and its absence is the contract rather than an omission.
// r6backtest.Params.EntryMode offers "signal_close" as a reference mode
// (internal/r6backtest/types.go:82); under EP-9's temporal contract that mode is a same-bar
// fill on T and is therefore unexpressible here. A study that wants it has to add a value to
// this list and break TestTheForbiddenSameBarArmIsNotExpressible, which is the point.
var AllExecutionArms = []ExecutionArm{ArmNextOpen, ArmZoneLimit, ArmChaseCeiling}

// Valid reports whether a is a defined arm. "" is not an arm.
func (a ExecutionArm) Valid() bool {
	for _, k := range AllExecutionArms {
		if a == k {
			return true
		}
	}
	return false
}

// ── Observation identity ──────────────────────────────────────────────────────────────────

// ObservationID is what makes two rows the SAME observation.
//
// Every field is part of the identity because changing any one of them changes the question
// the row answers:
//
//	Symbol        which stock
//	SignalAsOf    which completed session T the plan's information set ends at
//	RuleVersion   which EntryPlan rule set produced the plan (entryplan.RuleVersion). Two
//	              rule versions answer DIFFERENT QUESTIONS with the same field names — see
//	              internal/entryplan/types.go's RuleVersion doc, where EP4-v1's
//	              INSUFFICIENT_DATA means "no decision rule existed" and EP5-v1's means "the
//	              evidence did not support a call". Pooling them would average two different
//	              sentences.
//	Arm           which execution assumption
//	WaitSessions  how long a conditional arm was allowed to wait for its fill
//
// WaitSessions is on the identity even for ArmNextOpen, where it changes nothing about the
// fill. Making it conditional would mean the key's shape depends on the arm, and the first
// time somebody compared a NEXT_OPEN row against a ZONE_LIMIT row the two keys would collide
// or not depending on a rule nobody could see. A constant field that is sometimes inert is
// cheaper than a variable key.
type ObservationID struct {
	Symbol       string       `json:"symbol"`
	SignalAsOf   string       `json:"signal_as_of"`
	RuleVersion  string       `json:"rule_version"`
	Arm          ExecutionArm `json:"execution_arm"`
	WaitSessions int          `json:"wait_sessions"`
}

// Validate refuses an identity that could not be compared with another one.
func (o ObservationID) Validate() error {
	if o.Symbol == "" {
		return fmt.Errorf("entryplanbacktest: observation has no symbol")
	}
	if _, err := time.Parse(DateLayout, o.SignalAsOf); err != nil {
		return fmt.Errorf("entryplanbacktest: observation %s has signal date %q, not YYYY-MM-DD",
			o.Symbol, o.SignalAsOf)
	}
	if o.RuleVersion == "" {
		return fmt.Errorf("entryplanbacktest: observation %s/%s carries no RuleVersion — a "+
			"result with no rule stamp cannot be told apart from a result under a different rule",
			o.Symbol, o.SignalAsOf)
	}
	if !o.Arm.Valid() {
		return fmt.Errorf("entryplanbacktest: observation %s/%s has execution arm %q, which is "+
			"not one of %v", o.Symbol, o.SignalAsOf, o.Arm, AllExecutionArms)
	}
	if o.WaitSessions < 1 || o.WaitSessions > MaxWaitSessions {
		return fmt.Errorf("entryplanbacktest: observation %s/%s has wait window %d sessions, "+
			"want 1..%d — 0 would be a same-bar fill on T", o.Symbol, o.SignalAsOf,
			o.WaitSessions, MaxWaitSessions)
	}
	return nil
}

// Key is the identity as one comparable string. It is built from validated fields only, so
// Validate must be called first — Add does.
func (o ObservationID) Key() string {
	return fmt.Sprintf("%s|%s|%s|%s|%d", o.Symbol, o.SignalAsOf, o.RuleVersion, o.Arm, o.WaitSessions)
}

// ObservationSet is the deduplicating registry a Phase 2 run records into.
//
// It FAILS LOUDLY on a duplicate rather than overwriting or silently keeping the first. A
// duplicated observation is not a harmless repeat: it doubles that stock-session's weight in
// every mean the study reports, and the usual cause — the same symbol reached through two
// source tabs, or a re-run appended to an existing output — produces exactly the rows most
// likely to be unusual.
type ObservationSet struct {
	seen  map[string]struct{}
	order []ObservationID
}

// NewObservationSet returns an empty set.
func NewObservationSet() *ObservationSet {
	return &ObservationSet{seen: make(map[string]struct{})}
}

// Add validates o and records it, or returns an error naming the collision or the defect.
func (s *ObservationSet) Add(o ObservationID) error {
	if s.seen == nil {
		s.seen = make(map[string]struct{})
	}
	if err := o.Validate(); err != nil {
		return err
	}
	k := o.Key()
	if _, dup := s.seen[k]; dup {
		return fmt.Errorf("entryplanbacktest: duplicate observation %s — an observation "+
			"recorded twice doubles that stock-session's weight in every metric", k)
	}
	s.seen[k] = struct{}{}
	s.order = append(s.order, o)
	return nil
}

// Len is the number of accepted observations.
func (s *ObservationSet) Len() int { return len(s.order) }

// IDs returns the accepted identities in insertion order (a copy).
func (s *ObservationSet) IDs() []ObservationID {
	out := make([]ObservationID, len(s.order))
	copy(out, s.order)
	return out
}
