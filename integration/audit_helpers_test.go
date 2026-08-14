package integration

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// auditReaderSeq keeps each reader account unique. The name used to be derived
// from t.Name() alone, so a test that called findAuditEntry twice tripped the
// users.username unique constraint on the second call.
var auditReaderSeq atomic.Uint64

// findAuditEntry returns the newest audit entry matching action and resource, or
// nil if there is none. It reads through the admin API so the assertion covers
// the same path an operator would use to investigate a rejected request.
func findAuditEntry(t *testing.T, action, resource string) map[string]any {
	t.Helper()

	// Subtest names contain "/" and are not valid usernames.
	name := "audit_reader_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) +
		"_" + strconv.FormatUint(auditReaderSeq.Add(1), 10)
	auditor := PrepareAuth(t, db, name, true, nil, AuthH.Config.Server.JwtSecret)

	w := Perform(t, router, http.MethodGet, "/_/api/v1/admin/audit-log", WithSession(auditor))
	if w.Code != http.StatusOK {
		t.Fatalf("reading audit log: status %d, body %s", w.Code, w.Body.String())
	}

	var resp struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding audit log: %v", err)
	}

	for _, e := range resp.Entries {
		if e["action"] == action && e["resource"] == resource {
			return e
		}
	}
	return nil
}
