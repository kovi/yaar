package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestSearchAPI(t *testing.T) {

	ClearDatabase(db)

	// 2. Pre-seed Diverse Test Data
	s1 := "production-stream"
	g1 := "v1.2.3"

	// Item A: Matches by Path & Filename
	db.Create(&api.MetaResource{
		Path: "/apps/downloader.exe",
		Type: api.ResourceTypeFile,
	})

	// Item B: Matches by Stream & Group
	db.Create(&api.MetaResource{
		Path:   "/builds/app.bin",
		Type:   api.ResourceTypeFile,
		Stream: &s1,
		Group:  &g1,
	})

	// Item C: Matches by Tags
	resourceC := api.MetaResource{
		Path: "/config/settings.yaml",
		Type: api.ResourceTypeFile,
	}
	db.Create(&resourceC)
	db.Create(&api.MetaTag{ResourceID: resourceC.ID, Key: "env", Value: "staging"})
	db.Create(&api.MetaTag{ResourceID: resourceC.ID, Key: "locked", Value: ""})

	t.Run("Match by Filename/Path", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/search?q=downl", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var results []api.SearchResult
		json.Unmarshal(w.Body.Bytes(), &results)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, results, 1)
		assert.Equal(t, "downloader.exe", results[0].Name)
		assert.Equal(t, "/apps/downloader.exe", results[0].Path)
	})

	t.Run("Match by Stream and check metadata fields", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/search?q=production", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var results []api.SearchResult
		json.Unmarshal(w.Body.Bytes(), &results)

		assert.NotEmpty(t, results)
		assert.Equal(t, "production-stream", results[0].Stream)
		assert.Equal(t, "v1.2.3", results[0].Group)
		assert.Equal(t, "/builds/app.bin", results[0].Path)
	})

	t.Run("Match by Tag Key/Value and check tags array", func(t *testing.T) {
		// Search for the tag value "staging"
		req, _ := http.NewRequest("GET", "/_/api/v1/search?q=staging", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var results []api.SearchResult
		json.Unmarshal(w.Body.Bytes(), &results)

		assert.Len(t, results, 1)
		assert.Equal(t, "/config/settings.yaml", results[0].Path)

		// Check that the returned item has all its tags for the preview
		assert.Contains(t, results[0].Tags, "env=staging")
		assert.Contains(t, results[0].Tags, "locked")
	})

	t.Run("Ignore short queries", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/search?q=a", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var results []api.SearchResult
		json.Unmarshal(w.Body.Bytes(), &results)

		assert.Equal(t, 200, w.Code)
		assert.Len(t, results, 0)
	})
}
