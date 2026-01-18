package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/models"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestAuthAndUserManagement(t *testing.T) {
	session := PrepareAuth(t, db, "adminer", true, AuthH.Config.Server.JwtSecret)
	worker := PrepareAuth(t, db, "worker", false, AuthH.Config.Server.JwtSecret)

	t.Run("Successful Login returns JWT", func(t *testing.T) {
		body := map[string]string{"username": session.User.Username, "password": session.PlainPassword}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("POST", "/_/api/login", bytes.NewBuffer(jsonBody))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)

		assert.NotNil(t, resp["token"])
	})

	t.Run("Failed Login with wrong password", func(t *testing.T) {
		body := map[string]string{"username": "boss", "password": "wrong-password"}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("POST", "/_/api/login", bytes.NewBuffer(jsonBody))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 401, w.Code)
	})

	t.Run("Access /me with valid JWT", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/auth/me", nil)
		session.Apply(req)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code, w.Body.String())
		var resp map[string]any
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, session.User.Username, resp["username"])
		assert.Equal(t, session.User.ID, uint(resp["id"].(float64)))
		assert.True(t, resp["is_admin"].(bool))
	})

	t.Run("Access /me with invalid JWT", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/auth/me", nil)
		req.Header.Set("Authorization", "Bearer invalidtoken")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 401, w.Code, w.Body.String())
	})

	t.Run("Create a new user as Admin", func(t *testing.T) {
		body := map[string]any{"username": "workerx", "password": "pass", "is_admin": false}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("POST", "/_/api/admin/users", bytes.NewBuffer(jsonBody))
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 201, w.Code)

		// Verify in DB
		var worker models.User
		db.Where("username = ?", "workerx").First(&worker)
		assert.Equal(t, "workerx", worker.Username)
	})

	t.Run("Non-Admin cannot list users", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/admin/users", nil)
		worker.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Assert 403 Forbidden
		assert.Equal(t, 403, w.Code)
	})

	t.Run("Admin cannot delete themselves", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/_/api/admin/users/%v", session.User.ID), nil)
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 403, w.Code)

		// Verify user still exists
		var count int64
		db.Model(&models.User{}).Where("id = ?", session.User.ID).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("Admin can reset someone else's password", func(t *testing.T) {
		// 1. Create a user to reset
		victim := models.User{Username: "forgetful"}
		victim.SetPassword("old-pass")
		db.Create(&victim)

		// 2. Perform reset
		newPass := "shiny-new-password"
		body := map[string]any{"password": newPass}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("PATCH", fmt.Sprintf("/_/api/admin/users/%d", victim.ID), bytes.NewBuffer(jsonBody))
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)

		// 3. Verify login with NEW password
		db.First(&victim, victim.ID)
		assert.True(t, victim.CheckPassword(newPass))
		assert.False(t, victim.CheckPassword("old-pass"))
	})
}

func TestUserCacheIntegration(t *testing.T) {

	// Create a test user
	user := models.User{Username: "cache-user"}
	user.SetPassword("password")
	db.Create(&user)
	token, _ := auth.GenerateToken(user, Meta.Config.Server.JwtSecret)

	t.Run("First request populates cache", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/_/api/auth/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code)

		// Verify item is now in cache
		exists, _, found := AuthH.UserCache.Get(user.ID)
		assert.True(t, found)
		assert.True(t, exists)
	})

	t.Run("Middleware uses cache when DB record is gone", func(t *testing.T) {
		// DELETE user from real database
		logrus.Infof("delete user: %v", user)
		db.Unscoped().Delete(&user)

		// Request should still succeed because of the 2-minute cache TTL
		req, _ := http.NewRequest("GET", "/_/api/auth/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 200, w.Code, "Should succeed despite missing DB record (cached hit)")
	})

	t.Run("Invalidating cache forces DB check and fails", func(t *testing.T) {
		// Manually invalidate the cache entry for this user
		AuthH.UserCache.Invalidate(user.ID)

		// Now request should fail because user is missing from DB and Cache
		req, _ := http.NewRequest("GET", "/_/api/auth/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, 401, w.Code, "Should fail after cache invalidation")

		// Verify cache now stores the 'false' (non-existent) state to prevent DB spamming
		exists, _, found := AuthH.UserCache.Get(user.ID)
		assert.True(t, found)
		assert.False(t, exists)
	})

	t.Run("Admin status change reflects via Invalidation", func(t *testing.T) {
		// 1. Re-create user as non-admin
		newUser := models.User{Username: "temp-admin", IsAdmin: false}
		db.Create(&newUser)
		log.Printf("create user: %v", newUser)
		token2, _ := auth.GenerateToken(newUser, AuthH.Config.Server.JwtSecret)

		// 2. Populate cache (as non-admin)
		req, _ := http.NewRequest("GET", "/test-auth", nil)
		req.Header.Set("Authorization", "Bearer "+token2)
		router.ServeHTTP(httptest.NewRecorder(), req)

		// 3. Promote user to Admin in DB
		db.Model(&newUser).Update("is_admin", true)

		// 4. Invalidate cache to force update
		AuthH.UserCache.Invalidate(newUser.ID)

		// 5. Verify middleware sees the new Admin status
		req2, _ := http.NewRequest("GET", "/_/api/admin/users", nil)
		req2.Header.Set("Authorization", "Bearer "+token2)
		w2 := httptest.NewRecorder()
		router.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusOK, w2.Code)
	})
}
