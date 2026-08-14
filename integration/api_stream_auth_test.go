package integration

import (
	"net/http"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stream write endpoints set AutoExpirePrevious, which drives janitor
// deletion — so an unauthenticated PUT/PATCH is a path to destroying artifacts,
// not just to unauthorized configuration noise. Reads stay anonymous, matching
// the rest of the read surface.
func TestStreamWritesRequireAuth(t *testing.T) {
	user := PrepareAuth(t, db, "stream-auth-user", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)

	t.Run("Anonymous PUT is rejected and does not create the stream", func(t *testing.T) {
		w := Perform(t, router, "PUT", "/_/api/v1/streams/anon-put",
			WithJSON(map[string]any{"retain_latest": true}),
		)
		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

		var count int64
		db.Model(&api.Stream{}).Where("name = ?", "anon-put").Count(&count)
		assert.Zero(t, count, "rejected request must not have created a stream")
	})

	t.Run("Anonymous PATCH cannot enable auto-expiry on an existing stream", func(t *testing.T) {
		// This is the destructive case: flipping AutoExpirePrevious on a stream
		// that already holds artifacts hands them to the janitor.
		createGroupedResource(db, "/streams-auth/build.txt", "protected-stream", "g1")

		w := Perform(t, router, "PATCH", "/_/api/v1/streams/protected-stream",
			WithJSON(map[string]any{"auto_expire_previous": true}),
		)
		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())

		var stream api.Stream
		require.NoError(t, db.Where("name = ?", "protected-stream").First(&stream).Error)
		assert.Nil(t, stream.AutoExpirePrevious,
			"anonymous request must not have changed the expiry policy")
	})

	t.Run("Authenticated PUT succeeds", func(t *testing.T) {
		w := Perform(t, router, "PUT", "/_/api/v1/streams/authed-put",
			WithSession(user),
			WithJSON(map[string]any{"retain_latest": true}),
		)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var stream api.Stream
		require.NoError(t, db.Where("name = ?", "authed-put").First(&stream).Error)
		require.NotNil(t, stream.RetainLatest)
		assert.True(t, *stream.RetainLatest)
	})

	t.Run("Authenticated PATCH succeeds", func(t *testing.T) {
		w := Perform(t, router, "PATCH", "/_/api/v1/streams/authed-put",
			WithSession(user),
			WithJSON(map[string]any{"auto_expire_previous": true}),
		)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var stream api.Stream
		require.NoError(t, db.Where("name = ?", "authed-put").First(&stream).Error)
		require.NotNil(t, stream.AutoExpirePrevious)
		assert.True(t, *stream.AutoExpirePrevious)
	})

	t.Run("Stream reads remain anonymous", func(t *testing.T) {
		createGroupedResource(db, "/streams-auth/readable.txt", "readable-stream", "g1")

		w := Perform(t, router, "GET", "/_/api/v1/streams")
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		w = Perform(t, router, "GET", "/_/api/v1/streams/readable-stream")
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})
}

// POST /_/api/system/sync was registered directly on the engine in main.go,
// bypassing the AdminRequired() group its /_/api/system/* siblings use. It now
// lives inside that group.
func TestSyncTriggerRequiresAdmin(t *testing.T) {
	admin := PrepareAuth(t, db, "sync-admin", true, nil, AuthH.Config.Server.JwtSecret)
	plain := PrepareAuth(t, db, "sync-plain-user", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)

	t.Run("Anonymous is rejected without running a sync", func(t *testing.T) {
		before := syncTriggers.Load()

		w := Perform(t, router, "POST", "/_/api/system/sync")
		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		assert.Equal(t, before, syncTriggers.Load(), "sync must not have run")
	})

	t.Run("Non-admin is rejected without running a sync", func(t *testing.T) {
		before := syncTriggers.Load()

		w := Perform(t, router, "POST", "/_/api/system/sync", WithSession(plain))
		assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		assert.Equal(t, before, syncTriggers.Load(), "sync must not have run")
	})

	t.Run("Admin triggers the sync", func(t *testing.T) {
		before := syncTriggers.Load()

		w := Perform(t, router, "POST", "/_/api/system/sync", WithSession(admin))
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, before+1, syncTriggers.Load(), "sync should have run exactly once")
	})
}
