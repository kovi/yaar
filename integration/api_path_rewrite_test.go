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

// Rename and move rewrite the metadata path prefix for a directory and every
// entry beneath it. The rewrite must touch only the *leading* prefix: a path
// whose interior repeats the renamed segment (e.g. /rw-data/data/inner/f.txt) used
// to have every occurrence replaced, so the DB record drifted away from the file
// actually on disk and the metadata was silently orphaned.
//
// Each subtest asserts the same invariant: for every affected row, the DB path
// maps back to a real file under baseDir.

func TestPathPrefixRewriteWithRepeatedSegments(t *testing.T) {
	admin := PrepareAuth(t, db, "admin-path-rewrite", true, nil, AuthH.Config.Server.JwtSecret)

	// writeTree creates the files on disk and their metadata rows.
	writeTree := func(t *testing.T, paths ...string) {
		t.Helper()
		for _, p := range paths {
			disk := filepath.Join(baseDir, filepath.FromSlash(p))
			require.NoError(t, os.MkdirAll(filepath.Dir(disk), 0755))
			require.NoError(t, os.WriteFile(disk, []byte("content of "+p), 0644))
			require.NoError(t, db.Create(&api.MetaResource{
				Path:   p,
				Type:   api.ResourceTypeFile,
				SHA256: "hash-of-" + p,
			}).Error)
		}
	}

	// assertMetaMatchesDisk is the real point of these tests: a DB path that
	// does not resolve to a file on disk is an orphaned record.
	assertMetaMatchesDisk := func(t *testing.T, dbPath string) {
		t.Helper()
		var meta api.MetaResource
		res := db.Where("path = ?", dbPath).Limit(1).Find(&meta)
		require.NoError(t, res.Error)
		require.Equal(t, int64(1), res.RowsAffected, "no metadata row at %q — the path rewrite orphaned it", dbPath)
		assert.FileExists(t, filepath.Join(baseDir, filepath.FromSlash(dbPath)),
			"metadata path %q does not correspond to a file on disk", dbPath)
	}

	t.Run("Rename: repeated segment is rewritten only at the prefix", func(t *testing.T) {
		// /rw-data/data/... repeats "data" in an interior segment. Renaming the
		// outer /rw-data to /rw-renamed must produce /rw-renamed/data/inner/f.txt,
		// matching what os.Rename does on disk. The REPLACE() bug rewrote the
		// interior occurrence too.
		writeTree(t,
			"/rw-data/top.txt",
			"/rw-data/data/inner/f.txt",
			"/rw-data/data/data/deep.txt",
		)

		w := Perform(t, router, "POST", "/_/api/v1/fs/rw-data",
			WithSession(admin),
			WithJSON(map[string]any{"rename_to": "rw-renamed"}),
		)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		assertMetaMatchesDisk(t, "/rw-renamed/top.txt")
		assertMetaMatchesDisk(t, "/rw-renamed/data/inner/f.txt")
		assertMetaMatchesDisk(t, "/rw-renamed/data/data/deep.txt")

		// The corrupted forms must not exist.
		var bogus int64
		db.Model(&api.MetaResource{}).Where("path LIKE ?", "/rw-renamed/renamed%").Count(&bogus)
		assert.Zero(t, bogus, "interior occurrences of the old segment were rewritten")

		// And nothing is left behind under the old prefix.
		var stale int64
		db.Model(&api.MetaResource{}).Where("path = ? OR path LIKE ?", "/rw-data", "/rw-data/%").Count(&stale)
		assert.Zero(t, stale, "metadata still references the pre-rename prefix")
	})

	t.Run("Move: repeated segment is rewritten only at the prefix", func(t *testing.T) {
		writeTree(t,
			"/rw-builds/builds/artifact.tar",
			"/rw-builds/nested/builds/report.txt",
		)

		w := Perform(t, router, "POST", "/_/api/v1/fs/rw-builds",
			WithSession(admin),
			WithJSON(map[string]any{"move_to": "/rw-archive"}),
		)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		assertMetaMatchesDisk(t, "/rw-archive/builds/artifact.tar")
		assertMetaMatchesDisk(t, "/rw-archive/nested/builds/report.txt")

		var bogus int64
		db.Model(&api.MetaResource{}).Where("path LIKE ?", "/rw-archive/archive%").Count(&bogus)
		assert.Zero(t, bogus, "interior occurrences of the old segment were rewritten")
	})

	t.Run("Move: destination name repeated inside source paths", func(t *testing.T) {
		// The *new* prefix already occurs inside the tree. A correct prefix-only
		// rewrite is indifferent to this; it is worth pinning because a
		// search-and-replace implementation is not.
		//
		// Paths are unique to this test: baseDir is shared across the whole e2e
		// suite, so a name another test already created would make os.Rename
		// fail with "file exists".
		writeTree(t, "/rewrite-src/rewrite-dst/keep.txt")

		w := Perform(t, router, "POST", "/_/api/v1/fs/rewrite-src",
			WithSession(admin),
			WithJSON(map[string]any{"move_to": "/rewrite-dst"}),
		)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		assertMetaMatchesDisk(t, "/rewrite-dst/rewrite-dst/keep.txt")
	})

	t.Run("Rename: sibling with the old name as a prefix is untouched", func(t *testing.T) {
		// Guards the LIKE match: renaming /rw-images must not disturb
		// /rw-images-backup, whose name merely starts with the same string.
		writeTree(t,
			"/rw-images/photo.png",
			"/rw-images-backup/photo.png",
		)

		w := Perform(t, router, "POST", "/_/api/v1/fs/rw-images",
			WithSession(admin),
			WithJSON(map[string]any{"rename_to": "rw-pictures"}),
		)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		assertMetaMatchesDisk(t, "/rw-pictures/photo.png")
		assertMetaMatchesDisk(t, "/rw-images-backup/photo.png")
	})

	t.Run("Rename preserves metadata across the rewrite", func(t *testing.T) {
		// The rewrite updates paths in place; the rest of the row must survive.
		writeTree(t, "/rw-logs/logs/run.txt")

		w := Perform(t, router, "POST", "/_/api/v1/fs/rw-logs",
			WithSession(admin),
			WithJSON(map[string]any{"rename_to": "rw-journal"}),
		)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var meta api.MetaResource
		require.NoError(t, db.Where("path = ?", "/rw-journal/logs/run.txt").First(&meta).Error)
		assert.Equal(t, "hash-of-/rw-logs/logs/run.txt", meta.SHA256,
			"row contents should be preserved, only the path prefix rewritten")
	})
}
