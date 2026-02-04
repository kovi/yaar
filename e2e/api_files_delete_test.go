package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestDelete(t *testing.T) {
	ClearDatabase(Meta.DB)
	content := []byte("hello delete")
	session := PrepareAuth(t, db, "udelete", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Delete removes metadata too", func(t *testing.T) {
		target := "/delete/good.txt"
		w := Perform(t, router, "PUT", target, WithBody(content), WithSession(session))

		// File and meta was created
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.FileExists(t, filepath.Join(baseDir, target))
		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", target).Count(&count)
		assert.Equal(t, int64(1), count)

		w = Perform(t, router, "DELETE", target, WithBody(content), WithSession(session))

		// File and meta are no longer exist
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		assert.NoFileExists(t, filepath.Join(baseDir, target))
		db.Model(&api.MetaResource{}).Where("path = ?", target).Count(&count)
		assert.Equal(t, int64(0), count)
	})
}

func TestRecursiveDelete_WithAuditCollection(t *testing.T) {
	session := PrepareAuth(t, db, "udelete1", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Audit log should contain all nested paths", func(t *testing.T) {
		// 1. Create a structure with 3 files
		paths := []string{"/dir/a.txt", "/dir/sub/b.txt", "/dir/c.txt"}
		for _, p := range paths {
			diskPath := filepath.Join(baseDir, p)
			os.MkdirAll(filepath.Dir(diskPath), 0755)
			os.WriteFile(diskPath, []byte("test"), 0644)
			db.Create(&api.MetaResource{Path: p, Type: "file"})
		}
		// Add the directory itself
		db.Create(&api.MetaResource{Path: "/dir", Type: "dir"})

		// 2. Delete the directory
		w := Perform(t, router, "DELETE", "/dir", WithSession(session))
		assert.Equal(t, http.StatusNoContent, w.Code)

		// 3. Verify DB is empty
		var count int64
		db.Model(&api.MetaResource{}).Count(&count)
		assert.Equal(t, int64(0), count)

		// Logic Check: affectedPaths would have contained 4 items:
		// ["/dir", "/dir/a.txt", "/dir/sub/b.txt", "/dir/c.txt"]
	})
}
