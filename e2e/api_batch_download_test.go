package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestBatchDownload(t *testing.T) {
	WithConfig(t, func(c *config.Config) { c.Storage.DefaultBatchMode = "literal" })

	// Create test files
	os.MkdirAll(filepath.Join(baseDir, "dir1"), 0755)
	os.MkdirAll(filepath.Join(baseDir, "dir2"), 0755)
	os.WriteFile(filepath.Join(baseDir, "file1.txt"), []byte("content1"), 0644)
	os.WriteFile(filepath.Join(baseDir, "dir1/file2.txt"), []byte("content2"), 0644)
	os.WriteFile(filepath.Join(baseDir, "dir2/file2-2.txt"), []byte("content3"), 0644)

	os.MkdirAll(filepath.Join(baseDir, "projects", "app1"), 0755)
	os.WriteFile(filepath.Join(baseDir, "projects", "app1", "f1.txt"), []byte("1"), 0644)
	os.WriteFile(filepath.Join(baseDir, "projects", "app1", "f2.txt"), []byte("2"), 0644)

	// Set app1 to "merge" mode
	assert.NoError(t, db.Create(&api.MetaResource{
		Path:         "/projects/app1",
		Type:         "dir",
		DownloadMode: "merge",
	}).Error)

	t.Run("Generate valid ZIP stream", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/file1.txt&p=/dir1")

		assert.Equal(t, 200, w.Code)
		assert.Equal(t, "application/zip", w.Header().Get("Content-Type"))

		// Use standard library to verify ZIP content
		zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.NoError(t, err)

		// Expect 2 files inside the zip
		assert.Equal(t, 2, len(zipReader.File))
	})

	t.Run("Fail on invalid URL parameter", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/test.txt&mode=garbage")

		assert.Equal(t, 400, w.Code)
		var resp map[string]string
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "Invalid mode parameter", w.Body.String())
	})

	t.Run("Filename matches parent and mode is inherited", func(t *testing.T) {
		// Download two files inside /projects/app1
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/projects/app1/f1.txt&p=/projects/app1/f2.txt")

		// 1. Check Filename Header
		assert.Contains(t, w.Header().Get("Content-Disposition"), "filename=app1.zip")

		// 2. Check ZIP Structure (should be merged/flat because parent says so)
		zipReader, _ := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))

		// In merge mode, file is at root
		assert.Equal(t, "f1.txt", zipReader.File[0].Name)
		assert.Equal(t, "f2.txt", zipReader.File[1].Name)
	})

	t.Run("Deduplicate identical paths", func(t *testing.T) {
		// Request the same file twice
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/projects/app1/f1.txt&p=/projects/app1/f1.txt")

		zipReader, _ := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		// Should only have 1 file
		assert.Equal(t, 1, len(zipReader.File))
	})
}
