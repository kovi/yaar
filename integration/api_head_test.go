package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeadRequests(t *testing.T) {
	session := PrepareAuth(t, db, "head_tester", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("HEAD on non-existent file returns 404", func(t *testing.T) {
		w := Perform(t, router, http.MethodHead, "/missing.txt")
		assert.Equal(t, http.StatusNotFound, w.Code)
		// It should still have text/html since it serves index.html, but with 404.
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	})

	// A missing path serves the SPA so a browser can render its own 404 page, but
	// the status must stay 404 so non-browser clients (CI, curl --fail) can tell
	// the artifact is absent.
	t.Run("GET on non-existent file returns 404 with the SPA body", func(t *testing.T) {
		w := Perform(t, router, http.MethodGet, "/missing.txt")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
		assert.Contains(t, w.Body.String(), "<!DOCTYPE html>")
	})

	t.Run("GET on non-existent file returns 404 for browser Accept too", func(t *testing.T) {
		w := Perform(t, router, http.MethodGet, "/missing.txt", WithHeader("Accept", "text/html,application/xhtml+xml,*/*"))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "<!DOCTYPE html>")
	})

	// A directory is not a downloadable artifact. Browsers get the SPA so they can
	// render the listing; everyone else gets 404 rather than the 301-to-trailing-
	// slash that http.ServeFile would emit, which reads as success to CI tooling.
	t.Run("GET on a directory serves the SPA for browsers", func(t *testing.T) {
		os.MkdirAll(filepath.Join(baseDir, "dirview"), 0755)

		w := Perform(t, router, http.MethodGet, "/dirview", WithHeader("Accept", "text/html,application/xhtml+xml,*/*;q=0.8"))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<!DOCTYPE html>")
	})

	t.Run("GET on a directory returns 404 for non-browser clients", func(t *testing.T) {
		os.MkdirAll(filepath.Join(baseDir, "dirview"), 0755)

		for _, accept := range []string{"", "*/*", "application/json"} {
			w := Perform(t, router, http.MethodGet, "/dirview", WithHeader("Accept", accept))
			assert.Equal(t, http.StatusNotFound, w.Code, "Accept: %q", accept)
			assert.NotEqual(t, http.StatusMovedPermanently, w.Code, "Accept: %q", accept)
		}
	})

	// Media types are case-insensitive and may carry whitespace, so these are all
	// browsers and must reach the SPA, not the 404 path.
	t.Run("GET on a directory serves the SPA for equivalent Accept spellings", func(t *testing.T) {
		os.MkdirAll(filepath.Join(baseDir, "dirview"), 0755)

		for _, accept := range []string{"text/HTML", "text/html ; q=0.9", "text/*"} {
			w := Perform(t, router, http.MethodGet, "/dirview", WithHeader("Accept", accept))
			assert.Equal(t, http.StatusOK, w.Code, "Accept: %q", accept)
		}
	})

	t.Run("HEAD on existing meta directory returns 200", func(t *testing.T) {
		// Create a directory first
		os.MkdirAll(filepath.Join(baseDir, "lmc", "releases"), 0755)

		w := Perform(t, router, http.MethodHead, "/_/api/v1/fs/lmc/releases", WithSession(session))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("HEAD on existing meta file returns 200", func(t *testing.T) {
		// Create a file
		os.MkdirAll(filepath.Join(baseDir, "lmc"), 0755)
		os.WriteFile(filepath.Join(baseDir, "lmc", "file.txt"), []byte("content"), 0644)

		w := Perform(t, router, http.MethodHead, "/_/api/v1/fs/lmc/file.txt", WithSession(session))
		assert.Equal(t, http.StatusOK, w.Code)
	})
}
