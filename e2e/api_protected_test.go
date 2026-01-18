package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestDirectoryListing_Protected(t *testing.T) {
	WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/protected-listing", "/archive"} })

	// Create a protected folder and a file inside
	os.MkdirAll(filepath.Join(baseDir, "protected-listing"), 0755)
	os.WriteFile(filepath.Join(baseDir, "protected-listing", "release.bin"), []byte("data"), 0644)

	// Create an unprotected folder and a file inside
	os.MkdirAll(filepath.Join(baseDir, "public"), 0755)
	os.WriteFile(filepath.Join(baseDir, "public", "readme.txt"), []byte("data"), 0644)

	t.Run("Verify protection flag in Root listing", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var resp []api.FileResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		// Find the 'protected-listing' folder and 'public' folder in the root list
		for _, f := range resp {
			if f.Name == "protected-listing" {
				assert.True(t, f.Policy.IsProtected, "folder /stabprotected-listingle should be marked protected")
			}
			if f.Name == "public" {
				assert.False(t, f.Policy.IsProtected, "folder /public should NOT be marked protected")
			}
		}
	})

	t.Run("Verify protection flag inside protected folder", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/fs/protected-listing")
		assert.Equal(t, http.StatusOK, w.Code)

		var resp []api.FileResponse
		json.Unmarshal(w.Body.Bytes(), &resp)

		assert.NotEmpty(t, resp)
		assert.Equal(t, "release.bin", resp[0].Name)
		assert.True(t, resp[0].Policy.IsProtected, "file inside /protected-listing should be marked protected")
	})
}

func TestProtectedPathEnforcement(t *testing.T) {
	WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/protected-listing"} })
	session := PrepareAuth(t, db, "protector1", false, AuthH.Config.Server.JwtSecret)

	// 3. Create a file in the protected directory
	protectedFilePath := "/protected-listing/release.v1.bin"
	diskPath := filepath.Join(baseDir, "protected-listing", "release.v1.bin")
	os.MkdirAll(filepath.Dir(diskPath), 0755)
	os.WriteFile(diskPath, []byte("original content"), 0644)

	t.Run("Block DELETE on protected path", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, protectedFilePath, nil)
		session.Apply(req)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.FileExists(t, diskPath, "File should still exist after blocked delete")
	})

	t.Run("Block PUT (Overwrite) on protected path", func(t *testing.T) {
		newContent := []byte("malicious overwrite")
		req, _ := http.NewRequest(http.MethodPut, protectedFilePath, bytes.NewBuffer(newContent))
		req.Header.Set("Content-Type", "application/octet-stream")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)

		// Verify content remains unchanged
		onDisk, _ := os.ReadFile(diskPath)
		assert.Equal(t, "original content", string(onDisk))
	})

	t.Run("Block Rename on protected path", func(t *testing.T) {
		payload := map[string]string{"rename_to": "hacked.bin"}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest(http.MethodPost, "/_/api/v1/fs"+protectedFilePath, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.FileExists(t, diskPath, "File should still be at original location")
		assert.NoFileExists(t, filepath.Join(baseDir, "stable", "hacked.bin"))
	})

	t.Run("Allow GET even on protected path", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, protectedFilePath, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Policy only restricts write/delete, not read
		assert.Equal(t, http.StatusOK, w.Code)
	})

}
