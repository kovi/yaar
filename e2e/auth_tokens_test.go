package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestTokenManagementAndScoping(t *testing.T) {
	adminSession := PrepareAuth(t, db, "token-admin", true, nil, AuthH.Config.Server.JwtSecret)

	var plainToken string
	var tokenId uint

	t.Run("Step 1: Admin Creates a Scoped Token", func(t *testing.T) {
		payload := map[string]any{
			"user_id":       adminSession.User.ID,
			"name":          "CI-Builder",
			"allowed_paths": []string{"/ci-artifacts"},
		}
		w := Perform(t, router, "POST", "/_/api/admin/tokens", WithJSON(payload), WithSession(adminSession))

		assert.Equal(t, 201, w.Code, w.Body.String())

		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)

		plainToken = resp["plain_token"].(string)
		tokenId = uint(resp["id"].(float64))

		assert.NotEmpty(t, plainToken)
		assert.Contains(t, plainToken, "af_") // Check prefix
	})

	t.Run("Step 2: Use Token inside Allowed Scope (Upload)", func(t *testing.T) {
		req, _ := http.NewRequest("PUT", "/ci-artifacts/build.zip", bytes.NewBuffer([]byte("data")))
		req.Header.Set("X-API-Token", plainToken)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Should be allowed (201 Created or 200 OK)
		assert.Contains(t, []int{200, 201}, w.Code)
	})

	t.Run("Step 3: Use Token outside Allowed Scope (Delete)", func(t *testing.T) {
		path := "/production/app.exe"
		w := Perform(t, router, "PUT", path, WithBody([]byte("data")), WithSession(adminSession))
		assert.Equal(t, 200, w.Code)

		w = Perform(t, router, "DELETE", path, WithHeader("X-API-Token", plainToken))

		assert.Equal(t, 403, w.Code)
	})

	t.Run("Step 4: Admin Revokes the Token", func(t *testing.T) {
		url := fmt.Sprintf("/_/api/admin/tokens/%d", tokenId)
		req, _ := http.NewRequest("DELETE", url, nil)
		adminSession.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 204, w.Code)
	})

	t.Run("Step 5: Revoked Token no longer works", func(t *testing.T) {
		w := Perform(t, router, "PUT", "/ci-artifacts/new.zip", WithBody([]byte("data")), WithHeader("X-API_Token", plainToken))

		// Should be 401 because the hash is gone from DB
		assert.Equal(t, 401, w.Code)
	})
}

func TestTokenExpiry(t *testing.T) {

	user := models.User{Username: "bot"}
	db.Create(&user)

	t.Run("Reject expired token", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		token := models.Token{
			UserID:     user.ID,
			SecretHash: auth.HashToken("af_expired"),
			ExpiresAt:  &past,
		}
		db.Create(&token)

		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-API-Token", "af_expired")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 401, w.Code)
	})

	t.Run("Accept token with future expiry", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		token := models.Token{
			UserID:     user.ID,
			SecretHash: auth.HashToken("af_future"),
			ExpiresAt:  &future,
		}
		db.Create(&token)

		req, _ := http.NewRequest("GET", "/test", nil)
		req.Header.Set("X-API-Token", "af_future")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 404, w.Code)
	})
}
