package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestStreamLogic(t *testing.T) {

	// Seed data
	s1 := "prod-builds"
	s2 := "dev-builds"
	g1 := "v1.0"
	g2 := "v1.1"

	db.Create(&api.MetaResource{Path: "/p1", Stream: &s1, Group: &g1})
	db.Create(&api.MetaResource{Path: "/p2/f2", Stream: &s1, Group: &g1}) // Same group
	db.Create(&api.MetaResource{Path: "/p3", Stream: &s1, Group: &g2})    // Different group
	db.Create(&api.MetaResource{Path: "/d1", Stream: &s2, Group: &g1})    // Different stream

	t.Run("List Unique Streams", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/streams", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var streams []string
		json.Unmarshal(w.Body.Bytes(), &streams)

		assert.Contains(t, streams, "prod-builds")
		assert.Contains(t, streams, "dev-builds")
	})

	t.Run("Get Stream Groups and Files", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/streams/prod-builds", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var groups []api.GroupInfo
		json.Unmarshal(w.Body.Bytes(), &groups)

		// Should have 2 groups: v1.1 (latest) and v1.0
		assert.Equal(t, 2, len(groups), w.Body.String())

		// Check v1.0 (should have 2 files)
		for _, g := range groups {
			if g.Name == "v1.0" {
				assert.Equal(t, 2, len(g.Files))
				assert.Equal(t, "/p1", g.Files[0].Name)
				assert.Equal(t, "/p2/f2", g.Files[1].Name, "full path should be in the response")
			}
		}
	})
}
