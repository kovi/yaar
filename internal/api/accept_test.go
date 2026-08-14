package api

import "testing"

func TestGetScore(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   float64
	}{
		// Real-world clients
		{
			name:   "chrome",
			header: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			want:   1.0,
		},
		{
			name:   "curl default is not a request for HTML",
			header: "*/*",
			want:   0.0,
		},
		{
			name:   "weighted catch-all is not a request for HTML",
			header: "*/*;q=0.8",
			want:   0.0,
		},
		{
			name:   "api client",
			header: "application/json",
			want:   0.0,
		},
		{
			name:   "no header",
			header: "",
			want:   0.0,
		},

		// Media types are case-insensitive (RFC 9110)
		{name: "uppercase subtype", header: "text/HTML", want: 1.0},
		{name: "uppercase type and subtype", header: "TEXT/HTML", want: 1.0},

		// Optional whitespace is allowed around ";" and "="
		{name: "space before semicolon", header: "text/html ; q=0.9", want: 0.9},
		{name: "space around equals", header: "text/html; q = 0.9", want: 0.9},
		{name: "uppercase q parameter", header: "text/html;Q=0.5", want: 0.5},

		// Wildcard subtypes match; q=0 means "not acceptable"
		{name: "subtype wildcard", header: "text/*", want: 1.0},
		{name: "weighted subtype wildcard", header: "text/*;q=0.3", want: 0.3},
		{name: "explicit refusal", header: "text/html;q=0", want: 0.0},

		// The strongest matching entry wins, whatever the order
		{name: "exact match after wildcard", header: "text/*;q=0.2,text/html", want: 1.0},
		{name: "exact match before wildcard", header: "text/html,text/*;q=0.2", want: 1.0},
		{name: "weighted among alternatives", header: "text/plain,text/html;q=0.4", want: 0.4},

		// A malformed q must not silently read as "not acceptable"
		{name: "malformed q keeps default weight", header: "text/html;q=bogus", want: 1.0},

		// Substrings of the target must not match
		{name: "longer subtype", header: "text/htmlx", want: 0.0},
		{name: "longer type", header: "xtext/html", want: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getScore(tt.header, "text/html"); got != tt.want {
				t.Errorf("getScore(%q, \"text/html\") = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}
