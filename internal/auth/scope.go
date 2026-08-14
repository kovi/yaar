package auth

import (
	"path/filepath"
	"strings"
)

func IsInScope(path, scope string) bool {
	if scope == "" || scope == "/" {
		return true
	}
	// Simple directory prefix check
	cleanPath := filepath.Clean("/" + path)
	cleanScope := filepath.Clean("/" + scope)

	return cleanPath == cleanScope || strings.HasPrefix(cleanPath, cleanScope+"/")
}

// IsInScopes reports whether path falls inside any of the given scopes.
//
// An empty scope list means "no access", not "all access": absence of a granted
// scope must never read as full permission, or every caller that reuses this
// helper for enforcement inherits a fail-open default. Callers that legitimately
// have unrestricted reach (admins, internal jobs) pass []string{"/"} explicitly.
func IsInScopes(path string, scopes []string) bool {
	for _, scope := range scopes {
		if IsInScope(path, scope) {
			return true
		}
	}

	return false
}
