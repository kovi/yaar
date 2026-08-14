package integration

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cleanupGroup used to `return nil` on the first immutable or protected member,
// so one locked file kept the entire group alive forever — and the nil return
// recorded the aborted run as a success, hiding the skip. Protected members must
// be skipped individually while the rest of the group expires.
func TestJanitor_GroupWithImmutableMemberExpiresTheRest(t *testing.T) {
	session := PrepareAuth(t, db, "group_partial_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	stream := "partial-stream"
	group := "build-42"
	locked := "/partial/locked.txt"
	expirable1 := "/partial/expirable-1.txt"
	expirable2 := "/partial/expirable-2.txt"

	for _, p := range []string{locked, expirable1, expirable2} {
		w := Perform(t, router, "PUT", p,
			WithSession(session),
			WithBody([]byte("content of "+p)),
			WithHeader(api.HeaderStream, stream),
			WithHeader(api.HeaderGroup, group))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	// Lock one member, then expire the whole group.
	require.NoError(t, db.Model(&api.MetaResource{}).Where("path = ?", locked).
		Update("immutable", true).Error)

	var g api.Group
	require.NoError(t, db.Where("name = ?", group).First(&g).Error)
	past := time.Now().UTC().Add(-1 * time.Hour)
	require.NoError(t, db.Model(&g).Update("effective_delete_after", past).Error)

	require.NoError(t, Meta.RunCleanup())

	// The immutable member survives, on disk and in the DB.
	assert.FileExists(t, filepath.Join(baseDir, "partial", "locked.txt"),
		"immutable member must not be deleted")
	var lockedCount int64
	db.Model(&api.MetaResource{}).Where("path = ?", locked).Count(&lockedCount)
	assert.Equal(t, int64(1), lockedCount, "immutable member's record must survive")

	// Everything else in the group is gone — this is the actual regression.
	for _, p := range []string{expirable1, expirable2} {
		assert.NoFileExists(t, filepath.Join(baseDir, filepath.FromSlash(p)),
			"expirable member %s should have been cleaned up despite the locked sibling", p)

		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", p).Count(&count)
		assert.Equal(t, int64(0), count, "record for %s should have been removed", p)
	}

	// The group itself outlives the run because a member remains, but its
	// deadline is cleared so the janitor stops re-selecting it every tick.
	var after api.Group
	require.NoError(t, db.Where("id = ?", g.ID).First(&after).Error)
	assert.Nil(t, after.EffectiveDeleteAfter,
		"a group retained for a locked member must not keep an expired deadline")
}

// The all-clear path must still delete the group record itself.
func TestJanitor_GroupWithNoProtectedMembersIsFullyRemoved(t *testing.T) {
	session := PrepareAuth(t, db, "group_full_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	stream := "full-stream"
	group := "build-43"
	members := []string{"/full/a.txt", "/full/b.txt"}

	for _, p := range members {
		w := Perform(t, router, "PUT", p,
			WithSession(session),
			WithBody([]byte("content")),
			WithHeader(api.HeaderStream, stream),
			WithHeader(api.HeaderGroup, group))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	var g api.Group
	require.NoError(t, db.Where("name = ?", group).First(&g).Error)
	require.NoError(t, db.Model(&g).
		Update("effective_delete_after", time.Now().UTC().Add(-1*time.Hour)).Error)

	require.NoError(t, Meta.RunCleanup())

	for _, p := range members {
		assert.NoFileExists(t, filepath.Join(baseDir, filepath.FromSlash(p)))
	}

	var groupCount int64
	db.Model(&api.Group{}).Where("id = ?", g.ID).Count(&groupCount)
	assert.Equal(t, int64(0), groupCount, "a fully-expired group should be deleted")
}

// A config-protected member behaves like an immutable one: skipped, with the
// rest of the group still expiring.
func TestJanitor_GroupWithProtectedMemberExpiresTheRest(t *testing.T) {
	session := PrepareAuth(t, db, "group_protected_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	protectedPath := "/guarded/keep.txt"
	expirable := "/loose/drop.txt"
	stream := "protected-member-stream"
	group := "build-44"

	for _, p := range []string{protectedPath, expirable} {
		w := Perform(t, router, "PUT", p,
			WithSession(session),
			WithBody([]byte("content")),
			WithHeader(api.HeaderStream, stream),
			WithHeader(api.HeaderGroup, group))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}

	// Protect the path for the duration of this test only.
	original := Meta.Config.Storage.ProtectedPaths
	Meta.Config.Storage.ProtectedPaths = append(append([]string{}, original...), protectedPath)
	t.Cleanup(func() { Meta.Config.Storage.ProtectedPaths = original })

	var g api.Group
	require.NoError(t, db.Where("name = ?", group).First(&g).Error)
	require.NoError(t, db.Model(&g).
		Update("effective_delete_after", time.Now().UTC().Add(-1*time.Hour)).Error)

	require.NoError(t, Meta.RunCleanup())

	assert.FileExists(t, filepath.Join(baseDir, "guarded", "keep.txt"),
		"protected member must not be deleted")
	assert.NoFileExists(t, filepath.Join(baseDir, "loose", "drop.txt"),
		"unprotected member should have expired despite the protected sibling")
}

// Guards the immutable-member branch against a regression in ptr.Val handling:
// an explicit `immutable = false` must not be mistaken for a lock.
func TestJanitor_ExplicitlyMutableMemberIsNotRetained(t *testing.T) {
	session := PrepareAuth(t, db, "group_mutable_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	target := "/mutable/file.txt"
	stream := "mutable-stream"
	group := "build-45"

	w := Perform(t, router, "PUT", target,
		WithSession(session),
		WithBody([]byte("content")),
		WithHeader(api.HeaderStream, stream),
		WithHeader(api.HeaderGroup, group))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.NoError(t, db.Model(&api.MetaResource{}).Where("path = ?", target).
		Update("immutable", ptr.Of(false)).Error)

	var g api.Group
	require.NoError(t, db.Where("name = ?", group).First(&g).Error)
	require.NoError(t, db.Model(&g).
		Update("effective_delete_after", time.Now().UTC().Add(-1*time.Hour)).Error)

	require.NoError(t, Meta.RunCleanup())

	assert.NoFileExists(t, filepath.Join(baseDir, "mutable", "file.txt"),
		"immutable=false must not retain the member")
}
