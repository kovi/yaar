package auth

import (
	"testing"
	"time"
)

func TestLoginThrottleLocksOutAfterAllowance(t *testing.T) {
	tr := newLoginThrottle()
	key := "admin\x0010.0.0.1"

	// The allowance is spent without any lockout.
	for i := range maxFailedLogins - 1 {
		if lockout := tr.recordFailure(key); lockout != 0 {
			t.Fatalf("failure %d locked out early (%v)", i+1, lockout)
		}
		if wait := tr.retryAfter(key); wait != 0 {
			t.Fatalf("failure %d should not block the next attempt (%v)", i+1, wait)
		}
	}

	// The next failure trips the lockout.
	lockout := tr.recordFailure(key)
	if lockout != baseLockout {
		t.Fatalf("expected first lockout of %v, got %v", baseLockout, lockout)
	}
	if wait := tr.retryAfter(key); wait <= 0 {
		t.Fatalf("expected the key to be locked out, got %v", wait)
	}
}

func TestLoginThrottleLockoutGrowsAndIsCapped(t *testing.T) {
	tr := newLoginThrottle()
	key := "admin\x0010.0.0.1"

	for range maxFailedLogins - 1 {
		tr.recordFailure(key)
	}

	// 1m, 2m, 4m, ... doubling per additional failure, clamped at maxLockout.
	// With baseLockout=1m and maxLockout=1h the raw sequence reaches 32m, then
	// 64m — which exceeds the cap and clamps to 1h from that point on.
	for step := range 8 {
		want := baseLockout << step
		if want > maxLockout {
			want = maxLockout
		}
		if got := tr.recordFailure(key); got != want {
			t.Fatalf("step %d: expected lockout %v, got %v", step, want, got)
		}
	}

	// Far beyond the cap it must stay at maxLockout, never overflow negative.
	for range 40 {
		if got := tr.recordFailure(key); got != maxLockout {
			t.Fatalf("expected the lockout capped at %v, got %v", maxLockout, got)
		}
	}
}

func TestLoginThrottleUnlocksAfterTheLockoutExpires(t *testing.T) {
	tr := newLoginThrottle()
	key := "admin\x0010.0.0.1"

	now := time.Now()
	tr.now = func() time.Time { return now }

	for range maxFailedLogins {
		tr.recordFailure(key)
	}
	if wait := tr.retryAfter(key); wait <= 0 {
		t.Fatal("expected a lockout")
	}

	// Just before expiry it is still locked.
	now = now.Add(baseLockout - time.Second)
	if wait := tr.retryAfter(key); wait <= 0 {
		t.Fatal("lockout ended early")
	}

	// After expiry the attempt may proceed.
	now = now.Add(2 * time.Second)
	if wait := tr.retryAfter(key); wait != 0 {
		t.Fatalf("expected the lockout to have expired, still waiting %v", wait)
	}
}

func TestLoginThrottleForgetsOldFailures(t *testing.T) {
	tr := newLoginThrottle()
	key := "admin\x0010.0.0.1"

	now := time.Now()
	tr.now = func() time.Time { return now }

	// A couple of typos, well short of the allowance.
	tr.recordFailure(key)
	tr.recordFailure(key)

	// Come back much later: the stale count must not carry over.
	now = now.Add(failureWindow + time.Minute)

	for i := range maxFailedLogins - 1 {
		if lockout := tr.recordFailure(key); lockout != 0 {
			t.Fatalf("stale failures carried over: failure %d locked out (%v)", i+1, lockout)
		}
	}
}

func TestLoginThrottleSuccessClearsHistory(t *testing.T) {
	tr := newLoginThrottle()
	key := "admin\x0010.0.0.1"

	for range maxFailedLogins - 1 {
		tr.recordFailure(key)
	}
	tr.recordSuccess(key)

	// A fresh allowance after the successful login.
	for i := range maxFailedLogins - 1 {
		if lockout := tr.recordFailure(key); lockout != 0 {
			t.Fatalf("history survived a successful login: failure %d locked out (%v)", i+1, lockout)
		}
	}
}

// Locking one (user, IP) pair must not lock the same user from a different
// address — otherwise an attacker could lock a known account out of its own
// service on purpose.
func TestLoginThrottleIsolatesKeys(t *testing.T) {
	tr := newLoginThrottle()
	victim := "admin\x0010.0.0.1"
	attacker := "admin\x00203.0.113.9"

	for range maxFailedLogins * 3 {
		tr.recordFailure(attacker)
	}

	if wait := tr.retryAfter(attacker); wait <= 0 {
		t.Fatal("the attacking address should be locked out")
	}
	if wait := tr.retryAfter(victim); wait != 0 {
		t.Fatalf("the same user from another address must not be locked out (%v)", wait)
	}
}

func TestLoginThrottleSweepsExpiredEntries(t *testing.T) {
	tr := newLoginThrottle()

	now := time.Now()
	tr.now = func() time.Time { return now }

	// Fill past the sweep threshold with entries that will go stale.
	for i := range sweepThreshold + 1 {
		tr.recordFailure(string(rune(i)) + "\x00host")
	}
	if len(tr.attempts) <= sweepThreshold {
		t.Fatalf("expected the map to exceed the sweep threshold, got %d", len(tr.attempts))
	}

	// Once they age out, the next failure sweeps them away.
	now = now.Add(failureWindow + time.Minute)
	tr.recordFailure("trigger\x00host")

	if len(tr.attempts) != 1 {
		t.Fatalf("expected the sweep to drop stale entries, %d remain", len(tr.attempts))
	}
}
