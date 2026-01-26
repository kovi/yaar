package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestHandleMove(t *testing.T) {
	// Setup Admin session
	admin := PrepareAuth(t, db, "admin-move", true, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Success: Move file and update metadata", func(t *testing.T) {
		// 1. Create file and metadata
		oldPath := "/old-folder/file.txt"
		newPath := "/new-folder/moved.txt"
		diskOld := filepath.Join(baseDir, "old-folder", "file.txt")
		os.MkdirAll(filepath.Dir(diskOld), 0755)
		os.WriteFile(diskOld, []byte("content"), 0644)

		db.Create(&api.MetaResource{Path: oldPath, SHA256: "test-hash"})

		// 2. Perform Move
		w := Perform(t, router, "POST", "/_/api/v1/fs"+oldPath,
			WithSession(admin),
			WithJSON(map[string]any{"move_to": newPath}),
		)

		// 3. Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, "new-folder", "moved.txt"))
		assert.NoFileExists(t, diskOld)

		var meta api.MetaResource
		db.Where("path = ?", newPath).First(&meta)
		assert.Equal(t, "test-hash", meta.SHA256, "Metadata should follow the file to the new path")
	})

	t.Run("Success: Recursive Directory Move", func(t *testing.T) {
		// 1. Setup tree: /src/1.txt, /src/sub/2.txt
		os.MkdirAll(filepath.Join(baseDir, "src", "sub"), 0755)
		os.WriteFile(filepath.Join(baseDir, "src", "1.txt"), []byte("1"), 0644)
		os.WriteFile(filepath.Join(baseDir, "src", "sub", "2.txt"), []byte("2"), 0644)

		db.Create(&api.MetaResource{Path: "/src", Type: "dir"})
		db.Create(&api.MetaResource{Path: "/src/1.txt", SHA256: "h1"})
		db.Create(&api.MetaResource{Path: "/src/sub/2.txt", SHA256: "h2"})

		// 2. Move /src to /dest
		Perform(t, router, "POST", "/_/api/v1/fs/src",
			WithSession(admin),
			WithJSON(map[string]any{"move_to": "/dest"}),
		)

		// 3. Verify all 3 paths in DB updated via REPLACE logic
		var count int64
		db.Model(&api.MetaResource{}).Where("path LIKE ?", "/dest%").Count(&count)
		assert.Equal(t, int64(3), count)

		var file2 api.MetaResource
		db.Where("path = ?", "/dest/sub/2.txt").First(&file2)
		assert.Equal(t, "h2", file2.SHA256)
	})

	t.Run("Failure: Circular Move", func(t *testing.T) {
		// Attempt to move a folder into its own child
		os.MkdirAll(filepath.Join(baseDir, "parent", "child"), 0755)

		w := Perform(t, router, "POST", "/_/api/v1/fs/parent",
			WithSession(admin),
			WithJSON(map[string]any{"move_to": "/parent/child/oops"}),
		)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Contains(t, resp["error"], "subdirectory")
	})

	t.Run("Failure: Token Scope Restriction", func(t *testing.T) {
		// 1. Create a scoped user (only /ci)
		payload := map[string]any{
			"user_id":       admin.User.ID,
			"name":          "CI-Builder",
			"allowed_paths": models.StringList{"/cis"},
		}
		body, _ := json.Marshal(payload)

		w := Perform(t, router, "POST", "/_/api/admin/tokens", WithBody(body), WithSession(admin))
		assert.Equal(t, 201, w.Code)

		var resp map[string]any
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		plainToken := resp["plain_token"].(string)

		os.MkdirAll(filepath.Join(baseDir, "ci"), 0755)
		os.WriteFile(filepath.Join(baseDir, "ci", "data.zip"), []byte("..."), 0644)

		// 2. Attempt to move from /ci to /prod
		w = Perform(t, router, "POST", "/_/api/v1/fs/ci/data.zip",
			WithHeader("X-API-Token", plainToken),
			WithJSON(map[string]any{"move_to": "/prod/data.zip"}),
		)

		// Should fail because /prod is not in bot's allowed_paths
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, "ci", "data.zip"), "File should not have moved")
	})
}
