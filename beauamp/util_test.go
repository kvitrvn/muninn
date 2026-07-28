package beauamp

import "testing"

func TestFirstNumber(t *testing.T) {
	rec := map[string]any{"a": "not-a-number", "b": float64(42), "c": "100"}

	// A present-but-unparseable string value is skipped in favor of the next
	// key, same as if it were absent.
	if got := firstNumber(rec, "a", "b"); got != 42 {
		t.Errorf("firstNumber(a, b) = %v, want 42 (unparseable \"a\" falls through to \"b\")", got)
	}
	// A missing first key falls through to the next present one.
	if got := firstNumber(rec, "missing", "b"); got != 42 {
		t.Errorf("firstNumber(missing, b) = %v, want 42", got)
	}
	// Numeric strings parse fine.
	if got := firstNumber(rec, "c"); got != 100 {
		t.Errorf("firstNumber(c) = %v, want 100", got)
	}
	// No key present, or the only present key is unparseable: both return 0,
	// indistinguishable from each other or from a genuine zero amount.
	if got := firstNumber(rec, "missing"); got != 0 {
		t.Errorf("firstNumber(missing) = %v, want 0", got)
	}
	if got := firstNumber(rec, "a"); got != 0 {
		t.Errorf("firstNumber(a) = %v, want 0 (unparseable, no fallback key left)", got)
	}
}
