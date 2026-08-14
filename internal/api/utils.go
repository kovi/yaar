package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BindJSONStrict is a strict alternative to gin's ShouldBindJSON.
// It uses DisallowUnknownFields to reject JSON containing keys that don't exist in the target struct.
func BindJSONStrict(c *gin.Context, obj any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(obj)
}

// maxListLimit caps how many directory entries a single listing may return, so
// a client cannot ask the server to materialize an arbitrarily large directory.
const maxListLimit = 1000

// parseListPagination reads the `limit` and `offset` query parameters for
// directory listings. Both are optional; limit defaults to (and is capped at)
// maxListLimit. Malformed or negative values are rejected rather than silently
// coerced, so a client typo surfaces instead of quietly returning a wrong page.
func parseListPagination(c *gin.Context) (limit, offset int, err error) {
	limit = maxListLimit

	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return 0, 0, fmt.Errorf("invalid limit %q: expected a non-negative integer", raw)
		}
		if limit == 0 || limit > maxListLimit {
			limit = maxListLimit
		}
	}

	if raw := c.Query("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("invalid offset %q: expected a non-negative integer", raw)
		}
	}

	return limit, offset, nil
}

// parseDuration parses a retention duration.
//
//	"30d" → 30 * 24 * time.Hour
//	"7d"  → 7 * 24 * time.Hour
//	"2h"  → 2 * time.Hour
//
// Negative durations are rejected: they are always a mistake in a retention
// setting, and accepting one puts the deadline in the past, so the resource is
// deleted on the very next janitor tick.
func parseDuration(s string) (time.Duration, error) {
	var d time.Duration

	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		d, err = time.ParseDuration(s) // Handles "2h", "30m", etc.
		if err != nil {
			return 0, err
		}
	}

	if d < 0 {
		return 0, fmt.Errorf("duration %q is negative: retention durations must be positive", s)
	}

	return d, nil
}

// parseTimeString parses various time formats into time.Time.
// Supported formats:
//   - RFC3339: "2006-01-02T15:04:05Z07:00"
//   - ISO8601: "2006-01-02T15:04:05Z", "2006-01-02T15:04Z"
//   - Space-separated: "2006-01-02 15:04:05", "2006-01-02 15:04"
//   - Date only: "2006-01-02" (assumes 00:00:00 UTC)
func parseTimeString(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	// List of supported formats (order matters - most specific first)
	formats := []string{
		// SQLite / GORM default string formats with timezone offsets
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",

		// RFC3339 with timezone
		time.RFC3339,     // "2006-01-02T15:04:05Z07:00"
		time.RFC3339Nano, // "2006-01-02T15:04:05.999999999Z07:00"

		// ISO8601 variants
		"2006-01-02T15:04:05Z",        // UTC with seconds
		"2006-01-02T15:04:05",         // No timezone
		"2006-01-02T15:04Z",           // UTC without seconds
		"2006-01-02T15:04",            // No timezone, no seconds
		"2006-01-02T15:04:05Z0700",    // Compact timezone
		"2006-01-02T15:04:05.999Z",    // Milliseconds
		"2006-01-02T15:04:05.999999Z", // Microseconds

		// Space-separated formats
		"2006-01-02 15:04:05",       // With seconds
		"2006-01-02 15:04",          // Without seconds
		"2006-01-02 15:04:05 MST",   // With timezone name
		"2006-01-02 15:04:05 -0700", // With timezone offset

		// Date only (assumes 00:00:00 UTC)
		"2006-01-02",
	}

	var lastErr error
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}

	return time.Time{}, fmt.Errorf("unsupported time format '%s' (expected RFC3339, ISO8601, or 'YYYY-MM-DD HH:MM[:SS]'): %w", s, lastErr)
}

// parseTimeStringInLocation parses time string with a default location for formats without timezone.
// Useful when you want to interpret ambiguous timestamps in a specific timezone.
func parseTimeStringInLocation(s string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	// Formats WITH timezone info (parse directly)
	formatsWithTZ := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04Z",
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05.999Z",
		"2006-01-02T15:04:05.999999Z",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 -0700",
	}

	for _, layout := range formatsWithTZ {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	// Formats WITHOUT timezone (use provided location)
	formatsWithoutTZ := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}

	for _, layout := range formatsWithoutTZ {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported time format '%s'", s)
}
