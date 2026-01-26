package e2e

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestFileRetrieval(t *testing.T) {

	// 2. Pre-seed a file and its metadata
	fileName := "test-blob.bin"
	urlPath := "/" + fileName
	content := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") // 36 bytes
	os.WriteFile(filepath.Join(baseDir, fileName), content, 0644)

	// For the test, we'll just use dummy values to verify they are served correctly
	db.Create(&api.MetaResource{
		Path:        urlPath,
		ContentType: "application/octet-stream",
		SHA256:      "test-sha256-hash",
		SHA1:        "test-sha1-hash",
		MD5:         "test-md5-hash",
	})

	t.Run("Standard GET request", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))

		// Check Custom Integrity Headers
		assert.Equal(t, "test-sha256-hash", w.Header().Get("X-Checksum-Sha256"))

		// Check ETag (Should be the SHA256 per our design)
		assert.Equal(t, "test-sha256-hash", w.Header().Get("ETag"))

		// Verify body integrity
		assert.Equal(t, content, w.Body.Bytes())
	})

	t.Run("GET request with Range header", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=10-13"))

		// 206 Partial Content is expected
		assert.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, "bytes 10-13/36", w.Header().Get("Content-Range"))
		assert.Equal(t, int64(4), int64(w.Body.Len()))
		assert.Equal(t, []byte("ABCD"), w.Body.Bytes())
	})

	t.Run("HEAD request", func(t *testing.T) {
		w := Perform(t, router, "HEAD", urlPath)

		assert.Equal(t, http.StatusOK, w.Code)

		// HEAD must contain the same metadata headers as GET
		assert.Equal(t, "36", w.Header().Get("Content-Length"))
		assert.Equal(t, "test-sha256-hash", w.Header().Get("X-Checksum-Sha256"))
		assert.Equal(t, "test-sha256-hash", w.Header().Get("ETag"))

		// CRITICAL: HEAD must have an EMPTY body
		assert.Equal(t, 0, w.Body.Len())
	})

	t.Run("GET with special characters", func(t *testing.T) {
		fname := "/ab%20cd/file%20name.log"
		assert.NoError(t, os.MkdirAll(filepath.Join(baseDir, filepath.Dir(fname)), 0777))
		assert.NoError(t, os.WriteFile(filepath.Join(baseDir, fname), []byte("abdefe"), 0644))

		w := Perform(t, router, "GET", url.PathEscape(fname))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, []byte("abdefe"), w.Body.Bytes())
	})
}
