package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-uploading a file the sync worker had marked missing brings it back:
// the mark must be cleared by the upload itself, not only by the next sync
// pass, or the file stays hidden from search until then.
func TestUploadClearsMissingMark(t *testing.T) {
	session := PrepareAuth(t, db, "missing_upload_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	target := "/missing-mark/app.zip"
	w := Perform(t, router, "PUT", target, WithSession(session), WithBody([]byte("v1")))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	since := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, db.Model(&api.MetaResource{}).Where("path = ?", target).
		UpdateColumn("missing_since", since).Error)

	w = Perform(t, router, "PUT", target, WithSession(session), WithBody([]byte("v2")))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var r api.MetaResource
	require.NoError(t, db.Where("path = ?", target).First(&r).Error)
	assert.Nil(t, r.MissingSince)
}
