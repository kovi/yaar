package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSettingsAPI(t *testing.T) {

	t.Run("Get valid settings and config", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/v1/settings", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)

		assert.Equal(t, "dev", resp["version"])
		// Check if nested config exists
		configMap := resp["config"].(map[string]any)
		storage := configMap["Storage"].(map[string]any)
		assert.Equal(t, "100MB", storage["MaxUploadSize"])
	})
}
