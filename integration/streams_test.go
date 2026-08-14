package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestStreamLogic(t *testing.T) {
	s1 := "prod-builds"
	s2 := "dev-builds"
	g1 := "v1.0"
	g2 := "v1.1"

	createGroupedResource(db, "/p1", s1, g1)
	createGroupedResource(db, "/p2/f2", s1, g1) // Same group
	createGroupedResource(db, "/p3", s1, g2)    // Different group
	createGroupedResource(db, "/d1", s2, g1)    // Different stream

	t.Run("List Unique Streams", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/streams", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var streams []string
		json.Unmarshal(w.Body.Bytes(), &streams)

		assert.Contains(t, streams, "prod-builds")
		assert.Contains(t, streams, "dev-builds")
	})

	t.Run("Stream name containing slash round-trips via escaped path", func(t *testing.T) {
		slashStream := "team/alpha"
		createGroupedResource(db, "/s1", slashStream, "g1")

		// %2F must survive routing and reach the :name handler decoded.
		req, _ := http.NewRequest(
			"GET",
			"/_/api/v1/streams/"+url.PathEscape(slashStream),
			nil,
		)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code, w.Body.String())

		var details api.StreamDetailsResponse
		json.Unmarshal(w.Body.Bytes(), &details)
		assert.Equal(t, 1, len(details.Groups), w.Body.String())
	})

	t.Run("Get Stream Groups and Files", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/streams/prod-builds", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var details api.StreamDetailsResponse
		json.Unmarshal(w.Body.Bytes(), &details)

		// Should have 2 groups: v1.1 (latest) and v1.0
		assert.Equal(t, 2, len(details.Groups), w.Body.String())

		// Check v1.0 (should have 2 files)
		for _, g := range details.Groups {
			if g.Name == "v1.0" {
				assert.Equal(t, 2, len(g.Files))
				assert.Equal(t, "/p1", g.Files[0].Path)
				assert.Equal(t, "/p2/f2", g.Files[1].Path, "full path should be in the response")
			}
		}
	})
}
