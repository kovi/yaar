package api

import "testing"

// CleanupFailureTestAccess allows test suites to manipulate failed cleanup registry entries.
func (h *Handler) CleanupFailureTestAccess(t *testing.T, key string, fn func(*CleanupFailure)) {
	h.cleanupMutex.Lock()
	defer h.cleanupMutex.Unlock()

	if h.failedCleanups != nil {
		if f, exists := h.failedCleanups[key]; exists {
			fn(f)
		}
	}
}
