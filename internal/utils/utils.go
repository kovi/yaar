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

func ParseStream(value string) (stream, group string, err error) {
	if value == "" {
		return "", "", nil
	}

	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("stream must be in format 'stream/group'")
	}

	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}
