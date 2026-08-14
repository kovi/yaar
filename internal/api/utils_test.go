package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestParseTimeString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string // Expected time in RFC3339 for comparison
		wantErr bool
	}{
		// RFC3339 formats
		{
			name:  "RFC3339 with Z",
			input: "2027-04-12T15:04:05Z",
			want:  "2027-04-12T15:04:05Z",
		},
		{
			name:  "RFC3339 with timezone offset",
			input: "2027-04-12T15:04:05+02:00",
			want:  "2027-04-12T13:04:05Z", // Normalized to UTC
		},
		{
			name:  "RFC3339 with nanoseconds",
			input: "2027-04-12T15:04:05.123456789Z",
			want:  "2027-04-12T15:04:05.123456789Z",
		},

		// ISO8601 without seconds
		{
			name:  "ISO8601 without seconds UTC",
			input: "2027-04-12T15:04Z",
			want:  "2027-04-12T15:04:00Z",
		},
		{
			name:  "ISO8601 without seconds no TZ",
			input: "2027-04-12T15:04",
			want:  "2027-04-12T15:04:00Z",
		},

		// Space-separated formats
		{
			name:  "space-separated with seconds",
			input: "2027-04-12 15:04:05",
			want:  "2027-04-12T15:04:05Z",
		},
		{
			name:  "space-separated without seconds",
			input: "2027-04-12 15:04",
			want:  "2027-04-12T15:04:00Z",
		},
		{
			name:  "space-separated with timezone",
			input: "2027-04-12 15:04:05 +0200",
			want:  "2027-04-12T13:04:05Z",
		},

		// Date only
		{
			name:  "date only",
			input: "2027-04-12",
			want:  "2027-04-12T00:00:00Z",
		},

		// Edge cases
		{
			name:  "whitespace trimmed",
			input: "  2027-04-12T15:04:05Z  ",
			want:  "2027-04-12T15:04:05Z",
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "April 12, 2027",
			wantErr: true,
		},
		{
			name:    "invalid date",
			input:   "2027-13-45",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTimeString(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			want, _ := time.Parse(time.RFC3339Nano, tt.want)
			if !got.Equal(want) {
				t.Errorf("got %v, want %v", got.Format(time.RFC3339Nano), tt.want)
			}
		})
	}
}

func TestParseTimeStringInLocation(t *testing.T) {
	budapest, _ := time.LoadLocation("Europe/Budapest")

	tests := []struct {
		name     string
		input    string
		location *time.Location
		want     string
		wantErr  bool
	}{
		{
			name:     "no timezone, use UTC",
			input:    "2027-04-12 15:04:05",
			location: time.UTC,
			want:     "2027-04-12T15:04:05Z",
		},
		{
			name:     "no timezone, use Budapest",
			input:    "2027-04-12 15:04:05",
			location: budapest,
			want:     "2027-04-12T13:04:05Z", // Budapest is UTC+2 in April
		},
		{
			name:     "with timezone, ignore location",
			input:    "2027-04-12T15:04:05Z",
			location: budapest,
			want:     "2027-04-12T15:04:05Z", // Explicit UTC overrides location
		},
		{
			name:     "date only in Budapest",
			input:    "2027-04-12",
			location: budapest,
			want:     "2027-04-11T22:00:00Z", // Midnight in Budapest = 22:00 UTC previous day
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTimeStringInLocation(tt.input, tt.location)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			want, _ := time.Parse(time.RFC3339, tt.want)
			if !got.Equal(want) {
				t.Errorf("got %v, want %v", got.Format(time.RFC3339), tt.want)
			}
		})
	}
}

// A negative retention duration is always a mistake: it puts the deadline in
// the past, so the resource is deleted on the very next janitor tick.
func TestParseDurationRejectsNegative(t *testing.T) {
	for _, input := range []string{"-30d", "-1h", "-15m", "-1s"} {
		if d, err := parseDuration(input); err == nil {
			t.Errorf("parseDuration(%q) = %v, want an error", input, d)
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
			got, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDuration(%q) = %v, want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDuration(%q): unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseListPagination(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
		wantErr    bool
	}{
		{name: "defaults", query: "", wantLimit: maxListLimit},
		{name: "explicit values", query: "limit=10&offset=20", wantLimit: 10, wantOffset: 20},
		{name: "limit is capped", query: "limit=999999", wantLimit: maxListLimit},
		{name: "zero limit means default", query: "limit=0", wantLimit: maxListLimit},
		{name: "negative limit rejected", query: "limit=-1", wantErr: true},
		{name: "negative offset rejected", query: "offset=-1", wantErr: true},
		{name: "malformed limit rejected", query: "limit=abc", wantErr: true},
		{name: "malformed offset rejected", query: "offset=abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/?"+tt.query, nil)

			limit, offset, err := parseListPagination(c)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseListPagination(%q) = (%d, %d, nil), want an error", tt.query, limit, offset)
				}
				return
			}
			if err != nil {
				t.Errorf("parseListPagination(%q): unexpected error: %v", tt.query, err)
				return
			}
			if limit != tt.wantLimit || offset != tt.wantOffset {
				t.Errorf("parseListPagination(%q) = (%d, %d), want (%d, %d)",
					tt.query, limit, offset, tt.wantLimit, tt.wantOffset)
			}
		})
	}
}
