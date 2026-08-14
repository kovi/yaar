package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Range support comes from http.ServeFile via c.File, but ServeFile also sniffs
// and sets Content-Type itself — and ServeFile's range handling is entangled
// with the headers already on the response. Since ServeFile is called *after*
// ServeFile-bound headers are set from the DB, this is worth asserting
// explicitly rather than assuming: resumable downloads of large artifacts are
// the whole point.
func TestRangeRequests(t *testing.T) {
	ClearDatabase(db)

	// 36 bytes of known content makes offsets easy to reason about.
	content := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	fileName := "range-target.bin"
	urlPath := "/" + fileName
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, fileName), content, 0644))

	// A deliberately non-sniffable content type: if ServeFile were overriding
	// it, or if the stored value were being dropped on a 206, these assertions
	// would catch it.
	const storedType = "application/vnd.yaar-test+binary"
	require.NoError(t, db.Create(&api.MetaResource{
		Path:        urlPath,
		ContentType: storedType,
		SHA256:      "range-sha256",
		SHA1:        "range-sha1",
		MD5:         "range-md5",
	}).Error)

	t.Run("Server advertises range support", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"),
			"clients look for Accept-Ranges before attempting a resume")
	})

	t.Run("Mid-file range returns exactly the requested bytes", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=10-13"))

		require.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, []byte("ABCD"), w.Body.Bytes())
		assert.Equal(t, "bytes 10-13/36", w.Header().Get("Content-Range"))
		assert.Equal(t, "4", w.Header().Get("Content-Length"))
	})

	// The specific concern: the DB Content-Type must survive a 206.
	t.Run("Stored Content-Type survives a partial response", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=0-3"))

		require.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, storedType, w.Header().Get("Content-Type"),
			"the stored Content-Type must not be replaced by ServeFile's sniffing")
		assert.Equal(t, []byte("0123"), w.Body.Bytes())
	})

	t.Run("Checksum headers are still served on a partial response", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=5-9"))

		require.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, "range-sha256", w.Header().Get("X-Checksum-Sha256"))
		assert.Equal(t, "range-sha256", w.Header().Get("ETag"))
	})

	t.Run("Suffix range returns the tail", func(t *testing.T) {
		// "last 6 bytes" — how a resume of a nearly-complete download looks.
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=-6"))

		require.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, []byte("UVWXYZ"), w.Body.Bytes())
		assert.Equal(t, "bytes 30-35/36", w.Header().Get("Content-Range"))
	})

	t.Run("Open-ended range resumes to the end", func(t *testing.T) {
		// The canonical resume: "I have the first 30 bytes, send the rest."
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=30-"))

		require.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, []byte("UVWXYZ"), w.Body.Bytes())
		assert.Equal(t, "bytes 30-35/36", w.Header().Get("Content-Range"))
	})

	t.Run("Unsatisfiable range is rejected", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=100-200"))

		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, w.Code)
		assert.Equal(t, "bytes */36", w.Header().Get("Content-Range"),
			"a 416 must report the real size so the client can correct itself")
	})

	// Documents stdlib behavior rather than asserting a preference: net/http
	// rejects a syntactically invalid Range with 416 instead of ignoring it and
	// serving the whole file. RFC 9110 permits either reading; this pins which
	// one clients will actually see.
	t.Run("Malformed range is rejected with 416", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=not-a-range"))

		assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, w.Code)
	})

	// A Range header on an unsupported method must not produce a partial
	// response — only GET (and HEAD) are range-capable.
	t.Run("Range is ignored on a non-GET method", func(t *testing.T) {
		session := PrepareAuth(t, db, "range-put-user", false, nil, AuthH.Config.Server.JwtSecret)

		w := Perform(t, router, "PUT", "/range-put-target.bin",
			WithSession(session),
			WithBody([]byte("replacement")),
			WithHeader("Range", "bytes=0-3"))

		assert.Equal(t, http.StatusOK, w.Code,
			"a Range header must not turn an upload into a partial operation")
		onDisk, err := os.ReadFile(filepath.Join(baseDir, "range-put-target.bin"))
		require.NoError(t, err)
		assert.Equal(t, []byte("replacement"), onDisk, "the whole body must be stored")
	})

	t.Run("Multi-range returns a multipart body", func(t *testing.T) {
		w := Perform(t, router, "GET", urlPath, WithHeader("Range", "bytes=0-3,10-13"))

		require.Equal(t, http.StatusPartialContent, w.Code)
		ct := w.Header().Get("Content-Type")
		require.True(t, strings.HasPrefix(ct, "multipart/byteranges"),
			"expected a multipart response, got Content-Type %q", ct)

		// Both requested slices must be present in the multipart body.
		body := w.Body.String()
		assert.Contains(t, body, "0123")
		assert.Contains(t, body, "ABCD")
	})

	t.Run("HEAD advertises range support without a body", func(t *testing.T) {
		w := Perform(t, router, "HEAD", urlPath)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
		assert.Equal(t, fmt.Sprintf("%d", len(content)), w.Header().Get("Content-Length"))
		assert.Zero(t, w.Body.Len(), "HEAD must not return a body")
	})

	// Reassembling every chunk must reproduce the file byte-for-byte — the
	// property an actual chunked downloader depends on.
	t.Run("Chunked download reassembles the original file", func(t *testing.T) {
		const chunk = 7
		var got []byte

		for start := 0; start < len(content); start += chunk {
			end := min(start+chunk-1, len(content)-1)

			w := Perform(t, router, "GET", urlPath,
				WithHeader("Range", fmt.Sprintf("bytes=%d-%d", start, end)))
			require.Equal(t, http.StatusPartialContent, w.Code,
				"chunk %d-%d should be a partial response", start, end)
			require.Equal(t, fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)),
				w.Header().Get("Content-Range"))

			got = append(got, w.Body.Bytes()...)
		}

		assert.Equal(t, content, got, "chunked download must reproduce the file exactly")
	})
}
