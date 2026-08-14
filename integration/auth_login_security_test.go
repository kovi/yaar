package integration

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Five rapid failed logins used to return a plain 401 each with no throttling,
// so there was no brute-force resistance at all.
func TestLoginRateLimiting(t *testing.T) {
	session := PrepareAuth(t, db, "throttle-victim", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)
	auth.ResetLoginThrottle()
	t.Cleanup(auth.ResetLoginThrottle)

	login := func(t *testing.T, username, password string) *httptest.ResponseRecorder {
		t.Helper()
		return Perform(t, router, "POST", "/_/api/login",
			WithJSON(map[string]string{"username": username, "password": password}))
	}

	t.Run("Repeated failures eventually return 429 with Retry-After", func(t *testing.T) {
		auth.ResetLoginThrottle()

		// The allowance is spent on plain 401s.
		for i := range 5 {
			w := login(t, session.User.Username, "wrong-password")
			require.Equal(t, http.StatusUnauthorized, w.Code,
				"attempt %d should still be a plain 401", i+1)
		}

		// The next attempt is refused outright.
		w := login(t, session.User.Username, "wrong-password")
		require.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())

		retryAfter := w.Header().Get("Retry-After")
		require.NotEmpty(t, retryAfter, "a 429 must tell the client when to retry")
		seconds, err := strconv.Atoi(retryAfter)
		require.NoError(t, err, "Retry-After must be a number of seconds")
		assert.Positive(t, seconds)
	})

	t.Run("Lockout rejects even the correct password", func(t *testing.T) {
		// This is what makes it a real lockout rather than a slow 401: once
		// tripped, guessing right does not help until it expires.
		w := login(t, session.User.Username, session.PlainPassword)
		assert.Equal(t, http.StatusTooManyRequests, w.Code, w.Body.String())
	})

	t.Run("A different user is unaffected", func(t *testing.T) {
		other := PrepareAuth(t, db, "throttle-bystander", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)

		w := login(t, other.User.Username, other.PlainPassword)
		assert.Equal(t, http.StatusOK, w.Code,
			"one locked-out account must not block logins for everyone else")
	})

	t.Run("A successful login clears the failure count", func(t *testing.T) {
		auth.ResetLoginThrottle()
		user := PrepareAuth(t, db, "throttle-recovers", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)

		// Fail a few times, short of the allowance.
		for range 3 {
			require.Equal(t, http.StatusUnauthorized, login(t, user.User.Username, "nope").Code)
		}

		// Get it right: this resets the counter.
		require.Equal(t, http.StatusOK, login(t, user.User.Username, user.PlainPassword).Code)

		// A full fresh allowance follows, with no lockout.
		for i := range 5 {
			w := login(t, user.User.Username, "nope")
			require.Equal(t, http.StatusUnauthorized, w.Code,
				"attempt %d after a success should be a plain 401", i+1)
		}
	})

	t.Run("Unknown and known users are indistinguishable", func(t *testing.T) {
		auth.ResetLoginThrottle()

		known := login(t, session.User.Username, "definitely-wrong")
		unknown := login(t, "no-such-user-at-all", "definitely-wrong")

		assert.Equal(t, known.Code, unknown.Code,
			"status must not reveal whether the account exists")
		assert.JSONEq(t, known.Body.String(), unknown.Body.String(),
			"body must not reveal whether the account exists")
	})
}

// The bootstrap admin used to ship with the well-known password "admin123" on
// every fresh install. It is now random, and existing installations are never
// touched.
func TestBootstrapAdminPassword(t *testing.T) {
	auth.ResetLoginThrottle()
	t.Cleanup(auth.ResetLoginThrottle)

	t.Run("The old default password does not work", func(t *testing.T) {
		w := Perform(t, router, "POST", "/_/api/login",
			WithJSON(map[string]string{"username": "admin", "password": "admin123"}))
		assert.Equal(t, http.StatusUnauthorized, w.Code,
			"admin123 must not be a valid credential on a fresh instance")
	})

	t.Run("Generated passwords are random and long", func(t *testing.T) {
		seen := make(map[string]struct{})
		for range 100 {
			p, err := auth.GenerateInitialPassword()
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(p), 20, "password should carry real entropy")
			_, dup := seen[p]
			require.False(t, dup, "generated passwords must not repeat")
			seen[p] = struct{}{}
		}
	})
}

// Login attempts must reach the audit trail, so a brute-force run is visible
// rather than silent.
func TestLoginIsAudited(t *testing.T) {
	auth.ResetLoginThrottle()
	t.Cleanup(auth.ResetLoginThrottle)

	user := PrepareAuth(t, db, "audited-login-user", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)

	t.Run("Success is recorded", func(t *testing.T) {
		w := Perform(t, router, "POST", "/_/api/login",
			WithJSON(map[string]string{"username": user.User.Username, "password": user.PlainPassword}))
		require.Equal(t, http.StatusOK, w.Code)

		entry := findAuditEntry(t, "USER_LOGIN", user.User.Username)
		require.NotNil(t, entry, "a successful login must be audited")
		assert.Equal(t, "SUCCESS", entry["status"])
	})

	t.Run("Failure is recorded", func(t *testing.T) {
		target := "audited-failure-user"
		w := Perform(t, router, "POST", "/_/api/login",
			WithJSON(map[string]string{"username": target, "password": "wrong"}))
		require.Equal(t, http.StatusUnauthorized, w.Code)

		entry := findAuditEntry(t, "USER_LOGIN", target)
		require.NotNil(t, entry, "a failed login must be audited")
		assert.Equal(t, "FAILURE", entry["status"])
	})
}
