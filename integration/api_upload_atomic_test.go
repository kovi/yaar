package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HandleUpload used to write straight to the final path, so a body that failed
// its checksum or arrived truncated left a corrupt artifact live at its real
// URL — and, on an overwrite, destroyed the good file that was already there.
// Uploads now land in a temp file and are renamed into place only once verified.
func TestUploadIsAtomic(t *testing.T) {
	session := PrepareAuth(t, db, "atomic_upload_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	t.Run("Failed checksum leaves the previous file intact", func(t *testing.T) {
		target := "/atomic/existing.txt"
		good := []byte("the original, good content")

		w := Perform(t, router, "PUT", target, WithSession(session), WithBody(good))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		// Overwrite with a body whose declared SHA256 does not match.
		w = Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("corrupt replacement")),
			WithHeader("X-Checksum-Sha256", "0000000000000000000000000000000000000000000000000000000000000000"))
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		onDisk, err := os.ReadFile(filepath.Join(baseDir, "atomic", "existing.txt"))
		require.NoError(t, err)
		assert.Equal(t, good, onDisk,
			"a rejected overwrite must leave the previous artifact untouched")

		// And the served content agrees with the disk.
		w = Perform(t, router, "GET", target)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, good, w.Body.Bytes())
	})

	t.Run("Failed checksum on a new path leaves nothing behind", func(t *testing.T) {
		target := "/atomic/never-created.txt"

		w := Perform(t, router, "PUT", target,
			WithSession(session),
			WithBody([]byte("corrupt")),
			WithHeader("X-Checksum-Sha256", "0000000000000000000000000000000000000000000000000000000000000000"))
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		assert.NoFileExists(t, filepath.Join(baseDir, "atomic", "never-created.txt"),
			"a rejected upload must not leave a partial artifact")
	})

	t.Run("No temp files are left behind", func(t *testing.T) {
		// Both the successful and the rejected uploads above ran in /atomic.
		entries, err := os.ReadDir(filepath.Join(baseDir, "atomic"))
		require.NoError(t, err)

		for _, e := range entries {
			assert.NotContains(t, e.Name(), ".upload-",
				"upload temp files must be cleaned up, found %q", e.Name())
		}
	})

	t.Run("Successful upload is readable with the expected permissions", func(t *testing.T) {
		target := "/atomic/perms.txt"

		w := Perform(t, router, "PUT", target, WithSession(session), WithBody([]byte("data")))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		// os.CreateTemp makes 0600 files; the published artifact must get the
		// same 0644 os.Create used to produce.
		info, err := os.Stat(filepath.Join(baseDir, "atomic", "perms.txt"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
	})
}

// The ETag header used to be set inside the SHA1 branch, so a file that had a
// SHA256 but no SHA1 silently lost its ETag.
func TestETagIsServedWithoutSha1(t *testing.T) {
	ClearDatabase(db)

	fileName := "etag-no-sha1.bin"
	urlPath := "/" + fileName
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, fileName), []byte("payload"), 0644))

	require.NoError(t, db.Create(&api.MetaResource{
		Path:        urlPath,
		ContentType: "application/octet-stream",
		SHA256:      "sha256-only-hash",
		// SHA1 and MD5 deliberately absent.
	}).Error)

	w := Perform(t, router, "GET", urlPath)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "sha256-only-hash", w.Header().Get("ETag"),
		"ETag must be served whenever a SHA256 exists, with or without a SHA1")
	assert.Equal(t, "sha256-only-hash", w.Header().Get("X-Checksum-Sha256"))
	assert.Empty(t, w.Header().Get("X-Checksum-Sha1"))
}
