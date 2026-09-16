package entryplanbacktest

import (
	"strings"
	"testing"

	"github.com/deep-huang/stock-scanner/internal/entryplan"
)

func validID() ObservationID {
	return ObservationID{
		Symbol: "2330", SignalAsOf: "2026-09-11", RuleVersion: entryplan.RuleVersion,
		Arm: ArmZoneLimit, WaitSessions: 5,
	}
}

func TestADuplicateObservationFailsLoudly(t *testing.T) {
	s := NewObservationSet()
	if err := s.Add(validID()); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := s.Add(validID())
	if err == nil {
		t.Fatal("the same observation was accepted twice — it would carry double weight in " +
			"every metric the study reports")
	}
	if !strings.Contains(err.Error(), "duplicate observation") {
		t.Fatalf("error %q does not name the collision", err)
	}
	if s.Len() != 1 {
		t.Fatalf("set holds %d observations after a refused duplicate, want 1", s.Len())
	}
}

// TestEveryIdentityFieldSeparatesObservations is the positive half: change any ONE field and
// the two rows must coexist. Without it, Key() could return a constant and the duplicate test
// above would still pass.
func TestEveryIdentityFieldSeparatesObservations(t *testing.T) {
	base := validID()
	variants := []struct {
		field string
		id    ObservationID
	}{
		{"Symbol", func() ObservationID { v := base; v.Symbol = "2317"; return v }()},
		{"SignalAsOf", func() ObservationID { v := base; v.SignalAsOf = "2026-09-14"; return v }()},
		{"RuleVersion", func() ObservationID { v := base; v.RuleVersion = "EP5-v1"; return v }()},
		{"Arm", func() ObservationID { v := base; v.Arm = ArmNextOpen; return v }()},
		{"WaitSessions", func() ObservationID { v := base; v.WaitSessions = 3; return v }()},
	}
	for _, v := range variants {
		t.Run(v.field, func(t *testing.T) {
			s := NewObservationSet()
			if err := s.Add(base); err != nil {
				t.Fatalf("base: %v", err)
			}
			if err := s.Add(v.id); err != nil {
				t.Fatalf("changing %s was treated as the same observation: %v", v.field, err)
			}
			if s.Len() != 2 {
				t.Fatalf("set holds %d, want 2", s.Len())
			}
			if base.Key() == v.id.Key() {
				t.Fatalf("%s is not part of the identity key %q", v.field, base.Key())
			}
		})
	}
}

func TestAMalformedIdentityIsRefusedBeforeItIsRecorded(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ObservationID)
		want string
	}{
		{"no symbol", func(o *ObservationID) { o.Symbol = "" }, "no symbol"},
		{"bad date", func(o *ObservationID) { o.SignalAsOf = "11/09/2026" }, "not YYYY-MM-DD"},
		{"no rule version", func(o *ObservationID) { o.RuleVersion = "" }, "carries no RuleVersion"},
		{"no arm", func(o *ObservationID) { o.Arm = "" }, "execution arm"},
		{"unknown arm", func(o *ObservationID) { o.Arm = "SIGNAL_CLOSE" }, "execution arm"},
		{"zero wait", func(o *ObservationID) { o.WaitSessions = 0 }, "same-bar fill"},
		{"negative wait", func(o *ObservationID) { o.WaitSessions = -1 }, "same-bar fill"},
		{"wait past the horizon", func(o *ObservationID) { o.WaitSessions = MaxWaitSessions + 1 }, "wait window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := validID()
			c.mut(&id)
			if err := id.Validate(); err == nil {
				t.Fatalf("accepted %+v", id)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
			s := NewObservationSet()
			if err := s.Add(id); err == nil {
				t.Fatal("a malformed identity was recorded")
			}
			if s.Len() != 0 {
				t.Fatalf("set holds %d after a refused Add, want 0", s.Len())
			}
		})
	}
}

// TestTheForbiddenSameBarArmIsNotExpressible pins the absence of a signal-close arm.
// r6backtest.Params.EntryMode offers "signal_close"; under this contract it is a same-bar
// fill on T and must not be reachable through ExecutionArm.
func TestTheForbiddenSameBarArmIsNotExpressible(t *testing.T) {
	for _, forbidden := range []ExecutionArm{"SIGNAL_CLOSE", "signal_close", "SAME_BAR", ""} {
		if forbidden.Valid() {
			t.Fatalf("%q is a valid execution arm — a same-bar fill on T has become expressible",
				forbidden)
		}
	}
	for _, a := range AllExecutionArms {
		if !a.Valid() || a == "" {
			t.Fatalf("%q is in AllExecutionArms but is not Valid", a)
		}
	}
	if len(AllExecutionArms) != 3 {
		t.Fatalf("AllExecutionArms holds %d arms; adding one is a contract change that must be "+
			"argued here, not absorbed", len(AllExecutionArms))
	}
	// The wire values, pinned: they are what a Phase 2 CSV column contains.
	want := []ExecutionArm{"NEXT_OPEN", "ZONE_LIMIT", "CHASE_CEILING"}
	for i, w := range want {
		if AllExecutionArms[i] != w {
			t.Fatalf("arm %d serializes as %q, want %q", i, AllExecutionArms[i], w)
		}
	}
}

func TestTheWaitWindowCannotExceedTheOutcomeHorizon(t *testing.T) {
	if MaxWaitSessions != ExcursionSessions {
		t.Fatalf("MaxWaitSessions is %d and the outcome window is %d sessions — a plan allowed "+
			"to fill after the window it is graded over has closed cannot be graded",
			MaxWaitSessions, ExcursionSessions)
	}
}

func TestTheAcceptedIdentitiesAreReturnedAsACopy(t *testing.T) {
	s := NewObservationSet()
	if err := s.Add(validID()); err != nil {
		t.Fatalf("Add: %v", err)
	}
	ids := s.IDs()
	ids[0].Symbol = "MUTATED"
	if got := s.IDs()[0].Symbol; got != "2330" {
		t.Fatalf("the set's own record became %q after a caller mutated the returned slice", got)
	}
}

// TestTheRuleVersionOnTheIdentityIsTheProductionOne guards against the study inventing its own
// stamp: the value recorded must be entryplan's, so a rule bump changes every future row.
func TestTheRuleVersionOnTheIdentityIsTheProductionOne(t *testing.T) {
	if entryplan.RuleVersion == "" {
		t.Fatal("entryplan.RuleVersion is empty")
	}
	id := validID()
	if id.RuleVersion != entryplan.RuleVersion {
		t.Fatalf("fixture rule version %q is not entryplan.RuleVersion %q",
			id.RuleVersion, entryplan.RuleVersion)
	}
	if !strings.Contains(id.Key(), entryplan.RuleVersion) {
		t.Fatalf("identity key %q does not carry the rule version", id.Key())
	}
}
