package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuthUploadWorkflows(t *testing.T) {
	admin := PrepareAuth(t, db, "admin_creator", true, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Create User via API and upload file", func(t *testing.T) {
		// 1. Create User
		userPayload := map[string]any{
			"username":      "new_api_user",
			"password":      "strongpassword123",
			"allowed_paths": []string{"/userdir"},
			"is_admin":      false,
		}
		w := Perform(t, router, http.MethodPost, "/_/api/admin/users", WithSession(admin), WithJSON(userPayload))
		assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())

		// 2. Login to get token
		loginPayload := map[string]any{
			"username": "new_api_user",
			"password": "strongpassword123",
		}
		wLogin := Perform(t, router, http.MethodPost, "/_/api/login", WithJSON(loginPayload))
		assert.Equal(t, http.StatusOK, wLogin.Code)

		var loginData map[string]any
		json.Unmarshal(wLogin.Body.Bytes(), &loginData)
		jwtToken := loginData["token"].(string)

		// 3. Upload file with user's JWT
		req, _ := http.NewRequest(http.MethodPut, "/userdir/test.txt", bytes.NewBuffer([]byte("hello user")))
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		wUpload := httptest.NewRecorder()
		router.ServeHTTP(wUpload, req)

		assert.Equal(t, http.StatusOK, wUpload.Code)
	})

	t.Run("Create Token via API and upload file", func(t *testing.T) {
		// 1. Create Token
		tokenPayload := map[string]any{
			"name":          "UploadBot",
			"allowed_paths": []string{"/tokendir"},
		}
		// Admin creates token for themselves
		w := Perform(t, router, http.MethodPost, "/_/api/tokens", WithSession(admin), WithJSON(tokenPayload))
		assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())

		var tokenData map[string]any
		json.Unmarshal(w.Body.Bytes(), &tokenData)
		apiToken := tokenData["plain_token"].(string)

		// 2. Upload file with Token
		req, _ := http.NewRequest(http.MethodPut, "/tokendir/test.txt", bytes.NewBuffer([]byte("hello token")))
		req.Header.Set("Authorization", "Bearer "+apiToken)
		wUpload := httptest.NewRecorder()
		router.ServeHTTP(wUpload, req)

		assert.Equal(t, http.StatusOK, wUpload.Code)
	})
}
