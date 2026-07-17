package beauamp

import (
	"fmt"
	"time"
)

// str returns v as a string when it is one, else "".
func str(v any) string {
	s, _ := v.(string)
	return s
}

// firstString returns the first non-empty string value among the given keys.
func firstString(rec map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(rec[k]); s != "" {
			return s
		}
	}
	return ""
}

// firstNumber returns the first present numeric value among the given keys,
// tolerating JSON numbers and numeric strings. Returns 0 when none is set.
func firstNumber(rec map[string]any, keys ...string) float64 {
	for _, k := range keys {
		switch n := rec[k].(type) {
		case float64:
			return n
		case string:
			var f float64
			if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
				return f
			}
		}
	}
	return 0
}

// parseDate parses the ISO date used by BEAUAMP (e.g. "2026-06-03").
func parseDate(v string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, v)
}
