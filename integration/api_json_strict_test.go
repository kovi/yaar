package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONStrictness(t *testing.T) {
	session := PrepareAuth(t, db, "json_tester", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("PATCH meta with unknown field returns 400", func(t *testing.T) {
		// Valid patch has "content_type", but we send "unknown_field"
		payload := map[string]any{
			"unknown_field": "invalid",
		}
		w := Perform(t, router, http.MethodPatch, "/_/api/v1/fs/some-file.txt", WithSession(session), WithJSON(payload))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "unknown field")
	})

	t.Run("POST stream with unknown field returns 400", func(t *testing.T) {
		payload := map[string]any{
			"unknown_field": "invalid",
		}
		w := Perform(t, router, http.MethodPut, "/_/api/v1/streams/my-stream", WithSession(session), WithJSON(payload))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "unknown field")
	})
}
