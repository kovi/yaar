package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/models"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestUser contains everything needed to perform authenticated requests in tests
type TestSession struct {
	User          models.User
	PlainPassword string
	Token         string
}

// PrepareAuth creates a user in the DB and returns a session with a valid JWT
func PrepareAuth(t *testing.T, db *gorm.DB, username string, isAdmin bool, allowedPaths *models.StringList, secret string) *TestSession {
	t.Helper()

	if allowedPaths == nil {
		allowedPaths = &models.StringList{"/"}
	}

	user := models.User{
		Username:     username,
		IsAdmin:      isAdmin,
		AllowedPaths: *allowedPaths,
	}
	// We use a fixed password for all test users to keep it simple
	pw := "test-password-123"
	user.SetPassword(pw)

	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	token, err := auth.GenerateToken(user, secret)
	if err != nil {
		t.Fatalf("failed to generate test token: %v", err)
	}

	return &TestSession{
		User:          user,
		PlainPassword: pw,
		Token:         token,
	}
}

// Apply adds the bearer token to a request
func (s *TestSession) Apply(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+s.Token)
}

func ClearDatabase(db *gorm.DB) {
	// Session with AllowGlobalUpdate: true allows Delete() without a Where clause
	db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&api.MetaTag{})
	db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&api.MetaResource{})
}

// RequestOption defines a function that modifies an http.Request
type RequestOption func(*http.Request)

// Perform performs an HTTP request against the provided handler with optional modifiers
func Perform(t *testing.T, h http.Handler, method, path string, opts ...RequestOption) *httptest.ResponseRecorder {
	req, err := http.NewRequest(method, path, nil)
	assert.NoError(t, err)
	for _, opt := range opts {
		opt(req)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// --- Specific Options ---

func WithBody(body []byte) RequestOption {
	return func(req *http.Request) {
		req.Body = io.NopCloser(bytes.NewBuffer(body))
		// req.ContentLength = int64(len(body))
	}
}

func WithRawReader(reader io.Reader, length int64) RequestOption {
	return func(req *http.Request) {
		req.Body = io.NopCloser(reader)
		req.ContentLength = length
	}
}

type H map[string]any

func WithJSON(v any) RequestOption {
	return func(req *http.Request) {
		data, _ := json.Marshal(v)
		req.Body = io.NopCloser(bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
	}
}

func WithHeader(key, value string) RequestOption {
	return func(req *http.Request) {
		req.Header.Set(key, value)
	}
}

// WithSession supports your auth.TestSession (which has an Apply method)
func WithSession(session interface{ Apply(*http.Request) }) RequestOption {
	return func(req *http.Request) {
		if session != nil {
			session.Apply(req)
		}
	}
}

func WithToken(token string) RequestOption {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

type faultyReader struct {
	data  []byte
	limit int
	off   int
}

func (f *faultyReader) Read(p []byte) (n int, err error) {
	if f.off >= f.limit {
		// Simulate a "Connection Reset by Peer" error
		return 0, fmt.Errorf("read tcp: connection reset by peer")
	}
	n = copy(p, f.data[f.off:f.limit])
	f.off += n
	return n, nil
}

func createGroupedResource(db *gorm.DB, path string, streamName string, groupName string) api.MetaResource {
	var stream api.Stream
	db.Where("name = ?", streamName).FirstOrCreate(&stream, api.Stream{Name: streamName})

	var group api.Group
	db.Where("stream_id = ? AND name = ?", stream.ID, groupName).
		FirstOrCreate(&group, api.Group{StreamID: stream.ID, Name: groupName})

	r := api.MetaResource{
		Path:    path,
		Type:    api.ResourceTypeFile,
		GroupID: &group.ID,
	}
	db.Create(&r)
	return r
}
