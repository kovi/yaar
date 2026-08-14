package api

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestExtractRetentionHeaders(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		want        *RetentionPolicyPatch
		wantErr     bool
		errContains string
	}{
		{
			name: "all headers valid",
			headers: map[string]string{
				"Yaar-Retention-Immutable":           "true",
				"Yaar-Retention-Auto-Prune":          "false",
				"Yaar-Retention-Expire-After-Upload": "30d",
				"Yaar-Retention-Expire-At":           "2027-04-12T00:00:00Z",
			},
			want: &RetentionPolicyPatch{
				Immutable: boolPtr(true),
				AutoPrune: boolPtr(false),
				Expires: &ExpiresPatch{
					AfterUpload: strPtr("30d"),
					At:          strPtr("2027-04-12T00:00:00Z"),
				},
			},
		},
		{
			name:    "no headers",
			headers: map[string]string{},
			want:    nil,
		},
		{
			name: "invalid boolean",
			headers: map[string]string{
				"Yaar-Retention-Immutable": "maybe",
			},
			wantErr:     true,
			errContains: "invalid boolean",
		},
		{
			name: "conflicting expiry",
			headers: map[string]string{
				"Yaar-Retention-Expire-After-Upload":   "30d",
				"Yaar-Retention-Expire-After-Download": "7d",
			},
			wantErr:     true,
			errContains: "cannot combine",
		},
		{
			name: "invalid duration",
			headers: map[string]string{
				"Yaar-Retention-Expire-After-Upload": "thirty days",
			},
			wantErr:     true,
			errContains: "invalid duration",
		},
		{
			name: "invalid timestamp",
			headers: map[string]string{
				"Yaar-Retention-Expire-At": "2027-04-12 10",
			},
			wantErr:     true,
			errContains: "unsupported time format",
		},
		{
			name: "case insensitive boolean",
			headers: map[string]string{
				"Yaar-Retention-Immutable":  "TRUE",
				"Yaar-Retention-Auto-Prune": "No",
			},
			want: &RetentionPolicyPatch{
				Immutable: boolPtr(true),
				AutoPrune: boolPtr(false),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("PATCH", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := ExtractRetentionHeaders(req)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing '%s', got nil", tt.errContains)
				} else if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %v, want error containing '%s'", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Helper functions
func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }
