package auth

import (
	"sync"
	"time"
)

// Login brute-force resistance.
//
// Failed logins previously returned a plain 401 with no throttling at all, so
// an attacker could try passwords as fast as the server would answer. Attempts
// are tracked per (username, client IP) pair and the account is locked out for
// a growing delay once the allowance is spent.
//
// Keying on the pair rather than on the username alone matters: keying on the
// username only would let anyone lock a known account out of its own service by
// failing logins on purpose, turning the protection into a denial-of-service.
// Including the IP means an attacker slows themselves down without being able
// to lock out a legitimate user elsewhere. It follows that a distributed
// attacker gets a fresh allowance per source address — this raises the cost of
// online guessing, and is not a substitute for a strong password.
const (
	// maxFailedLogins is how many failures a pair may accumulate before the
	// first lockout. Comfortable for a human typo, punishing for a script.
	maxFailedLogins = 5

	// baseLockout is the first lockout, doubling per subsequent failure.
	baseLockout = 1 * time.Minute

	// maxLockout caps the exponential growth.
	maxLockout = 1 * time.Hour

	// failureWindow is how long a quiet pair keeps its failure count. A user
	// who mistypes twice today should not start tomorrow one step from lockout.
	failureWindow = 15 * time.Minute

	// sweepThreshold is the entry count past which a failed attempt also
	// evicts expired entries, bounding memory under a distributed attack.
	sweepThreshold = 1024
)

type loginAttempt struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

type loginThrottle struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt

	// now is injectable so tests can exercise lockout expiry without sleeping.
	now func() time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{
		attempts: make(map[string]*loginAttempt),
		now:      time.Now,
	}
}

// loginThrottler is the process-wide instance used by the Login handler.
var loginThrottler = newLoginThrottle()

// retryAfter reports whether the key is currently locked out, and for how much
// longer. A zero duration means the attempt may proceed.
func (t *loginThrottle) retryAfter(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	a, ok := t.attempts[key]
	if !ok {
		return 0
	}

	now := t.now()
	if now.Before(a.lockedUntil) {
		return a.lockedUntil.Sub(now)
	}

	// Not locked, and quiet for long enough: forget the history entirely.
	if now.Sub(a.lastFailure) > failureWindow {
		delete(t.attempts, key)
	}
	return 0
}

// recordFailure registers a failed attempt and returns the lockout it triggered
// (zero while the key is still within its allowance).
func (t *loginThrottle) recordFailure(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	a, ok := t.attempts[key]
	if !ok || now.Sub(a.lastFailure) > failureWindow {
		a = &loginAttempt{}
		t.attempts[key] = a
	}

	a.failures++
	a.lastFailure = now

	// The map is keyed by (username, IP), so a distributed attack would grow it
	// without bound. Sweep expired entries occasionally — cheap here, since this
	// only runs on failures and we already hold the lock.
	if len(t.attempts) > sweepThreshold {
		t.sweepLocked(now)
	}

	if a.failures < maxFailedLogins {
		return 0
	}

	// Exponential from the first lockout: 1m, 2m, 4m … capped at maxLockout.
	lockout := baseLockout << (a.failures - maxFailedLogins)
	if lockout > maxLockout || lockout <= 0 { // <=0 guards the shift overflowing
		lockout = maxLockout
	}

	a.lockedUntil = now.Add(lockout)
	return lockout
}

// sweepLocked drops entries that are neither locked out nor recent. The caller
// must hold the mutex.
func (t *loginThrottle) sweepLocked(now time.Time) {
	for key, a := range t.attempts {
		if now.Before(a.lockedUntil) {
			continue
		}
		if now.Sub(a.lastFailure) > failureWindow {
			delete(t.attempts, key)
		}
	}
}

// recordSuccess clears the history for a key after a successful login.
func (t *loginThrottle) recordSuccess(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

// reset drops all state. Used by tests.
func (t *loginThrottle) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts = make(map[string]*loginAttempt)
}

// ResetLoginThrottle clears the process-wide login throttle. Exported for tests
// that would otherwise leak lockout state between cases.
func ResetLoginThrottle() { loginThrottler.reset() }
