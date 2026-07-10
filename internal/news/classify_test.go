package news

import "testing"

func TestClassifierSemantics(t *testing.T) {
	cases := []struct {
		name     string
		sent     string
		wantType SignalType // "" = expect no signal (ok=false)
		reject   []SignalType
	}{
		{
			name:     "strong sector list is bullish",
			sent:     "強勢族群：被動元件、功率元件",
			wantType: SignalBullish,
		},
		{
			name:     "correction alone is caution, not bullish/rotation-in",
			sent:     "被動元件進入修正",
			wantType: SignalRiskWarning,
			reject:   []SignalType{SignalBullish, SignalRotationIn},
		},
		{
			name:     "correction hedged by support is caution, not strong bearish nor rotation-in",
			sent:     "被動元件進入修正，但仍在季線之上",
			wantType: SignalRiskWarning,
			reject:   []SignalType{SignalRotationIn, SignalBearish, SignalBullish},
		},
		{
			name:     "breakout-failure is not bullish",
			sent:     "突破失敗",
			wantType: SignalBearish,
			reject:   []SignalType{SignalBullish},
		},
		{
			name:   "negated weakness is not bearish",
			sent:   "沒有轉弱",
			reject: []SignalType{SignalBearish},
		},
		{
			name:   "not-yet-broken-out is not bullish",
			sent:   "尚未突破",
			reject: []SignalType{SignalBullish},
		},
		{
			name:     "broke-then-reclaimed is caution",
			sent:     "跌破後站回",
			wantType: SignalRiskWarning,
			reject:   []SignalType{SignalBullish, SignalRotationIn},
		},
	}
	for _, c := range cases {
		got, _, ok := classifySentence(c.sent)
		if c.wantType != "" {
			if !ok || got != c.wantType {
				t.Errorf("%s: classify(%q) = (%q, ok=%v) want %q", c.name, c.sent, got, ok, c.wantType)
			}
		}
		for _, bad := range c.reject {
			if ok && got == bad {
				t.Errorf("%s: classify(%q) must NOT be %q (got it)", c.name, c.sent, bad)
			}
		}
	}
}

// The EP677-style line that regressed: it must not read ROTATION_IN.
func TestEP677PassiveNotRotationIn(t *testing.T) {
	got, _, ok := classifySentence("被動元件進入修正，但仍在季線之上")
	if ok && got == SignalRotationIn {
		t.Fatalf("被動元件 correction line must never classify as ROTATION_IN (got %q)", got)
	}
}
