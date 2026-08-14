package integration

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
	"github.com/sirupsen/logrus"
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
		w := Perform(t, router, "POST", "/_/api/tokens", WithJSON(payload), WithSession(adminSession))

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
		req.Header.Set("Authorization", "Bearer "+plainToken)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Should be allowed (201 Created or 200 OK)
		assert.Contains(t, []int{200, 201}, w.Code)
	})

	t.Run("Step 3: Use Token outside Allowed Scope (Delete)", func(t *testing.T) {
		path := "/production/app.exe"
		w := Perform(t, router, "PUT", path, WithBody([]byte("data")), WithSession(adminSession))
		assert.Equal(t, 200, w.Code)

		w = Perform(t, router, "DELETE", path, WithToken(plainToken))

		assert.Equal(t, 403, w.Code)
	})

	t.Run("Step 4: Admin Revokes the Token", func(t *testing.T) {
		url := fmt.Sprintf("/_/api/tokens/%d", tokenId)
		req, _ := http.NewRequest("DELETE", url, nil)
		adminSession.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 204, w.Code)
	})

	t.Run("Step 5: Revoked Token no longer works", func(t *testing.T) {
		w := Perform(t, router, "PUT", "/ci-artifacts/new.zip", WithBody([]byte("data")), WithToken(plainToken))

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
		req.Header.Set("Authorization", "Bearer "+"af_expired")
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
		req.Header.Set("Authorization", "Bearer "+"af_future")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 404, w.Code)
	})
}

func TestTokenSelfService(t *testing.T) {
	ClearDatabase(db)

	// Create Users
	admin := PrepareAuth(t, db, "admin-t", true, nil, AuthH.Config.Server.JwtSecret)
	userA := PrepareAuth(t, db, "user-a", false, nil, AuthH.Config.Server.JwtSecret)
	userB := PrepareAuth(t, db, "user-b", false, nil, AuthH.Config.Server.JwtSecret)

	// Pre-seed a token for User B
	tokenB := models.Token{UserID: userB.User.ID, Name: "B-Secret", SecretHash: "hash"}
	db.Create(&tokenB)
	logrus.Infof("Created token: %v", tokenB)

	t.Run("User A sees only their own empty list", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/tokens", WithSession(userA))

		assert.Equal(t, 200, w.Code)
		var tokens []models.Token
		json.Unmarshal(w.Body.Bytes(), &tokens)

		// User A has 0 tokens, shouldn't see User B's token
		assert.Equal(t, 0, len(tokens))
	})

	t.Run("Admin sees all tokens automatically", func(t *testing.T) {
		w := Perform(t, router, "GET", "/_/api/tokens", WithSession(admin))

		assert.Equal(t, 200, w.Code)
		var tokens []models.Token
		json.Unmarshal(w.Body.Bytes(), &tokens)

		// Admin should see at least User B's token
		assert.GreaterOrEqual(t, len(tokens), 1)
		found := false
		for _, tk := range tokens {
			if tk.UserID == userB.User.ID {
				found = true
			}
		}
		assert.True(t, found, "Admin should see User B's token")
	})

	t.Run("User A creates a token for themselves", func(t *testing.T) {
		w := Perform(t, router, "POST", "/_/api/tokens",
			WithSession(userA),
			WithJSON(map[string]any{
				"name":          "A-Token",
				"allowed_paths": []string{"/a"},
			}),
		)

		assert.Equal(t, 201, w.Code)
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)

		// Verify DB assignment
		var check models.Token
		db.First(&check, uint(resp["id"].(float64)))
		assert.Equal(t, userA.User.ID, check.UserID, "Token must be owned by User A")
	})

	t.Run("User A cannot spoof ownership to User B", func(t *testing.T) {
		// Attempt to create a token but pass User B's ID in JSON
		w := Perform(t, router, "POST", "/_/api/tokens",
			WithSession(userA),
			WithJSON(map[string]any{
				"name":    "I-Am-Stealing",
				"user_id": userB.User.ID, // Spoof attempt
			}),
		)

		assert.Equal(t, 201, w.Code)
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)

		var check models.Token
		db.First(&check, uint(resp["id"].(float64)))
		// Logic check: targetUserID := currentUserID unless Admin
		assert.Equal(t, userA.User.ID, check.UserID, "System must ignore user_id spoofing from non-admins")
	})

	t.Run("User A cannot delete User B token", func(t *testing.T) {
		url := fmt.Sprintf("/_/api/tokens/%d", tokenB.ID)
		w := Perform(t, router, "DELETE", url, WithSession(userA))

		assert.Equal(t, 403, w.Code, "Should be forbidden to delete someone else's token")

		// Verify token still exists
		var check models.Token
		assert.NoError(t, db.First(&check, tokenB.ID).Error)
	})

	t.Run("Admin can delete User B token", func(t *testing.T) {
		url := fmt.Sprintf("/_/api/tokens/%d", tokenB.ID)
		w := Perform(t, router, "DELETE", url, WithSession(admin))

		assert.Equal(t, 204, w.Code)

		// Verify token is gone
		var check models.Token
		r := db.Limit(1).Find(&check, tokenB.ID)
		assert.NoError(t, r.Error)
		assert.EqualValues(t, 0, r.RowsAffected)
	})
}
