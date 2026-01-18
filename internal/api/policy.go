package api

import (
	"path/filepath"

	"github.com/kovi/yaar/internal/auth"
	"github.com/sirupsen/logrus"
)

type ModifyOptions struct {
	IgnoreProtected bool // Used for uploads (allow new files in protected dirs)
	IsUpload        bool // Specifically for checking if file exists for overwrite
}

// CanModify checks if a path is eligible for changes based on config and DB policy.
func (h *Handler) CanModify(urlPath string, allowedScopes []string, opts ModifyOptions) (bool, string) {
	urlPath = filepath.Clean("/" + urlPath)

	// 1. SCOPE CHECK: Is the path within one of the allowed scopes?
	inScope := false
	if len(allowedScopes) == 0 || (len(allowedScopes) == 1 && allowedScopes[0] == "/") {
		inScope = true
	} else {
		for _, scope := range allowedScopes {
			if auth.IsInScope(urlPath, scope) {
				inScope = true
				break
			}
		}
	}
	if !inScope {
		return false, "Path is outside of your authorized scope."
	}

	// 2. PARENT WALK: Check if any parent (or the file itself) is Protected or Immutable
	// We collect all parent paths: /a/b/c.txt -> ["/a/b/c.txt", "/a/b", "/a", "/"]
	parents := []string{urlPath}
	curr := urlPath
	for curr != "/" {
		curr = filepath.Dir(curr)
		parents = append(parents, curr)
	}

	// Fetch all metadata for this path and its parents in ONE query
	var metas []MetaResource
	h.DB.Where("path IN ?", parents).Find(&metas)

	metaMap := make(map[string]MetaResource)
	for _, m := range metas {
		metaMap[m.Path] = m
	}

	// Iterate through parents from top to bottom (root down to file)
	for i := len(parents) - 1; i >= 0; i-- {
		p := parents[i]

		// A. Check Configuration Protection (YAML)
		// Rule: If a directory is protected, we allow new uploads but block delete/overwrite.
		logrus.Infof("ignore:%v p:%v isprotected:%v", opts.IgnoreProtected, p, h.Config.IsProtected(p))
		if !opts.IgnoreProtected && h.Config.IsProtected(p) {
			return false, "Action prohibited: " + p + " is a protected directory."
		}

		// B. Check Database Immutability
		// Rule: If any parent (or the file) is Immutable, NO changes are allowed.
		if m, exists := metaMap[p]; exists {
			if m.Immutable != nil && *m.Immutable {
				return false, "Action prohibited: " + p + " is immutable (locked)."
			}
		}
	}

	return true, ""
}
