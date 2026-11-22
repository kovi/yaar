package utils

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseExpiry converts a string (absolute ISO date or relative duration) into a time.Time
func ParseExpiry(input string) (time.Time, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Time{}, nil
	}

	layouts := []string{
		time.RFC3339,          // 2026-01-02T15:04:05Z
		"2006-01-02 15:04:05", // 2026-01-02 15:04:05 (Space separator)
		"2006-01-02T15:04",    // 2026-01-02T15:04 (HTML datetime-local)
		"2006-01-02 15:04",    // 2026-01-02T15:04 (HTML datetime-local)
		"2006-01-02",          // 2026-01-02 (Date only)
	}

	for _, layout := range layouts {
		// If layout has no 'Z' or offset, use ParseInLocation
		var t time.Time
		var err error

		if strings.Contains(layout, "Z") || strings.Contains(layout, "-07") {
			t, err = time.Parse(layout, input)
		} else {
			t, err = time.ParseInLocation(layout, input, time.Local)
		}

		if err == nil {

			// ADD VALIDATION
			if !t.IsZero() && t.Before(time.Now().Add(-1*time.Minute)) {
				return time.Time{}, fmt.Errorf("expiry time is in the past")
			}

			return t, nil
		}
	}

	// Try Relative Durations
	// Handle custom 'd' suffix for days
	if daysStr, found := strings.CutSuffix(input, "d"); found {
		days, err := strconv.Atoi(daysStr)
		if err == nil {
			return time.Now().AddDate(0, 0, days), nil
		}
	}
	if weeksStr, found := strings.CutSuffix(input, "w"); found {
		days, err := strconv.Atoi(weeksStr)
		if err == nil {
			return time.Now().AddDate(0, 0, days*7), nil
		}
	}

	// Fallback to Go's standard duration parser (h, m, s)
	dur, err := time.ParseDuration(input)
	if err == nil {
		return time.Now().Add(dur), nil
	}

	return time.Time{}, fmt.Errorf("invalid expiry format: use duration (7d, 1h) or absolute time (ISO8601)")
}

// ParseDuration parses a retention duration: a whole number of days ("7d",
// "30d") or anything time.ParseDuration accepts ("2h", "90m", "0"). It is the
// single parser for every retention and grace-period setting, so they all
// accept the same syntax.
//
// Negative durations are rejected: they are always a mistake in a retention
// setting, and accepting one puts the deadline in the past, so the resource is
// deleted on the very next janitor tick.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: use e.g. \"7d\" or a Go duration like \"168h\"", s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, err
		}
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %q is negative: retention durations must be positive", s)
	}
	return d, nil
}
