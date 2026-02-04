package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseExpiry(t *testing.T) {
	now := time.Now()

	tests := []struct {
		input    string
		expected time.Time
		flexible bool // if true, use WithinDuration
	}{
		{"", time.Time{}, false},
		{"2026-01-10 12:00", time.Date(2026, 1, 10, 12, 0, 0, 0, time.Local), false},
		{"2026-01-10 12:00:10", time.Date(2026, 1, 10, 12, 0, 10, 0, time.Local), false},
		{"2026-01-10T12:00:00Z", time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC), false},
		{"1h", now.Add(time.Hour), true},
		{"2d", now.AddDate(0, 0, 2), true},
		{"1w", now.AddDate(0, 0, 7), true},
		{"3w", now.AddDate(0, 0, 21), true},
		{"2026-05-20", time.Date(2026, 5, 20, 0, 0, 0, 0, time.Local), false},
	}

	for _, tt := range tests {
		actual, err := ParseExpiry(tt.input)
		assert.NoError(t, err)
		if tt.flexible {
			assert.WithinDuration(t, tt.expected, actual, 1*time.Second)
		} else {
			assert.Equal(t, tt.expected, actual)
		}
	}

	t.Run("Invalid input", func(t *testing.T) {
		_, err := ParseExpiry("tomorrow")
		assert.Error(t, err)
	})
}
