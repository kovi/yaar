package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestFilesystemOperations(t *testing.T) {

	session := PrepareAuth(t, db, "developer", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Create Directory", func(t *testing.T) {
		payload := map[string]any{"create_dir": true}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest("POST", "/_/api/v1/fs/folder-a/sub-folder", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.DirExists(t, filepath.Join(baseDir, "folder-a", "sub-folder"))
	})

	t.Run("Rename File and Update Meta", func(t *testing.T) {
		// 1. Setup file and DB record
		oldPath := "renamedir/old-file.txt"
		newPath := "renamedir/new-file.txt"
		os.MkdirAll(filepath.Join(baseDir, "renamedir"), 0755)
		assert.NoError(t, os.WriteFile(filepath.Join(baseDir, oldPath), []byte("hello"), 0644))
		db.Create(&api.MetaResource{Path: "/" + oldPath})

		// test that cannot be moved to another directory
		payload := map[string]string{"rename_to": "../new-file.txt"}
		w := Perform(t, router, "POST", "/_/api/v1/fs/"+oldPath, WithJSON(payload), WithSession(session))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, oldPath))
		assert.NoFileExists(t, filepath.Join(baseDir, "renamedir/../new-file.txt"))

		payload = map[string]string{"rename_to": "new-file.txt"}
		w = Perform(t, router, "POST", "/_/api/v1/fs/"+oldPath, WithJSON(payload), WithSession(session))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, newPath))
		assert.NoFileExists(t, filepath.Join(baseDir, oldPath))

		var meta api.MetaResource
		err := db.Where("path = ?", "/"+newPath).First(&meta).Error
		assert.NoError(t, err, "Database record should be updated to new path")
	})

	t.Run("Rename Directory Recursively", func(t *testing.T) {
		// 1. Setup structure: /old-dir/file1.txt and /old-dir/subdir/file2.txt"
		oldDir := "old-dir"
		newDir := "new-dir"

		os.MkdirAll(filepath.Join(baseDir, oldDir, "subdir"), 0755)
		os.WriteFile(filepath.Join(baseDir, oldDir, "file1.txt"), []byte("1"), 0644)
		os.WriteFile(filepath.Join(baseDir, oldDir, "subdir", "file2.txt"), []byte("2"), 0644)

		// 2. Create metadata records
		db.Create(&api.MetaResource{Path: "/" + oldDir})
		db.Create(&api.MetaResource{Path: "/" + oldDir + "/file1.txt"})
		db.Create(&api.MetaResource{Path: "/" + oldDir + "/subdir/file2.txt"})

		// 3. Execute Rename API
		payload := map[string]string{"rename_to": newDir}
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "/_/api/v1/fs/"+oldDir, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// 4. Filesystem Assertions
		assert.Equal(t, http.StatusOK, w.Code)
		assert.DirExists(t, filepath.Join(baseDir, newDir, "subdir"))
		assert.FileExists(t, filepath.Join(baseDir, newDir, "file1.txt"))
		assert.FileExists(t, filepath.Join(baseDir, newDir, "subdir", "file2.txt"))

		// 5. Database Recursive Assertions
		var count int64
		db.Model(&api.MetaResource{}).Where("path LIKE ?", "/"+newDir+"%").Count(&count)
		assert.Equal(t, int64(3), count, "All 3 records (dir + 2 files) should have updated paths")

		// Verify specific child path
		var childMeta api.MetaResource
		err := db.Where("path = ?", "/"+newDir+"/subdir/file2.txt").First(&childMeta).Error
		assert.NoError(t, err, "Deeply nested file path should be updated correctly")

		// Verify old paths are gone
		db.Model(&api.MetaResource{}).Where("path LIKE ?", "/"+oldDir+"%").Count(&count)
		assert.Equal(t, int64(0), count, "No records should remain with the old path prefix")
	})

	t.Run("Successfully create a nested directory", func(t *testing.T) {
		// Define the path we want to create
		targetPath := "/projects/2025/new-folder"

		// 2. Prepare JSON body as per our "Differentiate" logic
		body := map[string]any{
			"create_dir": true,
		}
		jsonBody, _ := json.Marshal(body)

		req, err := http.NewRequest(http.MethodPost, "/_/api/v1/fs"+targetPath, bytes.NewBuffer(jsonBody))
		assert.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var response map[string]any
		err = json.Unmarshal(w.Body.Bytes(), &response)
		assert.NoError(t, err)
		assert.Equal(t, "created", response["status"])

		expectedDiskPath := filepath.Join(baseDir, "projects", "2025", "new-folder")
		info, err := os.Stat(expectedDiskPath)
		assert.NoError(t, err, "The directory should physically exist on disk")
		assert.True(t, info.IsDir(), "The path should be a directory, not a file")
	})

	t.Run("Fail when parent directory is not writable", func(t *testing.T) {
		// Create a read-only directory to test error handling
		readOnlyDir := filepath.Join(baseDir, "readonly")
		os.Mkdir(readOnlyDir, 0555)

		targetPath := "/readonly/subfolder"
		fsTargetPath := filepath.Join(baseDir, targetPath)
		body := map[string]any{"create_dir": true}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest(http.MethodPost, "/_/api/v1/fs"+targetPath, bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Assertions: 500 Internal Server Error expected
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoFileExists(t, fsTargetPath)
		assert.NoDirExists(t, fsTargetPath)
	})
}

func TestPatchMeta_UnlockImmutable(t *testing.T) {

	// 2. Create an Immutable file in DB and on Disk
	fileName := "locked.txt"
	urlPath := "/" + fileName
	os.WriteFile(filepath.Join(baseDir, fileName), []byte("content"), 0644)

	isTrue := true
	db.Create(&api.MetaResource{
		Path:      urlPath,
		Immutable: &isTrue,
	})

	session := PrepareAuth(t, db, "devimmut", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Reject tag update while still locked", func(t *testing.T) {
		tags := "new=tag"
		payload := api.MetaPatchRequest{Tags: &tags}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest("PATCH", "/_/api/v1/fs"+urlPath, bytes.NewBuffer(body))
		w := httptest.NewRecorder()
		session.Apply(req)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Successfully unlock the resource", func(t *testing.T) {
		isFalse := false
		// Request to set immutable to false
		payload := api.MetaPatchRequest{Immutable: &isFalse}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest("PATCH", "/_/api/v1/fs"+urlPath, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Assert Success
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify in Database
		var meta api.MetaResource
		db.Where("path = ?", urlPath).First(&meta)
		assert.NotNil(t, meta.Immutable)
		assert.False(t, *meta.Immutable, "Resource should now be unlocked")
	})

	t.Run("Allow tag update after unlocking", func(t *testing.T) {
		tags := "now=editable"
		payload := api.MetaPatchRequest{Tags: &tags}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest("PATCH", "/_/api/v1/fs"+urlPath, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify tags were saved
		var count int64
		db.Model(&api.MetaTag{}).Where("key = ?", "now").Count(&count)
		assert.Equal(t, int64(1), count)
	})
}
