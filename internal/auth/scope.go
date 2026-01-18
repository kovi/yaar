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

func IsInScopes(path string, scopes []string) bool {
	if len(scopes) == 0 || (len(scopes) == 1 && scopes[0] == "/") {
		return true
	}

	for _, scope := range scopes {
		if IsInScope(path, scope) {
			return true
		}
	}

	return false
}
