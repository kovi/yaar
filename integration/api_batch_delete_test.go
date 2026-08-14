package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/models"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
)

func TestBatchDelete(t *testing.T) {
	ClearDatabase(Meta.DB)
	session := PrepareAuth(t, db, "ubatchdel", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Delete multiple files", func(t *testing.T) {
		paths := []string{"/bdel/a.txt", "/bdel/b.txt", "/bdel/c.txt"}
		for _, p := range paths {
			diskPath := filepath.Join(baseDir, p)
			os.MkdirAll(filepath.Dir(diskPath), 0755)
			os.WriteFile(diskPath, []byte("test"), 0644)
			db.Create(&api.MetaResource{Path: p, Type: "file"})
		}

		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": paths}),
			WithSession(session),
		)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		for _, p := range paths {
			assert.NoFileExists(t, filepath.Join(baseDir, p))
			var count int64
			db.Model(&api.MetaResource{}).Where("path = ?", p).Count(&count)
			assert.Equal(t, int64(0), count)
		}
	})

	t.Run("Delete directory recursively", func(t *testing.T) {
		nested := []string{"/bdel2/x.txt", "/bdel2/sub/y.txt"}
		for _, p := range nested {
			diskPath := filepath.Join(baseDir, p)
			os.MkdirAll(filepath.Dir(diskPath), 0755)
			os.WriteFile(diskPath, []byte("test"), 0644)
			db.Create(&api.MetaResource{Path: p, Type: "file"})
		}
		db.Create(&api.MetaResource{Path: "/bdel2", Type: "dir"})

		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": []string{"/bdel2"}}),
			WithSession(session),
		)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NoDirExists(t, filepath.Join(baseDir, "/bdel2"))

		var count int64
		db.Model(&api.MetaResource{}).Where("path LIKE ?", "/bdel2%").Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("Partial success: skip non-existent paths", func(t *testing.T) {
		diskPath := filepath.Join(baseDir, "/bdel3/exists.txt")
		os.MkdirAll(filepath.Dir(diskPath), 0755)
		os.WriteFile(diskPath, []byte("test"), 0644)
		db.Create(&api.MetaResource{Path: "/bdel3/exists.txt", Type: "file"})

		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": []string{"/bdel3/exists.txt", "/bdel3/missing.txt"}}),
			WithSession(session),
		)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NoFileExists(t, filepath.Join(baseDir, "/bdel3/exists.txt"))
	})

	t.Run("Reject unauthenticated requests", func(t *testing.T) {
		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": []string{"/some/file.txt"}}),
		)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Reject empty paths", func(t *testing.T) {
		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": []string{}}),
			WithSession(session),
		)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Block delete when child resource is immutable", func(t *testing.T) {
		diskPath := filepath.Join(baseDir, "/bdel4/sub/locked.txt")
		os.MkdirAll(filepath.Dir(diskPath), 0755)
		os.WriteFile(diskPath, []byte("locked"), 0644)
		db.Create(&api.MetaResource{Path: "/bdel4/sub/locked.txt", Type: "file", Immutable: ptr.Of(true)})
		db.Create(&api.MetaResource{Path: "/bdel4", Type: "dir"})

		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": []string{"/bdel4"}}),
			WithSession(session),
		)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.FileExists(t, diskPath, "immutable child should prevent parent deletion")

		// Error map should name the blocked path
		var resp api.BatchDeleteResponse
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp.Errors, "/bdel4")
	})

	t.Run("Reject out-of-scope paths", func(t *testing.T) {
		restrictedSession := PrepareAuth(t, db, "ubatchdel_restricted", false,
			&models.StringList{"/allowed"},
			AuthH.Config.Server.JwtSecret,
		)

		diskPath := filepath.Join(baseDir, "/restricted/secret.txt")
		os.MkdirAll(filepath.Dir(diskPath), 0755)
		os.WriteFile(diskPath, []byte("secret"), 0644)

		w := Perform(t, router, "DELETE", "/_/api/v1/batch",
			WithJSON(H{"paths": []string{"/restricted/secret.txt"}}),
			WithSession(restrictedSession),
		)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.FileExists(t, diskPath, "file outside allowed scope should not be deleted")
	})
}
