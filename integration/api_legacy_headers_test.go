package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Headers from the pre-streams API must fail loudly. Ignoring them made an old
// CI script's upload succeed with its stream, expiry and keep-latest settings
// silently dropped, so artifacts that should rotate were kept forever.
func TestLegacyUploadHeadersRejected(t *testing.T) {
	admin := PrepareAuth(t, db, "legacy_hdr_admin", true, nil, AuthH.Config.Server.JwtSecret)

	cases := []struct {
		header, value, mentions string
	}{
		{"X-Stream", "legacy-hdr-stream/legacy-hdr-group", "Yaar-Stream"},
		{"X-Expires", "7d", "Yaar-Retention-Expire-After-Upload"},
		{"X-KeepLatest", "true", "auto_expire_previous"},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			path := "/legacy-headers/" + tc.header + ".bin"
			for _, method := range []string{http.MethodPut, http.MethodPost} {
				w := Perform(t, router, method, path,
					WithSession(admin), WithBody([]byte("payload")), WithHeader(tc.header, tc.value))
				require.Equal(t, http.StatusBadRequest, w.Code, "%s: %s", method, w.Body.String())

				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, "LEGACY_HEADER", body["error"])
				assert.Equal(t, tc.header, body["header"])
				assert.Contains(t, body["message"], tc.mentions, "message should name the replacement")
			}

			// Rejected before anything is written.
			_, err := os.Stat(filepath.Join(baseDir, path))
			assert.True(t, os.IsNotExist(err), "rejected upload must not create the file")
		})
	}

	t.Run("current headers still accepted", func(t *testing.T) {
		w := Perform(t, router, http.MethodPut, "/legacy-headers/ok.bin",
			WithSession(admin), WithBody([]byte("payload")),
			WithHeader("Yaar-Stream", "legacy-hdr-stream"), WithHeader("Yaar-Group", "legacy-hdr-group"),
			WithHeader("Yaar-Retention-Expire-After-Upload", "7d"))
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})
}

func TestLegacyTokenHeaderRejected(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			w := Perform(t, router, method, "/legacy-token/file.bin",
				WithBody([]byte("payload")), WithHeader("X-API-Token", "af_0123456789abcdef"))
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "LEGACY_HEADER", body["error"])
			assert.Contains(t, body["message"], "Authorization: Bearer")
		})
	}
}
