package e2e

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
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
		expiryStr := expiry.Format("2006-01-02 15:04:05")
		log.Printf("expiry: %v", expiryStr)
		tags := "env=patchtest; arch=x64"
		stream := "production-stream/group1"

		payload := api.MetaPatchRequest{
			ExpiresAt: &expiryStr,
			Tags:      &tags,
			Stream:    &stream,
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
		err = db.Preload("Tags").Where("path = ?", "/"+fileName).First(&meta).Error
		assert.NoError(t, err)

		assert.Equal(t, "production-stream", *meta.Stream)
		assert.Equal(t, "group1", *meta.Group)
		assert.True(t, meta.ExpiresAt.Equal(expiry))
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
}
