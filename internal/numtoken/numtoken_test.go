package numtoken

import "testing"

// The normaliser cleans and stops. Every case below is a token that the two callers must be
// free to interpret DIFFERENTLY, which is only possible while nothing here decides for them.
func TestNormalizeDecidesNothing(t *testing.T) {
	for in, want := range map[string]string{
		"1,234":     "1234",
		"  42 ":     "42",
		"-1,856":    "-1856",
		"1,234,567": "1234567",
		// The two tokens the callers disagree about, both passed straight through.
		// internal/market/provider maps them to 0 (a TWSE cash row's empty cell really is
		// a zero); internal/derivatives/provider maps them to ABSENT (a derivatives "-"
		// means "not carried", and "0" means "a strike nobody holds").
		"":     "",
		"-":    "-",
		"NULL": "NULL",
		// A percentage is NOT stripped here: a bare '%' is meaningful only in the fields
		// declared to be percentages, and stripping it everywhere would let "50%" parse as
		// 50 on a lot-count column where it should stay visible as an unknown token.
		"9.98%": "9.98%",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizePercent(t *testing.T) {
	for in, want := range map[string]string{
		"9.98%":   "9.98",
		"0.00%":   "0.00",
		"-1.20%":  "-1.20",
		" 1,2%  ": "12",
		// Not a percentage field's problem: a token with no suffix is unchanged, so the
		// same function is safe on a column that only sometimes prints one.
		"98.45": "98.45",
		"-":     "-",
		"":      "",
	} {
		if got := NormalizePercent(in); got != want {
			t.Errorf("NormalizePercent(%q) = %q, want %q", in, got, want)
		}
	}
}
