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
		{"2126-01-10 12:00", time.Date(2126, 1, 10, 12, 0, 0, 0, time.Local), false},
		{"2126-01-10 12:00:10", time.Date(2126, 1, 10, 12, 0, 10, 0, time.Local), false},
		{"2126-01-10T12:00:00Z", time.Date(2126, 1, 10, 12, 0, 0, 0, time.UTC), false},
		{"1h", now.Add(time.Hour), true},
		{"2d", now.AddDate(0, 0, 2), true},
		{"1w", now.AddDate(0, 0, 7), true},
		{"3w", now.AddDate(0, 0, 21), true},
		{"2126-05-20", time.Date(2126, 5, 20, 0, 0, 0, 0, time.Local), false},
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

	t.Run("Date is in past", func(t *testing.T) {
		_, err := ParseExpiry("2020-01-01")
		assert.Error(t, err)
	})
}

// A negative retention duration is always a mistake: it puts the deadline in
// the past, so the resource is deleted on the very next janitor tick.
func TestParseDurationRejectsNegative(t *testing.T) {
	for _, input := range []string{"-30d", "-1h", "-15m", "-1s"} {
		if d, err := ParseDuration(input); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", input, d)
		}
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{input: "30d", want: 30 * 24 * time.Hour},
		{input: "7d", want: 7 * 24 * time.Hour},
		{input: "2h", want: 2 * time.Hour},
		{input: "30m", want: 30 * time.Minute},
		{input: "0d", want: 0},
		{input: "garbage", wantErr: true},
		{input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseDuration(%q) = %v, want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseDuration(%q): unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
