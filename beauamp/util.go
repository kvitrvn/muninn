package beauamp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// str returns the textual representation used by JSON and CSV resource rows.
func str(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
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

// nativeStringList accepts a scalar, a JSON-like array returned as a string,
// or a decoded JSON array. BEAUAMP uses stringified Python lists for some
// group-award supplier SIRENs.
func nativeStringList(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			raw = append(raw, str(item))
		}
	case []string:
		raw = append(raw, typed...)
	case string:
		value := strings.TrimSpace(typed)
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
			for _, item := range strings.Split(value, ",") {
				raw = append(raw, strings.Trim(strings.TrimSpace(item), `"'`))
			}
		} else {
			raw = append(raw, value)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" && !strings.EqualFold(item, "none") && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

// firstNumber returns the first present numeric value among the given keys,
// tolerating JSON numbers and numeric strings; a string that fails to parse is
// skipped in favor of the next key, same as a missing one. Returns 0 when no
// key yields a value — indistinguishable from every key genuinely being
// absent or zero. The raw value remains available through Tender.Sources.
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
