package auth

import "testing"

// IsInScopes used to return true for an empty scope list, so absence of any
// granted scope read as full permission. That made the helper fail open for any
// caller that used it for enforcement.
func TestIsInScopesEmptyListDeniesEverything(t *testing.T) {
	for _, path := range []string{"/", "/secret/creds.txt", "/builds/ci/app.bin"} {
		if IsInScopes(path, nil) {
			t.Errorf("IsInScopes(%q, nil) = true, want false: no scopes must mean no access", path)
		}
		if IsInScopes(path, []string{}) {
			t.Errorf("IsInScopes(%q, []) = true, want false: no scopes must mean no access", path)
		}
	}
}

func TestIsInScopes(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		scopes []string
		want   bool
	}{
		{"root scope grants everything", "/secret/creds.txt", []string{"/"}, true},
		{"exact scope match", "/builds/ci", []string{"/builds/ci"}, true},
		{"path under scope", "/builds/ci/app.bin", []string{"/builds/ci"}, true},
		{"path outside scope", "/secret/creds.txt", []string{"/builds/ci"}, false},
		// The classic prefix-confusion case: "/builds/ci-old" must not match
		// the "/builds/ci" scope just because the string starts with it.
		{"sibling with shared prefix", "/builds/ci-old/app.bin", []string{"/builds/ci"}, false},
		{"parent of scope", "/builds", []string{"/builds/ci"}, false},
		{"any of several scopes", "/docs/readme.md", []string{"/builds/ci", "/docs"}, true},
		{"none of several scopes", "/secret/x", []string{"/builds/ci", "/docs"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInScopes(tt.path, tt.scopes); got != tt.want {
				t.Errorf("IsInScopes(%q, %v) = %v, want %v", tt.path, tt.scopes, got, tt.want)
			}
		})
	}
}
