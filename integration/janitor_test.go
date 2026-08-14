package integration

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestJanitor_Expiry(t *testing.T) {
	session := PrepareAuth(t, db, "pruner", false, nil, AuthH.Config.Server.JwtSecret)
	past := time.Now().Add(-6 * time.Hour)

	t.Run("Do not remove expired directory if it contains an active file", func(t *testing.T) {
		dirPath := "/old-folder"
		filePath := dirPath + "/important.txt"

		// Upload active file (no expiry)
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", filePath, WithSession(session), WithBody([]byte("don't delete me"))).Code)

		// Mark the directory as expired via PATCH
		assert.Equal(t, http.StatusOK, Perform(t, router, "PATCH", "/_/api/v1/fs"+dirPath, WithSession(session),
			WithJSON(H{"retention": H{"expires": H{"at": past.Format(time.RFC3339)}}})).Code)

		Meta.RunCleanup()

		assert.DirExists(t, filepath.Join(baseDir, "old-folder"), "Directory should not be removed if not empty")
		assert.FileExists(t, filepath.Join(baseDir, "old-folder", "important.txt"), "File inside should still exist")

		var meta api.MetaResource
		res := db.Where("path = ?", dirPath).Limit(1).Find(&meta)
		assert.NotZero(t, res.RowsAffected, "Database record should remain until physical dir is empty")
	})

	t.Run("Remove expired directory once it is empty", func(t *testing.T) {
		dirPath := "/empty-folder"
		os.MkdirAll(filepath.Join(baseDir, "empty-folder"), 0755)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PATCH", "/_/api/v1/fs"+dirPath, WithSession(session),
			WithJSON(H{"retention": H{"expires": H{"at": past.Format(time.RFC3339)}}})).Code)

		Meta.RunCleanup()

		assert.NoDirExists(t, filepath.Join(baseDir, "empty-folder"), "Empty expired directory should be removed")

		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", dirPath).Count(&count)
		assert.Equal(t, int64(0), count, "Database record should be deleted")
	})

	t.Run("Remove expired files", func(t *testing.T) {

		filePath := "ghost.txt"

		w := Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd")))
		assert.Equal(t, 201, w.Code)
		assert.FileExists(t, path.Join(Meta.BaseDir, filePath))

		Meta.RunCleanup()

		assert.NoFileExists(t, path.Join(Meta.BaseDir, filePath), "Janitor should have deleted the file")
		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", "/"+filePath).Count(&count)
		assert.Equal(t, int64(0), count, "Janitor should have removed DB record")
	})

	t.Run("Yaar-Retention-Expire-After-Upload calculates correct absolute time", func(t *testing.T) {
		target := "/expiry-test.txt"
		w := Perform(t, router, "PUT", target, WithSession(session), WithBody([]byte("data")), WithHeader("Yaar-Retention-Expire-After-Upload", "1h"))
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var meta api.MetaResource
		db.Where("path = ?", target).First(&meta)

		// Assert time is roughly now + 1 hour (allowing 2s buffer for test execution)
		expected := time.Now().Add(time.Hour)
		assert.WithinDuration(t, expected, *meta.DeleteAfter, 2*time.Second)
		assert.Equal(t, "1h", *meta.ExpiryAfterUpload)
	})

	t.Run("Stream-Auto-Expire-Previous removes older group in same stream", func(t *testing.T) {
		stream := "ci-builds"

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"retain_latest": true, "auto_expire_previous": true})).Code)

		w := Perform(t, router, "PUT", "/b1.bin", WithSession(session), WithBody([]byte("v1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v1"))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PUT", "/b2.bin", WithSession(session), WithBody([]byte("v2")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v2"))
		assert.Equal(t, http.StatusOK, w.Code)

		var metaV1 api.MetaResource
		db.Preload("Group.Stream").
			Joins("JOIN groups ON groups.id = meta_resources.group_id").
			Joins("JOIN streams ON streams.id = groups.stream_id").
			Where("groups.name = ? AND streams.name = ?", "v1", stream).
			First(&metaV1)

		var metaV2 api.MetaResource
		db.Preload("Group.Stream").
			Joins("JOIN groups ON groups.id = meta_resources.group_id").
			Joins("JOIN streams ON streams.id = groups.stream_id").
			Where("groups.name = ? AND streams.name = ?", "v2", stream).
			First(&metaV2)

		// Build 1 should now be expired (Expires <= Now)
		assert.True(t, metaV1.Group.EffectiveDeleteAfter.Before(time.Now().Add(time.Second)), "Old group should have expired")
		// Build 2 should have no expiry
		assert.True(t, metaV2.Group.EffectiveDeleteAfter == nil, "New group should have no expiry")
	})

	t.Run("Stream-Latest-Expire-Policy without Auto-Expire just expires normally", func(t *testing.T) {
		stream := "exp-policy"
		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"retain_latest": true}))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PUT", "/latestexpiry1.bin", WithSession(session), WithBody([]byte("1.bin")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v1"),
			WithHeader("Yaar-Retention-Expire-At", past.Format(time.RFC3339)))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PUT", "/latestexpiry2.bin", WithSession(session), WithBody([]byte("2.bin")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v2"),
			WithHeader("Yaar-Retention-Expire-At", past.Format(time.RFC3339)))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PUT", "/latestexpiry3.bin", WithSession(session), WithBody([]byte("3.bin")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v3"),
			WithHeader("Yaar-Retention-Expire-At", time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339)))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PUT", "/latestexpiry4.bin", WithSession(session), WithBody([]byte("4.bin")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v4"),
			WithHeader("Yaar-Retention-Expire-At", past.Format(time.RFC3339)))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PUT", "/latestexpiry5.bin", WithSession(session), WithBody([]byte("5.bin")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v5"),
			WithHeader("Yaar-Retention-Expire-At", time.Now().UTC().Format(time.RFC3339)))
		assert.Equal(t, http.StatusOK, w.Code)

		Meta.RunCleanup()

		assert.NoFileExists(t, path.Join(Meta.BaseDir, "latestexpiry1.bin"), "should be removed because expired")
		assert.NoFileExists(t, path.Join(Meta.BaseDir, "latestexpiry2.bin"), "should be removed because expired")
		assert.FileExists(t, path.Join(Meta.BaseDir, "latestexpiry3.bin"), "not expired yet because expiry is 10minu10 minutes in future")
		assert.NoFileExists(t, path.Join(Meta.BaseDir, "latestexpiry4.bin"), "should be removed because expired")
		assert.FileExists(t, path.Join(Meta.BaseDir, "latestexpiry5.bin"), "protected by retain")
	})
}

func TestJanitor_SafetyGuards(t *testing.T) {
	ClearDatabase(Meta.DB)
	session := PrepareAuth(t, db, "safety", false, nil, AuthH.Config.Server.JwtSecret)
	WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/protected-dir"} })

	// Upload immutable file with expiry (expiry should be ignored)
	assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/immutable-file.txt", WithSession(session),
		WithBody([]byte("data")),
		WithHeader("Yaar-Retention-Immutable", "true"),
		WithHeader(api.HeaderRetentionExpireAt, time.Now().Add(-1*time.Hour).Format(time.RFC3339))).Code)

	// Upload file in protected path with expiry
	assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/protected-dir/expired.txt", WithSession(session),
		WithBody([]byte("data")),
		WithHeader(api.HeaderRetentionExpireAt, time.Now().Add(-1*time.Hour).Format(time.RFC3339))).Code)

	Meta.RunCleanup()

	var count int64
	db.Model(&api.MetaResource{}).Count(&count)
	assert.Equal(t, int64(2), count, "Janitor should have skipped BOTH files due to safety guards")
}

func TestJanitor_AutoPrune(t *testing.T) {
	baseDir := Meta.BaseDir
	session := PrepareAuth(t, db, "autopruner", false, nil, AuthH.Config.Server.JwtSecret)
	past := time.Now().Add(-6 * time.Hour)

	t.Run("Do NOT prune empty directory by default", func(t *testing.T) {
		filePath := "/temp-folder/expired.txt"
		w := Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd")))
		assert.Equal(t, 201, w.Code)

		Meta.RunCleanup()

		// Assertions: File is gone, but Directory remains
		assert.NoFileExists(t, filepath.Join(baseDir, "temp-folder", "expired.txt"))
		assert.DirExists(t, filepath.Join(baseDir, "temp-folder"))
	})

	t.Run("Prune directory if AutoPrune is true and it becomes empty", func(t *testing.T) {
		dirPath := "/prune-parent-dir"
		filePath := "/prune-parent-dir/expired.txt"
		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd"))).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs"+dirPath, WithSession(session), WithJSON(H{"retention": H{"auto_prune": true}})).Code)

		Meta.RunCleanup()

		// Assertions: Both file and directory are gone
		assert.NoFileExists(t, filepath.Join(baseDir, "prune-parent-dir", "expired.txt"))
		assert.NoDirExists(t, filepath.Join(baseDir, "prune-parent-dir"))

		// DB records should be cleared
		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", dirPath).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("Prune nested folders recursively", func(t *testing.T) {
		deepPath := "/project-a/logs/ci"
		filePath := deepPath + "/temp.log"

		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd"))).Code)
		// Mark the ci directory as AutoPrune - so its parent will be removed too
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs"+"/project-a/logs", WithSession(session), WithJSON(H{"retention": H{"auto_prune": true}})).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs"+"/project-a/logs/ci", WithSession(session), WithJSON(H{"retention": H{"auto_prune": true}})).Code)

		Meta.RunCleanup()

		assert.NoFileExists(t, filepath.Join(baseDir, filePath))
		assert.NoDirExists(t, filepath.Join(baseDir, deepPath), "Nested folder 'ci' should be pruned")
		assert.NoDirExists(t, filepath.Join(baseDir, "project-a/logs"), "Intermediate folder 'logs' should be pruned")
		assert.DirExists(t, filepath.Join(baseDir, "project-a"), "Top-level 'project-a' should be present")
	})

	t.Run("Do not prune if directory contains untracked files", func(t *testing.T) {
		filePath := "/prune-me/tracked.txt"

		// Upload a tracked file with expiry so the dir gets a MetaResource
		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("tracked"))).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs/prune-me", WithSession(session), WithJSON(H{"retention": H{"auto_prune": true}})).Code)

		// Add an untracked file on disk only
		os.WriteFile(filepath.Join(baseDir, "prune-me/manual.txt"), []byte("external"), 0644)

		Meta.RunCleanup()

		assert.DirExists(t, filepath.Join(baseDir, "prune-me"), "Should not delete because manual.txt is still on disk")
	})

	t.Run("Protected paths are not pruned", func(t *testing.T) {
		WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/stable"} })

		os.MkdirAll(filepath.Join(baseDir, "stable/temp"), 0755)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PATCH", "/_/api/v1/fs/stable/temp", WithSession(session),
			WithJSON(H{"retention": H{"auto_prune": true, "expires": H{"at": past.Format(time.RFC3339)}}})).Code)

		Meta.RunCleanup()

		assert.DirExists(t, filepath.Join(baseDir, "stable/temp"))
	})

	t.Run("Manual API delete triggers prune", func(t *testing.T) {
		filePath := "/api-test/target.txt"
		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithBody([]byte("abcd"))).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs"+"/api-test", WithSession(session), WithJSON(H{"retention": H{"auto_prune": true}})).Code)
		assert.Equal(t, 204, Perform(t, router, "DELETE", filePath, WithSession(session)).Code)
		assert.NoDirExists(t, filepath.Join(baseDir, "api-test"), "Folder should have been pruned immediately after manual file delete")
	})
}

func TestJanitor_PruneChildren(t *testing.T) {
	baseDir := Meta.BaseDir
	session := PrepareAuth(t, db, "childpruner", false, nil, AuthH.Config.Server.JwtSecret)
	past := time.Now().Add(-6 * time.Hour)

	t.Run("Recursively prune empty descendants at any depth", func(t *testing.T) {
		// PruneChildren on /root authorizes pruning of empty descendants at any
		// depth, even though the intermediate dirs have no retention of their own.
		deepPath := "/root/a/b/c"
		filePath := deepPath + "/temp.log"

		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd"))).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs/root", WithSession(session), WithJSON(H{"retention": H{"prune_children": true}})).Code)

		Meta.RunCleanup()

		assert.NoFileExists(t, filepath.Join(baseDir, filePath))
		assert.NoDirExists(t, filepath.Join(baseDir, "root/a/b/c"), "Deepest empty descendant should be pruned")
		assert.NoDirExists(t, filepath.Join(baseDir, "root/a/b"), "Intermediate descendant should be pruned")
		assert.NoDirExists(t, filepath.Join(baseDir, "root/a"), "Intermediate descendant should be pruned")
		assert.DirExists(t, filepath.Join(baseDir, "root"), "The PruneChildren root itself should be retained")
	})

	t.Run("Nested PruneChildren dirs both retained, descendants pruned", func(t *testing.T) {
		// /x and /x/y both have PruneChildren. An empty subtree below /x/y is
		// pruned, but both PruneChildren dirs themselves are retained.
		deepPath := "/x/y/z/w"
		filePath := deepPath + "/temp.log"

		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd"))).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs/x", WithSession(session), WithJSON(H{"retention": H{"prune_children": true}})).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs/x/y", WithSession(session), WithJSON(H{"retention": H{"prune_children": true}})).Code)

		Meta.RunCleanup()

		assert.NoDirExists(t, filepath.Join(baseDir, "x/y/z/w"), "Deepest descendant should be pruned")
		assert.NoDirExists(t, filepath.Join(baseDir, "x/y/z"), "Intermediate descendant should be pruned")
		assert.DirExists(t, filepath.Join(baseDir, "x/y"), "Nested PruneChildren dir should be retained")
		assert.DirExists(t, filepath.Join(baseDir, "x"), "Outer PruneChildren dir should be retained")
	})

	t.Run("Do not prune a protected dir or cross it", func(t *testing.T) {
		WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/guarded/keep"} })

		// /guarded has PruneChildren. /guarded/keep is protected and, once its
		// file expires, becomes empty — but a protected dir must never be pruned,
		// and the prune walk must stop at it rather than climbing to /guarded.
		filePath := "/guarded/keep/temp.log"

		assert.Equal(t, 201, Perform(t, router, "POST", filePath, WithSession(session), WithHeader(api.HeaderRetentionExpireAt, past.Format(time.RFC3339)), WithBody([]byte("abcd"))).Code)
		assert.Equal(t, 200, Perform(t, router, "PATCH", "/_/api/v1/fs/guarded", WithSession(session), WithJSON(H{"retention": H{"prune_children": true}})).Code)

		Meta.RunCleanup()

		// The file is under a protected path, so it is itself skipped (deadline
		// cleared) and the protected directory remains.
		assert.DirExists(t, filepath.Join(baseDir, "guarded/keep"), "Protected directory must not be pruned")
	})
}

// Test group-level retention: files in same group expire together
func TestGroupRetention(t *testing.T) {
	t.Run("Group members expire together (wait for slowest)", func(t *testing.T) {
		session := PrepareAuth(t, db, "groupretention1", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "reports"
		group := "q1-2024"

		// Upload file1 with 1 second expiry
		target1 := "/reports/summary.pdf"
		w := Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("summary data")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Upload file2 with 5 second expiry
		target2 := "/reports/details.xlsx"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("details data")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "5s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify individual expiry times are different
		var meta1, meta2 api.MetaResource
		db.Preload("Group.Stream").Where("path = ?", target1).First(&meta1)
		db.Preload("Group.Stream").Where("path = ?", target2).First(&meta2)

		assert.NotNil(t, meta1.DeleteAfter)
		assert.NotNil(t, meta2.DeleteAfter)
		assert.True(t, meta1.DeleteAfter.Before(*meta2.DeleteAfter))

		// Verify group expiry is set to the latest (file2's expiry)
		assert.Equal(t, meta2.DeleteAfter.Unix(), meta1.Group.EffectiveDeleteAfter.Unix(), "group deadline should match slowest member")

		// Wait for file1 to expire individually (2 seconds)
		time.Sleep(2 * time.Second)

		// Run janitor
		Meta.RunCleanup()

		// file1 should NOT be deleted (protected by group)
		db.Where("path = ?", target1).First(&meta1)
		assert.NotZero(t, meta1.ID, "file1 should still exist (protected by group)")

		// Wait for file2 to expire (4 more seconds)
		time.Sleep(4 * time.Second)

		// Run janitor again
		Meta.RunCleanup()

		// Now BOTH files should be deleted
		var count int64
		db.Model(&api.MetaResource{}).Where("path IN ?", []string{target1, target2}).Count(&count)
		assert.Equal(t, int64(0), count, "both files should be deleted together")
	})

	t.Run("DeleteAfter updated when member added", func(t *testing.T) {
		session := PrepareAuth(t, db, "gr2", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "logs"
		group := "2024-01"

		// Upload file1 with 2s expiry
		target1 := "/logs/app.log"
		w := Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("log1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "2s"))
		assert.Equal(t, http.StatusOK, w.Code)

		var meta1 api.MetaResource
		db.Where("path = ?", target1).First(&meta1)
		firstGroupExpiry := *meta1.DeleteAfter

		// Upload file2 with 10s expiry
		target2 := "/logs/error.log"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("log2")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "10s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Reload file1 and check DeleteAfter updated
		db.Preload("Group.Stream").Where("path = ?", target1).First(&meta1)
		assert.True(t, meta1.Group.EffectiveDeleteAfter.After(firstGroupExpiry),
			"DeleteAfter should be updated to later expiry")

	})

	t.Run("File with no expiry keeps entire group alive", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor1", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "archive"
		group := "important"

		// Upload file1 with 1s expiry
		target1 := "/archive/temp.txt"
		w := Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("temp")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Upload file2 with NO expiry
		target2 := "/archive/permanent.txt"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("permanent")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group))
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify file2 has no expiry — group deadline should also be nil
		var meta2 api.MetaResource
		db.Preload("Group").Where("path = ?", target2).First(&meta2)
		assert.Nil(t, meta2.DeleteAfter)
		assert.Nil(t, meta2.Group.EffectiveDeleteAfter, "group deadline should be nil when any member has no expiry")

		// Wait for file1 to expire
		time.Sleep(2 * time.Second)

		// Run janitor
		Meta.RunCleanup()

		// Both files should still exist
		var count int64
		db.Model(&api.MetaResource{}).Where("path IN ?", []string{target1, target2}).Count(&count)
		assert.Equal(t, int64(2), count, "both files should exist (file2 has no expiry)")
	})
}

// Test latest group retention policy
func TestLatestGroupRetention(t *testing.T) {
	t.Run("Latest group with retain policy is protected", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor2", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "deployments"

		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"retain_latest": true, "auto_expire_previous": false}))

		// Upload v1.0 (will become old)
		target1 := "/deploy/v1.0/app.jar"
		w = Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("v1.0")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v1.0"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Wait to ensure timestamp difference
		time.Sleep(100 * time.Millisecond)

		// Upload v1.1 (becomes latest)
		target2 := "/deploy/v1.1/app.jar"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("v1.1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v1.1"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Wait for both to expire
		time.Sleep(2 * time.Second)

		// Run janitor
		Meta.RunCleanup()

		// v1.0 should be deleted (not latest)
		var meta1 api.MetaResource
		result := db.Where("path = ?", target1).Limit(1).Find(&meta1)
		assert.NoError(t, result.Error)
		assert.EqualValues(t, 0, result.RowsAffected, "v1.0 should be deleted (not latest)")

		// v1.1 should be protected (is latest with retain policy)
		var meta2 api.MetaResource
		db.Where("path = ?", target2).First(&meta2)
		assert.NotZero(t, meta2.ID, "v1.1 should be protected (latest with retain)")
	})

	t.Run("Latest retain with retain_latest_max_expiry expires after deadline", func(t *testing.T) {
		session := PrepareAuth(t, db, "gr3", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "temp-deploy"
		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"retain_latest": true, "retain_latest_max_expiry": "2s"}))

		// Upload with retain deadline
		target := "/temp-deploy/v1.0/app.jar"
		w = Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("v1.0")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"v1.0"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Wait for expiry but before deadline
		time.Sleep(1500 * time.Millisecond)
		Meta.RunCleanup()

		// Should still exist (protected by retain until deadline)
		var meta api.MetaResource
		res := db.Where("path = ?", target).Limit(1).Find(&meta)
		assert.NoError(t, res.Error)
		assert.NotZero(t, meta.ID, "should be protected before deadline (%+v)", meta)

		// Wait for deadline to pass
		time.Sleep(1 * time.Second)
		Meta.RunCleanup()

		// Should now be deleted (deadline passed)
		result := db.Where("path = ?", target).Limit(1).Find(&meta)
		assert.NoError(t, result.Error)
		assert.EqualValues(t, 0, result.RowsAffected, "should be deleted after deadline")
	})

	t.Run("Older group loses retain protection when newer uploaded", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor5", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "releases"

		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"retain_latest": true}))

		// Upload v1 (initially latest with retain)
		target1 := "/releases/v1/app.jar"
		w = Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("v1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"v1"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Wait for v1 to expire
		time.Sleep(1500 * time.Millisecond)
		Meta.RunCleanup()

		// v1 should still exist (latest with retain)
		var meta1 api.MetaResource
		db.Where("path = ?", target1).First(&meta1)
		assert.NotZero(t, meta1.ID, "v1 should be protected (latest)")

		// Upload v2 (becomes new latest)
		target2 := "/releases/v2/app.jar"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("v2")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"v2"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "10s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Run janitor again
		Meta.RunCleanup()

		// v1 should now be deleted (no longer latest)
		result := db.Where("path = ?", target1).Limit(1).Find(&meta1)
		assert.NoError(t, result.Error)
		assert.EqualValues(t, 0, result.RowsAffected, "v1 should be deleted (no longer latest)")

		// v2 should exist (new latest)
		var meta2 api.MetaResource
		db.Where("path = ?", target2).First(&meta2)
		assert.NotZero(t, meta2.ID, "v2 should exist (new latest)")
	})
}

// Test auto-expire previous groups
func TestAutoExpirePreviousEnhanced(t *testing.T) {
	t.Run("Auto-expire sets DeleteAfter and ExpiredReason", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor6", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "artifacts"
		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"auto_expire_previous": true}))

		// Upload group1
		target1 := "/artifacts/build-123/app.jar"
		w = Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("build 123")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"build-123"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Upload group2 (should expire group1)
		target2 := "/artifacts/build-456/app.jar"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("build 456")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"build-456"))
		assert.Equal(t, http.StatusOK, w.Code)

		var meta1 api.MetaResource
		db.Preload("Group").Where("path = ?", target1).First(&meta1)
		assert.NotNil(t, meta1.Group.EffectiveDeleteAfter, "EffectiveDeleteAfter should be set on group")
		assert.Nil(t, meta1.DeleteAfter, "DeleteAfter should be nil (no user expiry set)")
	})

	t.Run("Auto-expire ignores created_at, only group matters", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor7", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "versions"
		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"auto_expire_previous": true}))

		// Upload newer file first
		target1 := "/versions/v2/app.jar"
		w = Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("v2")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"v2"))
		assert.Equal(t, http.StatusOK, w.Code)

		time.Sleep(100 * time.Millisecond)

		// Upload older version later (different group)
		target2 := "/versions/v1/app.jar"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("v1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+"v1"))
		assert.Equal(t, http.StatusOK, w.Code)

		var meta1 api.MetaResource
		db.Preload("Group").Where("path = ?", target1).First(&meta1)
		assert.NotNil(t, meta1.Group.EffectiveDeleteAfter, "v2 group should have expiry set")

		Meta.RunCleanup()

		var check api.MetaResource
		result := db.Where("path = ?", target1).Limit(1).Find(&check)
		assert.EqualValues(t, 0, result.RowsAffected, "v2 should be deleted after janitor")

	})
}

// Test download expiry updates
func TestDownloadExpiryUpdates(t *testing.T) {
	t.Run("Download updates only downloaded file, not entire group", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor8", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "temp"
		group := "session-123"

		// Upload file1 with after_download
		target1 := "/temp/file1.json"
		w := Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("data1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Download", "2s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Upload file2 with after_download
		target2 := "/temp/file2.json"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("data2")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Download", "2s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Get initial expiry times — DeleteAfter is non-nil even before download
		// because the fallback baseline (UpdatedAt) ensures undownloaded files expire eventually.
		var meta1Before, meta2Before api.MetaResource
		db.Where("path = ?", target1).First(&meta1Before)
		db.Where("path = ?", target2).First(&meta2Before)
		assert.NotNil(t, meta1Before.DeleteAfter, "expiry should be set from upload time fallback")
		assert.NotNil(t, meta2Before.DeleteAfter, "expiry should be set from upload time fallback")
		assert.Nil(t, meta1Before.LastDownloadedAt, "no download yet")
		assert.Nil(t, meta2Before.LastDownloadedAt, "no download yet")

		time.Sleep(100 * time.Millisecond)

		// Download file1 only
		w = Perform(t, router, "GET", target1, WithSession(session))
		assert.Equal(t, http.StatusOK, w.Code)

		time.Sleep(100 * time.Millisecond)

		// Download file1 again so LastDownloadedAt is set and expiry advances
		w = Perform(t, router, "GET", target1, WithSession(session))
		assert.Equal(t, http.StatusOK, w.Code)

		// file1: expiry should have moved forward (based on LastDownloadedAt, not UpdatedAt)
		var meta1After api.MetaResource
		db.Where("path = ?", target1).First(&meta1After)
		assert.NotNil(t, meta1After.DeleteAfter, "expiry should be set after download")
		assert.NotNil(t, meta1After.LastDownloadedAt, "LastDownloadedAt should be set")
		assert.True(t, meta1After.DeleteAfter.After(*meta1Before.DeleteAfter), "file1 expiry should advance after download")

		// file2: expiry unchanged (not downloaded), no LastDownloadedAt
		var meta2After api.MetaResource
		db.Where("path = ?", target2).First(&meta2After)
		assert.Equal(t, meta2Before.DeleteAfter, meta2After.DeleteAfter, "file2 expiry should not change")
		assert.Nil(t, meta2After.LastDownloadedAt, "file2 should not have LastDownloadedAt")
	})

	t.Run("Download with no after_download does not extend expiry", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor9", false, nil, AuthH.Config.Server.JwtSecret)

		target := "/static/file.txt"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("data")),
			WithHeader("Yaar-Retention-Expire-After-Upload", "10s"))
		assert.Equal(t, http.StatusOK, w.Code)

		var metaBefore api.MetaResource
		db.Where("path = ?", target).First(&metaBefore)

		// Download
		w = Perform(t, router, "GET", target, WithSession(session))
		assert.Equal(t, http.StatusOK, w.Code)

		// Expiry should be unchanged
		var metaAfter api.MetaResource
		db.Where("path = ?", target).First(&metaAfter)
		assert.Equal(t, metaBefore.DeleteAfter.Unix(), metaAfter.DeleteAfter.Unix(),
			"expiry should not change (no after_download)")
	})
}

// Test PATCH updates to retention policies
func TestRetentionPolicyPATCH(t *testing.T) {
	t.Run("PATCH updates expiry and recalculates group cache", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor15", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "docs"
		group := "v1"

		// Upload with short expiry
		target := "/docs/readme.md"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("readme")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "5s"))
		assert.Equal(t, http.StatusOK, w.Code)

		var metaBefore api.MetaResource
		db.Where("path = ?", target).First(&metaBefore)
		expiryBefore := *metaBefore.DeleteAfter

		// PATCH to extend expiry
		patchBody := `{"retention": {"expires": {"after_upload": "30s"}}}`
		w = Perform(t, router, "PATCH", "/_/api/v1/fs/"+target,
			WithSession(session),
			WithBody([]byte(patchBody)),
			WithHeader("Content-Type", "application/json"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify expiry updated
		var metaAfter api.MetaResource
		db.Where("path = ?", target).First(&metaAfter)
		assert.True(t, metaAfter.DeleteAfter.After(expiryBefore),
			"expiry should be extended")
		assert.Equal(t, "30s", *metaAfter.ExpiryAfterUpload)
	})

	t.Run("PATCH expires block replaces entire config", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor16", false, nil, AuthH.Config.Server.JwtSecret)

		// Upload with after_upload
		target := "/data/test.txt"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("data")),
			WithHeader("Yaar-Retention-Expire-After-Upload", "10s"))
		assert.Equal(t, http.StatusOK, w.Code)

		// PATCH to use after_download instead
		patchBody := `{"retention": {"expires": {"after_download": "1h"}}}`
		w = Perform(t, router, "PATCH", "/_/api/v1/fs/"+target,
			WithSession(session),
			WithBody([]byte(patchBody)),
			WithHeader("Content-Type", "application/json"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify after_upload cleared, after_download set
		var meta api.MetaResource
		db.Where("path = ?", target).First(&meta)
		assert.Nil(t, meta.ExpiryAfterUpload, "after_upload should be cleared")
		assert.NotNil(t, meta.ExpiryAfterDownload)
		assert.Equal(t, "1h", *meta.ExpiryAfterDownload)
	})

	t.Run("PATCH validates cannot combine after_upload and after_download", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor17", false, nil, AuthH.Config.Server.JwtSecret)

		target := "/data/test.txt"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("data")))
		assert.Equal(t, http.StatusOK, w.Code)

		// Try to set both
		patchBody := `{"retention": {"expires": {"after_upload": "10s", "after_download": "1h"}}}`
		w = Perform(t, router, "PATCH", "/_/api/v1/fs/"+target,
			WithSession(session),
			WithBody([]byte(patchBody)),
			WithHeader("Content-Type", "application/json"))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "cannot combine")
	})

}

// Test edge cases
func TestRetentionEdgeCases(t *testing.T) {
	t.Run("Immutable file never expires even with expiry header", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor19", false, nil, AuthH.Config.Server.JwtSecret)

		target := "/archive/important.pdf"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("important")),
			WithHeader("Yaar-Retention-Immutable", "true"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		var meta api.MetaResource
		db.Where("path = ?", target).First(&meta)

		time.Sleep(2 * time.Second)
		Meta.RunCleanup()

		db.Where("path = ?", target).First(&meta)
		assert.NotZero(t, meta.ID, "immutable should never be deleted")
	})

	t.Run("Immutable file in group keeps entire group alive", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor19b", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "archive"
		group := "set1"

		// Upload immutable file — no expiry, blocks group deletion
		target1 := "/archive2/important.pdf"
		w := Perform(t, router, "PUT", target1,
			WithSession(session),
			WithBody([]byte("important")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Immutable", "true"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Upload regular file with short expiry in the same group
		target2 := "/archive2/temp.txt"
		w = Perform(t, router, "PUT", target2,
			WithSession(session),
			WithBody([]byte("temp")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", ""+group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
		assert.Equal(t, http.StatusOK, w.Code)

		time.Sleep(2 * time.Second)
		Meta.RunCleanup()

		// Both should survive — immutable member blocks group cleanup
		var count int64
		db.Model(&api.MetaResource{}).Where("path IN ?", []string{target1, target2}).Count(&count)
		assert.Equal(t, int64(2), count, "immutable member keeps entire group alive")
	})

	t.Run("Admin override of DeleteAfter expires file early", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor20", false, nil, AuthH.Config.Server.JwtSecret)

		// Upload with long expiry
		target := "/data/test.txt"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("data")),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1h"))
		assert.Equal(t, http.StatusOK, w.Code)

		// Manually set DeleteAfter to now (simulate admin expiration)
		var meta api.MetaResource
		db.Where("path = ?", target).First(&meta)
		now := time.Now().UTC()
		meta.DeleteAfter = &now
		db.Save(&meta)

		// Run janitor
		Meta.RunCleanup()

		// Should be deleted despite long DeleteAfter
		result := db.Where("path = ?", target).Limit(1).Find(&meta)
		assert.NoError(t, result.Error)
		assert.EqualValues(t, 0, result.RowsAffected, "should be deleted via DeleteAfter")
	})

	t.Run("Multiple expiry rules - most permissive wins", func(t *testing.T) {
		session := PrepareAuth(t, db, "janitor21", false, nil, AuthH.Config.Server.JwtSecret)

		target := "/data/test.txt"
		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("data")),
			WithHeader("Yaar-Retention-Expire-After-Upload", "30s"),                                          // 30s from now
			WithHeader("Yaar-Retention-Expire-At", time.Now().UTC().Add(5*time.Second).Format(time.RFC3339))) // 5s from now
		assert.Equal(t, http.StatusOK, w.Code)

		// Latest (most permissive) wins — after_upload (30s) beats expires_at (5s)
		var meta api.MetaResource
		db.Where("path = ?", target).First(&meta)

		expected := time.Now().Add(30 * time.Second)
		assert.WithinDuration(t, expected, *meta.DeleteAfter, 2*time.Second)
	})
}

func TestGroupCleanup(t *testing.T) {
	t.Run("Group row deleted when all members expire", func(t *testing.T) {
		session := PrepareAuth(t, db, "groupclean1", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "cleanup-stream"
		group := "cleanup-group"

		target1 := "/cleanup/file1.txt"
		target2 := "/cleanup/file2.txt"

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", target1,
			WithSession(session), WithBody([]byte("f1")),
			WithHeader("Yaar-Stream", stream), WithHeader("Yaar-Group", group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s")).Code)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", target2,
			WithSession(session), WithBody([]byte("f2")),
			WithHeader("Yaar-Stream", stream), WithHeader("Yaar-Group", group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s")).Code)

		var meta api.MetaResource
		db.Preload("Group").Where("path = ?", target1).First(&meta)
		groupID := meta.GroupID

		time.Sleep(2 * time.Second)
		Meta.RunCleanup()

		var groupCount int64
		db.Model(&api.Group{}).Where("id = ?", *groupID).Count(&groupCount)
		assert.Equal(t, int64(0), groupCount, "group row should be deleted when all members expire")

		var memberCount int64
		db.Model(&api.MetaResource{}).Where("group_id = ?", *groupID).Count(&memberCount)
		assert.Equal(t, int64(0), memberCount, "all member records should be deleted")
	})

	t.Run("Group row kept when immutable member exists", func(t *testing.T) {
		session := PrepareAuth(t, db, "groupclean2", false, nil, AuthH.Config.Server.JwtSecret)

		stream := "cleanup-stream2"
		group := "cleanup-group2"

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/cleanup2/locked.txt",
			WithSession(session), WithBody([]byte("locked")),
			WithHeader("Yaar-Stream", stream), WithHeader("Yaar-Group", group),
			WithHeader("Yaar-Retention-Immutable", "true")).Code)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/cleanup2/temp.txt",
			WithSession(session), WithBody([]byte("temp")),
			WithHeader("Yaar-Stream", stream), WithHeader("Yaar-Group", group),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s")).Code)

		time.Sleep(2 * time.Second)
		Meta.RunCleanup()

		var count int64
		db.Model(&api.MetaResource{}).Where("path IN ?", []string{"/cleanup2/locked.txt", "/cleanup2/temp.txt"}).Count(&count)
		assert.Equal(t, int64(2), count, "group kept alive by immutable member")
	})

	t.Run("Group with immutable member is not re-expired by stream retention restore", func(t *testing.T) {
		session := PrepareAuth(t, db, "immutable-restore", false, nil, AuthH.Config.Server.JwtSecret)
		stream := "immutable-stream"

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session),
			WithJSON(H{"retain_latest": true})).Code)

		// Upload v1 group with one immutable member — group should never expire
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/immutable-stream/v1/app.jar", WithSession(session),
			WithBody([]byte("v1")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v1"),
			WithHeader("Yaar-Retention-Immutable", "true")).Code)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/immutable-stream/v1/config.json", WithSession(session),
			WithBody([]byte("{}")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v1"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "1s")).Code)

		// Upload v2 — v1 loses retain protection, effective deadline should be restored
		// but because v1 has an immutable member it should remain NULL, not set to 1s
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/immutable-stream/v2/app.jar", WithSession(session),
			WithBody([]byte("v2")),
			WithHeader("Yaar-Stream", stream),
			WithHeader("Yaar-Group", "v2")).Code)

		var v1meta api.MetaResource
		db.Preload("Group").
			Joins("JOIN groups ON groups.id = meta_resources.group_id").
			Joins("JOIN streams ON streams.id = groups.stream_id").
			Where("groups.name = ? AND streams.name = ?", "v1", stream).
			First(&v1meta)

		assert.Nil(t, v1meta.Group.EffectiveDeleteAfter,
			"v1 group effective deadline should remain NULL — immutable member protects entire group")

		// Janitor should not delete v1
		time.Sleep(2 * time.Second)
		Meta.RunCleanup()

		var count int64
		db.Model(&api.MetaResource{}).
			Joins("JOIN groups ON groups.id = meta_resources.group_id").
			Joins("JOIN streams ON streams.id = groups.stream_id").
			Where("groups.name = ? AND streams.name = ?", "v1", stream).
			Count(&count)
		assert.Equal(t, int64(2), count, "v1 group should not be deleted due to immutable member")
	})

}

func TestStreamPATCH(t *testing.T) {
	session := PrepareAuth(t, db, "streampatch", false, nil, AuthH.Config.Server.JwtSecret)
	stream := "patch-stream"

	t.Run("Create stream via PUT then update via PATCH", func(t *testing.T) {
		w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session),
			WithJSON(H{"retain_latest": true}))
		assert.Equal(t, http.StatusOK, w.Code)

		w = Perform(t, router, "PATCH", "/_/api/v1/streams/"+stream, WithSession(session),
			WithJSON(H{"auto_expire_previous": true}))
		assert.Equal(t, http.StatusOK, w.Code)

		var s api.Stream
		db.Where("name = ?", stream).First(&s)
		assert.Equal(t, true, *s.RetainLatest, "retain_latest should still be set")
		assert.Equal(t, true, *s.AutoExpirePrevious, "auto_expire_previous should be set by PATCH")
	})

	t.Run("PATCH non-existent stream returns 404", func(t *testing.T) {
		w := Perform(t, router, "PATCH", "/_/api/v1/streams/does-not-exist", WithSession(session),
			WithJSON(H{"retain_latest": true}))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("PUT replaces all settings", func(t *testing.T) {
		s := "replace-stream"
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/_/api/v1/streams/"+s, WithSession(session),
			WithJSON(H{"retain_latest": true, "auto_expire_previous": true})).Code)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/_/api/v1/streams/"+s, WithSession(session),
			WithJSON(H{"retain_latest": false})).Code)

		var stream api.Stream
		db.Where("name = ?", s).First(&stream)
		assert.Equal(t, false, *stream.RetainLatest)
		assert.Nil(t, stream.AutoExpirePrevious, "auto_expire_previous should be cleared by PUT")
	})
}

func TestGroupAssignment(t *testing.T) {
	session := PrepareAuth(t, db, "groupassign", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Ungrouped file with no expiry is never deleted", func(t *testing.T) {
		target := "/permanent/keep.txt"
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", target,
			WithSession(session), WithBody([]byte("keep me"))).Code)

		Meta.RunCleanup()

		var meta api.MetaResource
		result := db.Where("path = ?", target).Limit(1).Find(&meta)
		assert.EqualValues(t, 1, result.RowsAffected, "ungrouped file with no expiry should never be deleted")
	})

	t.Run("PATCH assigns file to group", func(t *testing.T) {
		target := "/assign/file.txt"
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", target,
			WithSession(session), WithBody([]byte("data"))).Code)

		w := Perform(t, router, "PATCH", "/_/api/v1/fs"+target, WithSession(session),
			WithJSON(H{"stream": "assign-stream", "group": "g1"}))
		assert.Equal(t, http.StatusOK, w.Code)

		var meta api.MetaResource
		db.Preload("Group.Stream").Where("path = ?", target).First(&meta)
		assert.NotNil(t, meta.GroupID, "GroupID should be set")
		assert.Equal(t, "g1", meta.Group.Name)
		assert.Equal(t, "assign-stream", meta.Group.Stream.Name)
	})

	t.Run("PATCH rejects moving file to different group", func(t *testing.T) {
		target := "/assign/file2.txt"
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", target,
			WithSession(session), WithBody([]byte("data"))).Code)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", "/_/api/v1/streams/assign-stream",
			WithSession(session), WithJSON(H{"auto_expire_previous": true})).Code)

		assert.Equal(t, http.StatusOK, Perform(t, router, "PATCH", "/_/api/v1/fs"+target, WithSession(session),
			WithJSON(H{"stream": "assign-stream", "group": "g1"})).Code)

		w := Perform(t, router, "PATCH", "/_/api/v1/fs"+target, WithSession(session),
			WithJSON(H{"stream": "assign-stream", "group": "g2"}))
		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("PATCH requires both stream and group", func(t *testing.T) {
		target := "/assign/file3.txt"
		assert.Equal(t, http.StatusOK, Perform(t, router, "PUT", target,
			WithSession(session), WithBody([]byte("data"))).Code)

		w := Perform(t, router, "PATCH", "/_/api/v1/fs"+target, WithSession(session),
			WithJSON(H{"stream": "assign-stream"}))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
