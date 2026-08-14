package integration

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cover the state-changing operations that used to complete without
// leaving any audit entry at all: account creation, token revocation, retention
// policy edits, and authorization denials.

func TestAuditUserCreate(t *testing.T) {
	admin := PrepareAuth(t, db, "audit-usercreate-admin", true, nil, AuthH.Config.Server.JwtSecret)

	w := Perform(t, router, http.MethodPost, "/_/api/admin/users",
		WithJSON(map[string]any{
			"username": "audit-created-user",
			"password": "hunter2-hunter2",
			"is_admin": true,
		}),
		WithSession(admin),
	)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	e := findAuditEntry(t, "USER_CREATE", "audit-created-user")
	require.NotNil(t, e, "creating a user must leave an audit entry")
	assert.Equal(t, "SUCCESS", e["status"])
	assert.Equal(t, admin.User.Username, e["created_by"])
	// The privilege grant is the point of the record.
	assert.Equal(t, true, e["is_admin"])
}

func TestAuditTokenDelete(t *testing.T) {
	user := PrepareAuth(t, db, "audit-tokendel-user", false, nil, AuthH.Config.Server.JwtSecret)

	w := Perform(t, router, http.MethodPost, "/_/api/tokens",
		WithJSON(map[string]any{"name": "audit-doomed-token"}),
		WithSession(user),
	)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var created struct {
		ID uint `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// Creation already recorded the owner; it used to log an empty string because
	// the Token was built literally with no User association loaded.
	if e := findAuditEntry(t, "TOKEN_CREATED", "audit-doomed-token"); assert.NotNil(t, e) {
		assert.Equal(t, user.User.Username, e["owner"])
	}

	w = Perform(t, router, http.MethodDelete,
		"/_/api/tokens/"+strconv.FormatUint(uint64(created.ID), 10), WithSession(user))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	e := findAuditEntry(t, "TOKEN_DELETED", "audit-doomed-token")
	require.NotNil(t, e, "revoking a token must leave an audit entry")
	assert.Equal(t, "SUCCESS", e["status"])
	assert.Equal(t, user.User.Username, e["owner"])
	assert.Equal(t, user.User.Username, e["revoked_by"])
}

func TestAuditStreamUpdate(t *testing.T) {
	admin := PrepareAuth(t, db, "audit-stream-admin", true, nil, AuthH.Config.Server.JwtSecret)

	w := Perform(t, router, http.MethodPut, "/_/api/v1/streams/audit-stream",
		WithJSON(map[string]any{"auto_expire_previous": true}),
		WithSession(admin),
	)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	e := findAuditEntry(t, "STREAM_UPDATE", "audit-stream")
	require.NotNil(t, e, "a retention policy edit must leave an audit entry")
	assert.Equal(t, "SUCCESS", e["status"])
	assert.Equal(t, admin.User.Username, e["user"])
	// The settings themselves are recorded: they decide what the janitor deletes.
	assert.Equal(t, true, e["auto_expire_previous"])
	assert.Equal(t, "create", e["op"])
}

func TestAuditAuthDenied(t *testing.T) {
	worker := PrepareAuth(t, db, "audit-denied-worker", false, nil, AuthH.Config.Server.JwtSecret)

	w := Perform(t, router, http.MethodGet, "/_/api/v1/admin/audit-log", WithSession(worker))
	require.Equal(t, http.StatusForbidden, w.Code)

	e := findAuditEntry(t, "AUTH_DENIED", "/_/api/v1/admin/audit-log")
	require.NotNil(t, e, "a non-admin probing an admin route must be recorded")
	assert.Equal(t, "FAILURE", e["status"])
	// The entry names the account, which is what distinguishes this from an
	// anonymous 401.
	assert.Equal(t, worker.User.Username, e["user"])
}

func TestAuditAuthDeniedBadToken(t *testing.T) {
	w := Perform(t, router, http.MethodGet, "/_/api/v1/admin/audit-log",
		WithToken("af_this-token-does-not-exist"))
	require.Equal(t, http.StatusUnauthorized, w.Code)

	e := findAuditEntry(t, "AUTH_DENIED", "/_/api/v1/admin/audit-log")
	require.NotNil(t, e, "an unknown API token must be recorded")
	assert.Equal(t, "FAILURE", e["status"])
}

// TestAuditLogPagingFieldsPresent guards the contract the UI depends on: the
// generation cursor and has_more must be served, since after a rotation
// next_before_offset alone cannot distinguish "no more entries" from
// "continue at the end of the next older file".
func TestAuditLogPagingFieldsPresent(t *testing.T) {
	admin := PrepareAuth(t, db, "audit-paging-admin", true, nil, AuthH.Config.Server.JwtSecret)

	w := Perform(t, router, http.MethodGet, "/_/api/v1/admin/audit-log", WithSession(admin))
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "next_generation")
	assert.Contains(t, resp, "has_more")

	// An explicit generation is accepted and echoed as a valid cursor.
	w = Perform(t, router, http.MethodGet,
		"/_/api/v1/admin/audit-log?generation=0", WithSession(admin))
	assert.Equal(t, http.StatusOK, w.Code)

	// A malformed generation is rejected rather than silently treated as 0.
	w = Perform(t, router, http.MethodGet,
		"/_/api/v1/admin/audit-log?generation=-2", WithSession(admin))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
