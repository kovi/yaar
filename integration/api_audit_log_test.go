package integration

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditLogAPI(t *testing.T) {
	admin := PrepareAuth(t, db, "audit-admin", true, nil, AuthH.Config.Server.JwtSecret)
	worker := PrepareAuth(t, db, "audit-worker", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Unauthenticated request is rejected", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log")
		assert.Equal(t, 401, w.Code)
	})

	t.Run("Non-admin is forbidden", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log", WithSession(worker))
		assert.Equal(t, 403, w.Code)
	})

	t.Run("Returns valid page structure", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log", WithSession(admin))
		require.Equal(t, 200, w.Code)

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp, "entries")
		assert.Contains(t, resp, "next_before_offset")
		assert.Contains(t, resp, "scan_limit_hit")
		assert.IsType(t, []any{}, resp["entries"])
	})

	t.Run("Upload generates a visible audit entry", func(t *testing.T) {
		content := []byte("audit-log-e2e-test-content")
		Perform(t, router, "PUT", "/audit-e2e-marker.txt",
			WithRawReader(bytes.NewReader(content), int64(len(content))),
			WithSession(admin),
		)

		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log", WithSession(admin))
		require.Equal(t, 200, w.Code)

		var resp struct {
			Entries []map[string]any `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

		found := false
		for _, e := range resp.Entries {
			if e["action"] == "FILE_UPLOAD" && e["resource"] == "/audit-e2e-marker.txt" {
				found = true
				assert.Equal(t, "SUCCESS", e["status"])
				assert.Equal(t, admin.User.Username, e["user"])
				break
			}
		}
		assert.True(t, found, "expected FILE_UPLOAD entry for /audit-e2e-marker.txt in audit log")
	})

	t.Run("Filter returns only matching entries", func(t *testing.T) {
		// Filter on the unique marker path written above
		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log?filter=audit-e2e-marker", WithSession(admin))
		require.Equal(t, 200, w.Code)

		var resp struct {
			Entries []map[string]any `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.NotEmpty(t, resp.Entries)
		for _, e := range resp.Entries {
			resource, _ := e["resource"].(string)
			assert.Contains(t, resource, "audit-e2e-marker")
		}
	})

	t.Run("Limit is respected", func(t *testing.T) {
		// Upload a few more files so the log has multiple entries
		for i := 0; i < 3; i++ {
			content := []byte("x")
			Perform(t, router, "PUT", "/audit-limit-test.txt",
				WithRawReader(bytes.NewReader(content), int64(len(content))),
				WithSession(admin),
			)
		}

		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log?limit=2", WithSession(admin))
		require.Equal(t, 200, w.Code)

		var resp struct {
			Entries []map[string]any `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.LessOrEqual(t, len(resp.Entries), 2)
	})

	t.Run("Pagination via before_offset works", func(t *testing.T) {
		// First page
		w1 := Perform(t, router, "GET", "/_/api/v1/admin/audit-log?limit=2", WithSession(admin))
		require.Equal(t, 200, w1.Code)

		var page1 struct {
			Entries          []map[string]any `json:"entries"`
			NextBeforeOffset int64            `json:"next_before_offset"`
		}
		require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &page1))

		if page1.NextBeforeOffset < 0 {
			t.Skip("not enough audit entries for pagination test")
		}

		// Second page using the cursor
		w2 := Perform(t, router, "GET",
			"/_/api/v1/admin/audit-log?limit=2&before_offset="+
				jsonInt64(page1.NextBeforeOffset),
			WithSession(admin),
		)
		require.Equal(t, 200, w2.Code)

		var page2 struct {
			Entries []map[string]any `json:"entries"`
		}
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &page2))

		// No overlap: no request_id from page2 should appear in page1
		page1IDs := map[string]bool{}
		for _, e := range page1.Entries {
			if id, ok := e["request_id"].(string); ok {
				page1IDs[id] = true
			}
		}
		for _, e := range page2.Entries {
			if id, ok := e["request_id"].(string); ok && id != "internal" {
				assert.False(t, page1IDs[id], "entry with request_id %q appeared on both pages", id)
			}
		}
	})

	t.Run("Invalid before_offset returns 400", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log?before_offset=notanumber", WithSession(admin))
		assert.Equal(t, 400, w.Code)
	})

	t.Run("Invalid limit returns 400", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log?limit=bad", WithSession(admin))
		assert.Equal(t, 400, w.Code)
	})
}

func jsonInt64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
