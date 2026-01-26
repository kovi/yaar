package e2e

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestFileUploads(t *testing.T) {
	testContent := []byte("artifactory test content 2025")

	// Helper to calculate expected hashes
	calcHashes := func(data []byte) (string, string, string) {
		m := md5.Sum(data)
		s1 := sha1.Sum(data)
		s256 := sha256.Sum256(data)
		return hex.EncodeToString(m[:]), hex.EncodeToString(s1[:]), hex.EncodeToString(s256[:])
	}
	expectedMD5, expectedSHA1, expectedSHA256 := calcHashes(testContent)

	session := PrepareAuth(t, db, "uploader1", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Raw Binary Upload (PUT)", func(t *testing.T) {
		targetURL := "/raw/test-file.txt"
		req, _ := http.NewRequest(http.MethodPut, targetURL, bytes.NewBuffer(testContent))
		req.Header.Set("Content-Type", "text/plain")
		session.Apply(req)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, "raw", "test-file.txt"))

		// Verify DB
		var meta api.MetaResource
		err := db.Where("path = ?", "/raw/test-file.txt").First(&meta).Error
		assert.NoError(t, err)
		assert.Equal(t, expectedSHA256, meta.SHA256)
		assert.Equal(t, expectedSHA1, meta.SHA1)
		assert.Equal(t, expectedMD5, meta.MD5)
		assert.Equal(t, "text/plain", meta.ContentType)
	})

	t.Run("Multipart Form Upload (POST)", func(t *testing.T) {
		// Prepare multipart form
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// Manually create the part headers
		h := make(textproto.MIMEHeader)
		// Content-Disposition must follow the standard form-data format
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "file", "form-uploaded.bin"))
		h.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(h)
		assert.NoError(t, err)
		part.Write(testContent)
		writer.Close()

		// Target URL is a directory
		targetURL := "/uploads/"
		req, _ := http.NewRequest(http.MethodPost, targetURL, body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, "uploads", "form-uploaded.bin"))

		var meta api.MetaResource
		err = db.Where("path = ?", "/uploads/form-uploaded.bin").First(&meta).Error
		assert.NoError(t, err)
		assert.Equal(t, expectedSHA256, meta.SHA256)
		assert.Equal(t, "image/png", meta.ContentType)
	})

	t.Run("Conflict on POST existing file", func(t *testing.T) {
		fileName := "conflict.txt"
		os.WriteFile(filepath.Join(baseDir, fileName), []byte("exist"), 0644)

		req, _ := http.NewRequest(http.MethodPost, "/"+fileName, bytes.NewBuffer(testContent))
		session.Apply(req)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("Overwrite on PUT existing file", func(t *testing.T) {
		fileName := "overwrite.txt"
		os.WriteFile(filepath.Join(baseDir, fileName), []byte("old data"), 0644)
		db.Create(&api.MetaResource{Path: "/" + fileName, SHA256: "old-hash"})

		req, _ := http.NewRequest(http.MethodPut, "/"+fileName, bytes.NewBuffer(testContent))
		session.Apply(req)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var meta api.MetaResource
		db.Where("path = ?", "/"+fileName).First(&meta)
		assert.Equal(t, expectedSHA256, meta.SHA256, "Hash should be updated to new content")
	})

	t.Run("Reject upload if path is immutable in DB", func(t *testing.T) {
		fileName := "locked-forever.txt"
		urlPath := "/locked-forever.txt"

		// 1. Pre-seed the filesystem
		os.WriteFile(filepath.Join(baseDir, fileName), []byte("original"), 0644)

		// 2. Pre-seed the DB with Immutable set to true
		isImmutable := true
		db.Create(&api.MetaResource{
			Path:      urlPath,
			Immutable: &isImmutable,
		})

		// 3. Attempt to overwrite via PUT
		req, _ := http.NewRequest(http.MethodPut, urlPath, bytes.NewBuffer([]byte("new content")))
		session.Apply(req)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Assert 403 Forbidden
		assert.Equal(t, http.StatusForbidden, w.Code)

		// 4. Verify filesystem was NOT changed
		content, _ := os.ReadFile(filepath.Join(baseDir, fileName))
		assert.Equal(t, "original", string(content))
	})
}

func TestUploadAndVerifyMetadataChecksums(t *testing.T) {
	session := PrepareAuth(t, db, "uploader2", false, nil, AuthH.Config.Server.JwtSecret)

	content := []byte("integrity-check-2025")

	m := md5.Sum(content)
	s1 := sha1.Sum(content)
	s256 := sha256.Sum256(content)

	expMD5 := hex.EncodeToString(m[:])
	expSHA1 := hex.EncodeToString(s1[:])
	expSHA256 := hex.EncodeToString(s256[:])

	targetPath := "/check/sum-test.bin"

	t.Run("Step 1: Upload File", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, targetPath, bytes.NewBuffer(content))
		req.Header.Set("Content-Type", "application/octet-stream")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Step 2: Verify Metadata JSON contains Checksums", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/_/api/v1/fs"+targetPath, nil)
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Parse Response
		var resp api.FileResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		assert.NoError(t, err, w.Body.String())

		// Assertions on the Metadata object
		assert.Equal(t, false, resp.IsDir)
		assert.Equal(t, expMD5, resp.ChecksumMD5, "MD5 mismatch")
		assert.Equal(t, expSHA1, resp.ChecksumSHA1, "SHA1 mismatch")
		assert.Equal(t, expSHA256, resp.ChecksumSHA256, "SHA256 mismatch")

		// headers on file get
		req, _ = http.NewRequest(http.MethodGet, targetPath, nil)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, w.Body.Bytes(), content)
		assert.Equal(t, expSHA256, w.Header().Get("X-Checksum-Sha256"))
		assert.Equal(t, expSHA1, w.Header().Get("X-Checksum-Sha1"))
		assert.Equal(t, expMD5, w.Header().Get("X-Checksum-Md5"))
	})
}

func TestUploadWithIntegrityCheck(t *testing.T) {
	session := PrepareAuth(t, db, "uploader3", false, nil, AuthH.Config.Server.JwtSecret)
	content := []byte("hello integrity")
	wrongSha256 := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	t.Run("Reject upload with mismatched SHA256", func(t *testing.T) {
		target := "/integrity/bad.txt"
		req, _ := http.NewRequest("PUT", target, bytes.NewBuffer(content))
		req.Header.Set("X-Checksum-Sha256", wrongSha256)
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		// Verify file was deleted from disk
		assert.NoFileExists(t, filepath.Join(baseDir, "integrity", "bad.txt"))

		// Verify no DB record was created
		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", target).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("Accept upload with matching SHA256", func(t *testing.T) {
		target := "/integrity/good.txt"
		sha256 := sha256.New()
		sha256.Write(content)
		req, _ := http.NewRequest("PUT", target, bytes.NewBuffer(content))
		req.Header.Set("X-Checksum-Sha256", hex.EncodeToString(sha256.Sum(nil)))
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.FileExists(t, filepath.Join(baseDir, "integrity", "good.txt"))
	})
}

func TestMaxUploadSize(t *testing.T) {
	WithConfig(t, func(c *config.Config) { c.Storage.MaxUploadSize = "10B" })
	session := PrepareAuth(t, db, "uploader4", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Reject file with bigger body and incosistent Content-Length", func(t *testing.T) {
		largeData := make([]byte, 100)
		req, _ := http.NewRequest("PUT", "/upload/big2.bin", bytes.NewBuffer(largeData))
		req.Header.Set("Content-Length", "8") //lie about content length
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("Reject file exceeding Content-Length", func(t *testing.T) {
		largeData := make([]byte, 100)
		req, _ := http.NewRequest("PUT", "/upload/big.bin", bytes.NewBuffer(largeData))
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("Accept file within limit", func(t *testing.T) {
		smallData := []byte("hello")
		req, _ := http.NewRequest("PUT", "/upload/small.bin", bytes.NewBuffer(smallData))
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestUploadWithTags(t *testing.T) {
	session := PrepareAuth(t, db, "uploader5", false, nil, AuthH.Config.Server.JwtSecret)

	t.Run("Upload with multiple tags via header", func(t *testing.T) {
		target := "/tags-test.bin"
		content := []byte("tag testing")

		req, _ := http.NewRequest("PUT", target, bytes.NewBuffer(content))
		// Test mixed separators and key/value vs label-only
		req.Header.Set("X-Tags", "env=prod; arch=x64, verified")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify Database
		var meta api.MetaResource
		err := db.Preload("Tags").Where("path = ?", target).First(&meta).Error
		assert.NoError(t, err)

		// Should have 3 tags
		assert.Len(t, meta.Tags, 3)

		// Create a map for easy lookup
		tagMap := make(map[string]string)
		for _, tag := range meta.Tags {
			tagMap[tag.Key] = tag.Value
		}

		assert.Equal(t, "prod", tagMap["env"])
		assert.Equal(t, "x64", tagMap["arch"])
		assert.Equal(t, "", tagMap["verified"]) // Label-only tag
	})

	t.Run("Overwrite existing tags on re-upload", func(t *testing.T) {
		target := "/tags-test.bin"
		req, _ := http.NewRequest("PUT", target, bytes.NewBuffer([]byte("new content")))
		req.Header.Set("X-Tags", "new=tag")
		session.Apply(req)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		var meta api.MetaResource
		db.Preload("Tags").Where("path = ?", target).First(&meta)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Len(t, meta.Tags, 1)
		assert.Equal(t, "new", meta.Tags[0].Key)
	})
}

func TestTokenUploadAndProtection(t *testing.T) {
	WithConfig(t, func(c *config.Config) {
		c.Storage.ProtectedPaths = []string{"/stable"}
	})
	admin := PrepareAuth(t, db, "admintokenupload", true, nil, AuthH.Config.Server.JwtSecret)

	tokenPayload := map[string]any{
		"user_id":       admin.User.ID,
		"name":          "CI-Bot",
		"allowed_paths": models.StringList{"/"}, // Full scope for this test
	}

	resp := Perform(t, router, "POST", "/_/api/admin/tokens", WithJSON(tokenPayload), WithSession(admin))
	assert.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	var tokenData map[string]any
	json.Unmarshal(resp.Body.Bytes(), &tokenData)
	apiToken := tokenData["plain_token"].(string)

	t.Run("SUCCESS: Upload NEW file to protected dir", func(t *testing.T) {
		url := "/stable/new-binary.bin"
		w := Perform(t, router, "PUT", url, WithBody([]byte("ver1")), WithHeader("X-API-Token", apiToken))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.FileExists(t, filepath.Join(baseDir, "stable", "new-binary.bin"))
	})

	t.Run("FAILURE: Overwrite EXISTING file in protected dir", func(t *testing.T) {
		// File already exists from previous sub-test
		url := "/stable/new-binary.bin"

		w := Perform(t, router, "PUT", url, WithBody([]byte("malicious-overwrite")), WithHeader("X-API-Token", apiToken))

		// Should be 403 Forbidden because path is protected and file exists
		assert.Equal(t, http.StatusForbidden, w.Code)

		// Verify content was NOT changed
		content, _ := os.ReadFile(filepath.Join(baseDir, "stable", "new-binary.bin"))
		assert.Equal(t, "ver1", string(content))
	})

	t.Run("SUCCESS: Overwrite EXISTING file in UNPROTECTED dir", func(t *testing.T) {
		url := "/public/temp.txt"
		// Initial upload
		Perform(t, router, "PUT", url, WithBody([]byte("old")), WithHeader("X-API-Token", apiToken))

		// Overwrite upload
		w := Perform(t, router, "PUT", url, WithBody([]byte("new")), WithHeader("X-API-Token", apiToken))

		assert.Equal(t, http.StatusOK, w.Code)
		content, _ := os.ReadFile(filepath.Join(baseDir, "public", "temp.txt"))
		assert.Equal(t, "new", string(content))
	})
}
