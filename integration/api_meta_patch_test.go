package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
)

func TestPatchMeta(t *testing.T) {
	session := PrepareAuth(t, db, "metapatcher1", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("404 when physical file is missing", func(t *testing.T) {
		targetPath := "/ghost-file.txt"
		payload := map[string]any{
			"tags": "some-tag",
		}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest(http.MethodPatch, "/_/api/v1/fs"+targetPath, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Assert that even if metadata logic exists, we return 404 because file is gone
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("Successfully patch metadata for existing file", func(t *testing.T) {
		// 1. Create the physical file
		fileName := "real-file.bin"
		filePath := filepath.Join(baseDir, fileName)
		err := os.WriteFile(filePath, []byte("data"), 0644)
		assert.NoError(t, err)

		// 2. Prepare Patch Payload
		expiry := time.Now().Add(24 * time.Hour).Truncate(time.Second)
		expiryStr := expiry.Format("2006-01-02 15:04:05 -0700") // include tz or will be considered UTC
		log.Printf("expiry: %v", expiryStr)
		tags := "env=patchtest; arch=x64"

		payload := api.MetaPatchRequest{
			Retention: &api.RetentionPolicyPatch{Expires: &api.ExpiresPatch{At: &expiryStr}},
			Tags:      &tags,
			Stream:    ptr.Of("production-stream"),
			Group:     ptr.Of("group1"),
		}
		body, _ := json.Marshal(payload)

		// 3. Execute Request
		req, _ := http.NewRequest(http.MethodPatch, "/_/api/v1/fs/"+fileName, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// 4. Assert Status 200
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

		// 5. Verify Database State
		var meta api.MetaResource
		// Query DB directly to ensure persistence
		err = db.Preload("Tags").Preload("Group.Stream").Where("path = ?", "/"+fileName).First(&meta).Error
		assert.NoError(t, err)

		assert.Equal(t, "production-stream", meta.Group.Stream.Name)
		assert.Equal(t, "group1", meta.Group.Name)
		assert.Equal(t, expiry.UTC(), meta.ExpiryAt.UTC())
		assert.Len(t, meta.Tags, 2)

		// Verify individual tags
		tagMap := make(map[string]string)
		for _, tt := range meta.Tags {
			tagMap[tt.Key] = tt.Value
		}
		assert.Equal(t, "patchtest", tagMap["env"])
		assert.Equal(t, "x64", tagMap["arch"])
	})

	t.Run("403 Forbidden when patching immutable resource", func(t *testing.T) {
		fileName := "locked-file.txt"
		os.WriteFile(filepath.Join(baseDir, fileName), []byte("locked"), 0644)

		// Pre-create an immutable record in DB
		isImmutable := true
		db.Create(&api.MetaResource{
			Path:      "/" + fileName,
			Immutable: &isImmutable,
		})

		payload := map[string]any{"tags": "new-tag"}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest(http.MethodPatch, "/_/api/v1/fs/"+fileName, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("Patch directory metadata and verify physical stats are returned", func(t *testing.T) {
		// 1. Create a parent directory and a subdirectory
		parentDir := "parent-patch-test"
		subDir := "sub-dir"
		parentPath := filepath.Join(baseDir, parentDir)
		subDirPath := filepath.Join(parentPath, subDir)
		assert.NoError(t, os.MkdirAll(subDirPath, 0755))

		// 2. Patch the directory to assign a stream
		payload := api.MetaPatchRequest{
			Stream: ptr.Of("dir-stream"),
			Group:  ptr.Of("dir-group"),
		}
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest(http.MethodPatch, "/_/api/v1/fs/"+parentDir+"/"+subDir, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// 3. Verify response from patch has non-zero modTime and physical size
		var patchResp api.ResourceResponse
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &patchResp))
		assert.NotEmpty(t, patchResp.ModTime)
		assert.NotEqual(t, time.Time{}, patchResp.ModTime)
		assert.NotEqual(t, "0001-01-01T00:00:00Z", patchResp.ModTime.Format(time.RFC3339))

		// 4. Query parent directory and verify the listing entry has correct modTime and size
		reqList, _ := http.NewRequest(http.MethodGet, "/_/api/v1/fs/"+parentDir, nil)
		session.Apply(reqList)
		wList := httptest.NewRecorder()
		router.ServeHTTP(wList, reqList)
		assert.Equal(t, http.StatusOK, wList.Code)

		var listResp []api.ResourceResponse
		assert.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listResp))
		assert.Len(t, listResp, 1)
		assert.Equal(t, "/"+parentDir+"/"+subDir, listResp[0].Path)
		assert.NotEmpty(t, listResp[0].ModTime)
		assert.NotEqual(t, time.Time{}, listResp[0].ModTime)
		assert.NotEqual(t, "0001-01-01T00:00:00Z", listResp[0].ModTime.Format(time.RFC3339))

		// 5. Query streams details endpoint and verify the member directory has correct modTime
		reqStream, _ := http.NewRequest(http.MethodGet, "/_/api/v1/streams/dir-stream", nil)
		session.Apply(reqStream)
		wStream := httptest.NewRecorder()
		router.ServeHTTP(wStream, reqStream)
		assert.Equal(t, http.StatusOK, wStream.Code)

		var streamResp struct {
			Groups []struct {
				Name  string                 `json:"name"`
				Files []api.ResourceResponse `json:"files"`
			} `json:"groups"`
		}
		assert.NoError(t, json.Unmarshal(wStream.Body.Bytes(), &streamResp))
		assert.Len(t, streamResp.Groups, 1)
		assert.Len(t, streamResp.Groups[0].Files, 1)
		assert.Equal(t, "/"+parentDir+"/"+subDir, streamResp.Groups[0].Files[0].Path)
		assert.NotEmpty(t, streamResp.Groups[0].Files[0].ModTime)
		assert.NotEqual(t, time.Time{}, streamResp.Groups[0].Files[0].ModTime)
		assert.NotEqual(t, "0001-01-01T00:00:00Z", streamResp.Groups[0].Files[0].ModTime.Format(time.RFC3339))

		// 6. Change physical modTime on disk for subdirectory and run sync to verify it updates in DB
		originalModTime := listResp[0].ModTime
		newTime := originalModTime.Add(-10 * time.Hour).Truncate(time.Second)
		assert.NoError(t, os.Chtimes(subDirPath, newTime, newTime))

		// Trigger filesystem sync
		Meta.SyncFilesystem(context.Background())

		// Query parent directory again and verify the listing entry has updated modTime
		reqList2, _ := http.NewRequest(http.MethodGet, "/_/api/v1/fs/"+parentDir, nil)
		session.Apply(reqList2)
		wList2 := httptest.NewRecorder()
		router.ServeHTTP(wList2, reqList2)
		assert.Equal(t, http.StatusOK, wList2.Code)

		var listResp2 []api.ResourceResponse
		assert.NoError(t, json.Unmarshal(wList2.Body.Bytes(), &listResp2))
		assert.Len(t, listResp2, 1)
		assert.Equal(t, "/"+parentDir+"/"+subDir, listResp2[0].Path)
		assert.NotEmpty(t, listResp2[0].ModTime)
		assert.Equal(t, newTime.Unix(), listResp2[0].ModTime.Unix())
	})
}
