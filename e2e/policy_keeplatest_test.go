package e2e

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestPolicyDrivenUploads(t *testing.T) {
	ClearDatabase(Meta.DB)
	session := PrepareAuth(t, db, "protector2", false, AuthH.Config.Server.JwtSecret)

	t.Run("X-Expires calculates correct absolute time", func(t *testing.T) {
		target := "/expiry-test.txt"
		req, _ := http.NewRequest("PUT", target, bytes.NewBuffer([]byte("data")))
		req.Header.Set("X-Expires", "1h") // 1 hour duration
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var meta api.MetaResource
		db.Where("path = ?", target).First(&meta)

		// Assert time is roughly now + 1 hour (allowing 2s buffer for test execution)
		expected := time.Now().Add(time.Hour)
		assert.WithinDuration(t, expected, *meta.ExpiresAt, 2*time.Second)
	})

	t.Run("X-KeepLatest rotates older group in same stream", func(t *testing.T) {
		stream := "ci-builds"

		// 1. Upload Build 1 (v1) with KeepLatest
		req1, _ := http.NewRequest("PUT", "/b1.bin", bytes.NewBuffer([]byte("v1")))
		req1.Header.Set("X-Stream", stream+"/v1")
		req1.Header.Set("X-KeepLatest", "true")
		session.Apply(req1)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req1)
		assert.Equal(t, http.StatusOK, w.Code)

		// 2. Upload Build 2 (v2) with KeepLatest
		req2, _ := http.NewRequest("PUT", "/b2.bin", bytes.NewBuffer([]byte("v2")))
		req2.Header.Set("X-Stream", stream+"/v2")
		req2.Header.Set("X-KeepLatest", "true")
		session.Apply(req2)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req2)
		assert.Equal(t, http.StatusOK, w.Code)

		// 3. Assertions
		var metaV1 api.MetaResource
		db.Where("`group` = ?", "v1").First(&metaV1)

		var metaV2 api.MetaResource
		db.Where("`group` = ?", "v2").First(&metaV2)

		// Build 1 should now be expired (Expires <= Now)
		assert.True(t, metaV1.ExpiresAt.Before(time.Now().Add(time.Second)), "Old group should have expired")
		// Build 2 should have no expiry (or future expiry if set)
		assert.True(t, metaV2.ExpiresAt == nil || metaV2.ExpiresAt.After(time.Now()), "New group should be active")
	})

	t.Run("Janitor physically removes expired files", func(t *testing.T) {
		// 1. Manually create a record that expired 5 minutes ago
		expiredFile := "ghost.txt"
		diskPath := filepath.Join(baseDir, expiredFile)
		os.WriteFile(diskPath, []byte("gone soon"), 0644)

		past := time.Now().UTC().Add(-5 * time.Minute)
		db.Debug().Create(&api.MetaResource{
			Path:      "/" + expiredFile,
			ExpiresAt: &past,
		})

		// 2. Run the Janitor logic once (Manual Trigger)
		Meta.RunCleanup()

		// 3. Verify
		assert.NoFileExists(t, diskPath, "Janitor should have deleted the file")
		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", "/"+expiredFile).Count(&count)
		assert.Equal(t, int64(0), count, "Janitor should have removed DB record")
	})
}

func TestJanitor_SafetyGuards(t *testing.T) {
	ClearDatabase(Meta.DB)
	WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/protected-dir"} })

	// 1. Create a file that is EXPIRED but IMMUTABLE
	past := time.Now().UTC().Add(-1 * time.Hour)
	isImmutable := true
	db.Create(&api.MetaResource{
		Path:      "/immutable-file.txt",
		ExpiresAt: &past,
		Immutable: &isImmutable,
	})

	// 2. Create a file that is EXPIRED but in a PROTECTED path
	db.Create(&api.MetaResource{
		Path:      "/protected-dir/expired.txt",
		ExpiresAt: &past,
	})

	// Run cleanup
	Meta.RunCleanup()

	// 3. Assertions
	var count int64
	db.Model(&api.MetaResource{}).Count(&count)
	assert.Equal(t, int64(2), count, "Janitor should have skipped BOTH files due to safety guards")
}
