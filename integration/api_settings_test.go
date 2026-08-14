package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GET /_/api/v1/settings used to be fully anonymous and returned the whole
// config (storage paths, DB filename), the commit SHA and a dependency SBOM with
// versions — a ready-made vulnerability-matching list. It is now authenticated,
// and the detailed fields are admin-only.
func TestSettingsAPI(t *testing.T) {
	admin := PrepareAuth(t, db, "settings-admin", true, nil, AuthH.Config.Server.JwtSecret)
	plain := PrepareAuth(t, db, "settings-plain-user", false, &models.StringList{"/"}, AuthH.Config.Server.JwtSecret)

	getSettings := func(t *testing.T, opts ...RequestOption) map[string]any {
		t.Helper()
		w := Perform(t, router, "GET", "/_/api/v1/settings", opts...)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp
	}

	t.Run("Anonymous is rejected", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/settings")
		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	})

	t.Run("Admin gets valid settings and config", func(t *testing.T) {
		resp := getSettings(t, WithSession(admin))

		assert.Equal(t, "dev", resp["version"])
		// Check if nested config exists
		configMap := resp["config"].(map[string]any)
		storage := configMap["storage"].(map[string]any)
		assert.Equal(t, "100MB", storage["max_upload_size"])

		assert.Contains(t, resp, "dependencies")
		assert.Contains(t, resp, "commit")
		assert.Contains(t, resp, "system_states")
	})

	t.Run("Non-admin gets metrics but no config or SBOM", func(t *testing.T) {
		resp := getSettings(t, WithSession(plain))

		// The operational view the UI's System tab renders stays available.
		assert.Equal(t, "dev", resp["version"])
		assert.Contains(t, resp, "uptime_seconds")
		assert.Contains(t, resp, "runtime")
		assert.Contains(t, resp, "storage")
		assert.Contains(t, resp, "db_size")

		// The sensitive detail does not.
		assert.NotContains(t, resp, "config", "storage paths and DB filename are admin-only")
		assert.NotContains(t, resp, "dependencies", "the SBOM is admin-only")
		assert.NotContains(t, resp, "commit", "the exact commit is admin-only")
		assert.NotContains(t, resp, "system_states")
	})
}
