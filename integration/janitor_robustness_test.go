package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestGetMeta_PreloadGroupStream(t *testing.T) {
	session := PrepareAuth(t, db, "meta_preload_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	stream := "builds"
	group := "v1.2.3"
	filePath := "/preload-test.txt"

	// Upload file with group and stream headers
	w := Perform(t, router, "PUT", filePath,
		WithSession(session),
		WithBody([]byte("test preloads")),
		WithHeader("Yaar-Stream", stream),
		WithHeader("Yaar-Group", group))
	assert.Equal(t, http.StatusOK, w.Code)

	// Fetch metadata via GET
	w = Perform(t, router, "GET", "/_/api/v1/fs"+filePath, WithSession(session))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp api.ResourceResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)

	// Assert stream and group are correctly preloaded and returned in response
	assert.NotNil(t, resp.Stream, "Stream should not be nil")
	assert.NotNil(t, resp.Group, "Group should not be nil")
	assert.Equal(t, stream, *resp.Stream)
	assert.Equal(t, group, *resp.Group)
}

func TestJanitor_RetainLatestOlderModification(t *testing.T) {
	session := PrepareAuth(t, db, "retain_latest_mod_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	stream := "mod-stream"

	// Create stream with RetainLatest=true
	w := Perform(t, router, "PUT", "/_/api/v1/streams/"+stream, WithSession(session), WithJSON(H{"retain_latest": true}))
	assert.Equal(t, http.StatusOK, w.Code)

	// 1. Upload v1 (will be older group)
	target1 := "/mod-stream/v1/app.jar"
	w = Perform(t, router, "PUT", target1,
		WithSession(session),
		WithBody([]byte("v1 content")),
		WithHeader("Yaar-Stream", stream),
		WithHeader("Yaar-Group", "v1"),
		WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
	assert.Equal(t, http.StatusOK, w.Code)

	time.Sleep(100 * time.Millisecond)

	// 2. Upload v2 (latest group) with a short expiry
	target2 := "/mod-stream/v2/app.jar"
	w = Perform(t, router, "PUT", target2,
		WithSession(session),
		WithBody([]byte("v2 content")),
		WithHeader("Yaar-Stream", stream),
		WithHeader("Yaar-Group", "v2"),
		WithHeader("Yaar-Retention-Expire-After-Upload", "1s"))
	assert.Equal(t, http.StatusOK, w.Code)

	// 3. Modify older group v1 by updating tags
	patchBody := `{"tags": "status=archived"}`
	w = Perform(t, router, "PATCH", "/_/api/v1/fs"+target1,
		WithSession(session),
		WithBody([]byte(patchBody)),
		WithHeader("Content-Type", "application/json"))
	assert.Equal(t, http.StatusOK, w.Code)

	// Wait for expiry time (1.5 seconds)
	time.Sleep(1500 * time.Millisecond)

	// Run cleanup
	Meta.RunCleanup()

	// v1 should be deleted (it is not latest, so it expired)
	var count1 int64
	db.Model(&api.MetaResource{}).Where("path = ?", target1).Count(&count1)
	assert.Equal(t, int64(0), count1, "v1 should be deleted")

	// v2 should STILL EXIST (it is the latest group, and should remain protected despite the v1 modification)
	var count2 int64
	db.Model(&api.MetaResource{}).Where("path = ?", target2).Count(&count2)
	assert.Equal(t, int64(1), count2, "v2 should be preserved (latest group)")
}

func TestJanitor_BackoffOnError(t *testing.T) {
	session := PrepareAuth(t, db, "backoff_user", true, nil, AuthH.Config.Server.JwtSecret) // Admin session
	ClearDatabase(db)

	dirName := "failed-prune-dir"
	fullDir := filepath.Join(Meta.BaseDir, dirName)
	os.MkdirAll(fullDir, 0755)

	// Create a child file inside the directory to make directory removal fail
	childPath := filepath.Join(fullDir, "child.txt")
	os.WriteFile(childPath, []byte("prevent delete"), 0644)

	past := time.Now().UTC().Add(-1 * time.Hour)

	// Insert directory resource with short expiry (it is a directory but we'll try to remove it via os.Remove which fails when not empty)
	res := api.MetaResource{
		Path:        "/" + dirName,
		Type:        api.ResourceTypeDir,
		DeleteAfter: &past,
	}
	db.Create(&res)

	// Verify DB record exists
	var check api.MetaResource
	db.Where("path = ?", "/"+dirName).Limit(1).Find(&check)
	assert.NotZero(t, check.ID)

	// Run cleanup -> this should fail to delete "/failed-prune-dir"
	Meta.RunCleanup()

	// DB record must NOT be deleted
	check = api.MetaResource{}
	db.Where("path = ?", "/"+dirName).Limit(1).Find(&check)
	assert.NotZero(t, check.ID, "DB record should remain intact on deletion failure")

	// Assert error is registered in in-memory registry
	w := Perform(t, router, "GET", "/_/api/system/janitor/errors", WithSession(session))
	assert.Equal(t, http.StatusOK, w.Code)

	var errors []api.CleanupFailure
	err := json.Unmarshal(w.Body.Bytes(), &errors)
	assert.NoError(t, err)

	assert.Len(t, errors, 1)
	assert.Equal(t, "file:/"+dirName, errors[0].Key)
	assert.Equal(t, "file", errors[0].Type)
	assert.Equal(t, 1, errors[0].Attempts)
	assert.Contains(t, errors[0].LastError, "directory not empty")

	// Fast-forward retry timestamp to verify retry logic
	func() {
		Meta.CleanupFailureTestAccess(t, "file:/"+dirName, func(f *api.CleanupFailure) {
			f.RetryAfter = time.Now().Add(-1 * time.Hour) // in the past
		})
	}()

	// Remove the child file so deletion succeeds next time
	os.Remove(childPath)

	// Run cleanup again
	Meta.RunCleanup()

	// DB record should now be deleted successfully
	var count int64
	db.Model(&api.MetaResource{}).Where("path = ?", "/"+dirName).Count(&count)
	assert.Zero(t, count, "DB record should be cleaned up on successful retry")

	// Registry should be cleared
	w = Perform(t, router, "GET", "/_/api/system/janitor/errors", WithSession(session))
	assert.Equal(t, http.StatusOK, w.Code)
	err = json.Unmarshal(w.Body.Bytes(), &errors)
	assert.NoError(t, err)
	assert.Len(t, errors, 0, "registry should be empty after success")
}
