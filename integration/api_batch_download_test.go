package integration

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func ensureFile(t *testing.T, name string) os.FileInfo {
	fn := filepath.Join(baseDir, name)
	assert.NoError(t, os.MkdirAll(filepath.Dir(fn), 0755))
	assert.NoError(t, os.WriteFile(fn, []byte("content-"+fn), 0644))
	s, err := os.Stat(fn)
	assert.NoError(t, err)
	return s
}

func TestBatchDownload(t *testing.T) {

	// Create test files
	ensureFile(t, "file1.txt")
	ensureFile(t, "dir1/d1.txt")
	ensureFile(t, "dir2/d2.txt")
	f1 := ensureFile(t, "projects/app1/f1.txt")
	f2 := ensureFile(t, "projects/app1/f2.txt")

	t.Run("Generate valid ZIP stream", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/file1.txt&p=/dir1")

		assert.Equal(t, 200, w.Code)
		assert.Equal(t, "application/zip", w.Header().Get("Content-Type"))

		// Use standard library to verify ZIP content
		zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.NoError(t, err)

		// Expect 2 files inside the zip
		assert.Equal(t, 2, len(zipReader.File))
		assert.Equal(t, "file1.txt", zipReader.File[0].Name)
		assert.Equal(t, "dir1/d1.txt", zipReader.File[1].Name)
	})

	t.Run("Fail on invalid URL parameter", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/test.txt&mode=garbage")

		assert.Equal(t, 400, w.Code)
		var resp map[string]string
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "Invalid mode parameter", w.Body.String())
	})

	t.Run("Merge mode", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/projects/app1/f1.txt&p=/projects/app1/f2.txt&mode=merge")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="app1.zip"`)
		zipReader, _ := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.Equal(t, "f1.txt", zipReader.File[0].Name)
		assert.Equal(t, "f2.txt", zipReader.File[1].Name)

		w = Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1&p=/dir2&mode=merge")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="artifactory_root.zip"`)
		zipReader, _ = zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.Equal(t, "d1.txt", zipReader.File[0].Name)
		assert.Equal(t, "d2.txt", zipReader.File[1].Name)

		w = Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1&p=/projects&mode=merge")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="artifactory_root.zip"`)
		zipReader, _ = zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.Len(t, zipReader.File, 3)

		zipF1 := zipReader.File[1]
		zipF2 := zipReader.File[2]
		d1, err := os.Stat(filepath.Join(baseDir, "/projects/app1"))
		assert.NoError(t, err)

		assert.Equal(t, "d1.txt", zipReader.File[0].Name)
		assert.Equal(t, d1.ModTime().UTC().Truncate(2*time.Second), zipReader.File[0].Modified.UTC().Truncate(2*time.Second))

		assert.Equal(t, "app1/f1.txt", zipF1.Name)
		assert.Equal(t, f1.ModTime().UTC().Truncate(2*time.Second), zipF1.Modified.UTC().Truncate(2*time.Second))
		assert.Equal(t, f1.Size(), zipF1.FileInfo().Size())

		assert.Equal(t, "app1/f2.txt", zipF2.Name)
		assert.Equal(t, f2.ModTime().UTC().Truncate(2*time.Second), zipF2.Modified.UTC().Truncate(2*time.Second))
		assert.Equal(t, f2.Size(), zipF2.FileInfo().Size())
	})

	t.Run("Deduplicate identical paths", func(t *testing.T) {
		// Request the same file twice
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/projects/app1/f1.txt&p=/projects/app1/f1.txt")

		zipReader, _ := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.Equal(t, 1, len(zipReader.File), "should have only one file")
	})

	t.Run("Single entry uses entry name as zip filename", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="dir1.zip"`)

		w = Perform(t, router, "GET", "/_/api/v1/batch?p=/projects/app1/f1.txt")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="f1.txt.zip"`)
	})

	t.Run("Name query parameter overrides zip filename", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1&p=/dir2&name=my-export")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="my-export.zip"`)

		// Also works for single-entry requests
		w = Perform(t, router, "GET", "/_/api/v1/batch?p=/dir1&name=override")
		assert.Contains(t, w.Header().Get("Content-Disposition"), `filename="override.zip"`)
	})

	t.Run("Merge mode: last selection wins on zip entry name conflict", func(t *testing.T) {
		ensureFile(t, "merge_a/x.txt")
		ensureFile(t, "merge_a/y.txt")
		ensureFile(t, "merge_b/x.txt")

		w := Perform(t, router, "GET", "/_/api/v1/batch?p=/merge_a&p=/merge_b&mode=merge")
		assert.Equal(t, 200, w.Code)

		zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		assert.NoError(t, err)
		assert.Len(t, zipReader.File, 2)

		// Collect zip entries by name for order-independent lookup
		zipFiles := make(map[string]*zip.File)
		for _, f := range zipReader.File {
			zipFiles[f.Name] = f
		}

		assert.Contains(t, zipFiles, "x.txt")
		assert.Contains(t, zipFiles, "y.txt")

		// x.txt must come from merge_b (later in the request list)
		rc, err := zipFiles["x.txt"].Open()
		assert.NoError(t, err)
		content, _ := io.ReadAll(rc)
		rc.Close()
		expectedContent := "content-" + filepath.Join(baseDir, "merge_b/x.txt")
		assert.Equal(t, expectedContent, string(content))
	})
}
