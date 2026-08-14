package auth

import (
	"sync"
	"time"
)

// lastUsedResolution is how coarse Token.LastUsedAt is allowed to be. The field
// exists to spot dormant tokens, so minute granularity is ample.
const lastUsedResolution = time.Minute

// tokenUseTracker remembers, per token, when we last persisted last_used_at.
//
// The DB value alone is not enough to throttle on: the middleware loads the
// token row before the write, so under a burst several concurrent requests can
// all read the same stale timestamp and each decide to write. The in-process
// map collapses those to one write per token per window.
var tokenUseTracker = struct {
	sync.Mutex
	lastWrite map[uint]time.Time
}{lastWrite: make(map[uint]time.Time)}

// shouldRecordTokenUse reports whether last_used_at is stale enough to be worth
// a write. persisted is the value currently on the token row, and may be nil
// for a token that has never been used.
func shouldRecordTokenUse(tokenID uint, persisted *time.Time) bool {
	now := time.Now()

	tokenUseTracker.Lock()
	defer tokenUseTracker.Unlock()

	if seen, ok := tokenUseTracker.lastWrite[tokenID]; ok && now.Sub(seen) < lastUsedResolution {
		return false
	}

	// Not written by this process recently. Fall back to the stored value so a
	// restart does not force a write on the first request for every token.
	if persisted != nil && now.Sub(*persisted) < lastUsedResolution {
		return false
	}

	tokenUseTracker.lastWrite[tokenID] = now
	return true
}
