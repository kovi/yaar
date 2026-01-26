package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

	t.Run("Generate valid ZIP stream", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/file1.txt&p=/dir1")

		assert.Equal(t, 200, w.Code)
		assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))

		// Use standard library to verify ZIP content
		zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.NoError(t, err)

		// Expect 2 files inside the zip
		assert.Equal(t, 2, len(zipReader.File))
	})

	t.Run("Mode: Literal (Preserves base folder)", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1&p=dir2&p=file1.txt&mode=literal")
		assert.Equal(t, 200, w.Code)

		zipReader, _ := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))

		assert.Len(t, zipReader.File, 3)
		assert.Equal(t, "dir1/file2.txt", zipReader.File[0].Name)
		assert.Equal(t, "dir2/file2-2.txt", zipReader.File[1].Name)
		assert.Equal(t, "file1.txt", zipReader.File[2].Name)
	})

	t.Run("Mode: Merge (Flattens base folder)", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1&p=dir2&p=file1.txt&mode=merge")
		assert.Equal(t, 200, w.Code)

		zipReader, _ := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))

		assert.Len(t, zipReader.File, 3)
		assert.Equal(t, "file2.txt", zipReader.File[0].Name)
		assert.Equal(t, "file2-2.txt", zipReader.File[1].Name)
		assert.Equal(t, "file1.txt", zipReader.File[2].Name)
	})

	t.Run("Fail on invalid URL parameter", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/test.txt&mode=garbage")

		assert.Equal(t, 400, w.Code)
		var resp map[string]string
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "invalid batch mode", w.Body.String())
	})

	t.Run("Fail on invalid Config/Env value", func(t *testing.T) {
		cfg := config.NewConfig()
		cfg.Server.JwtSecret = "12345678901234567890123456789032"
		cfg.Storage.DefaultBatchMode = "oops" // Simulating bad YAML or AF_BATCH_MODE

		err := cfg.Finalize()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid value \"oops\"")
	})
}
