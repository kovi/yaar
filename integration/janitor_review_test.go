package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-uploading a file into the group it already belongs to used to fail with a
// 500 *after* the new content had replaced the file on disk, leaving the DB
// with stale checksums. Same group is now a plain overwrite; another group is
// a 409 that leaves the previous artifact untouched.
func TestUpload_ReuploadIntoGroup(t *testing.T) {
	session := PrepareAuth(t, db, "reupload_user", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)

	put := func(body, group string) int {
		w := Perform(t, router, "PUT", "/reupload/bundle",
			WithSession(session), WithBody([]byte(body)),
			WithHeader(api.HeaderStream, "reupload-stream"),
			WithHeader(api.HeaderGroup, group))
		return w.Code
	}
	onDisk := func() string {
		b, err := os.ReadFile(filepath.Join(baseDir, "reupload", "bundle"))
		require.NoError(t, err)
		return string(b)
	}

	require.Equal(t, http.StatusOK, put("v1", "1"))
	require.Equal(t, http.StatusOK, put("v2", "1"), "same group re-upload is an overwrite")
	assert.Equal(t, "v2", onDisk())

	assert.Equal(t, http.StatusConflict, put("v3", "2"), "moving to another group is refused")
	assert.Equal(t, "v2", onDisk(), "a refused upload must not replace the file on disk")

	var r api.MetaResource
	require.NoError(t, db.Where("path = ?", "/reupload/bundle").First(&r).Error)
	assert.Equal(t, int64(2), r.Size)

	matches, _ := filepath.Glob(filepath.Join(baseDir, "reupload", ".bundle.upload-*"))
	assert.Empty(t, matches, "refused upload must not leave its temp file behind")
}

type pendingResp struct {
	Suspended bool               `json:"suspended"`
	Reason    string             `json:"reason"`
	Groups    []api.PendingGroup `json:"groups"`
	Files     []api.PendingFile  `json:"files"`
}

func getPending(t *testing.T, session *TestSession) pendingResp {
	t.Helper()
	w := Perform(t, router, "GET", "/_/api/system/janitor/pending", WithSession(session))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var p pendingResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	return p
}

func findPending(p pendingResp, stream, group string) *api.PendingGroup {
	for i := range p.Groups {
		if p.Groups[i].Stream == stream && p.Groups[i].Group == group {
			return &p.Groups[i]
		}
	}
	return nil
}

func TestJanitorSuspendReviewKeepResume(t *testing.T) {
	admin := PrepareAuth(t, db, "review_admin", true, nil, AuthH.Config.Server.JwtSecret)
	plain := PrepareAuth(t, db, "review_plain", false, nil, AuthH.Config.Server.JwtSecret)
	ClearDatabase(db)
	t.Cleanup(func() { _ = api.ResumeJanitor(db) })

	write := func(rel string) {
		full := filepath.Join(baseDir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(rel), 0644))
	}
	past := time.Now().UTC().Add(-time.Hour)

	// Expired group with two files.
	a := createGroupedResource(db, "/review/old/a.bin", "review-s", "old")
	b := createGroupedResource(db, "/review/old/b.bin", "review-s", "old")
	write("/review/old/a.bin")
	write("/review/old/b.bin")
	require.NoError(t, db.Model(&api.Group{}).Where("id = ?", *a.GroupID).Update("effective_delete_after", past).Error)

	// auto_expire_previous stream: older group goes on the next upload.
	createGroupedResource(db, "/review/ae/1.bin", "review-ae", "1")
	time.Sleep(5 * time.Millisecond)
	createGroupedResource(db, "/review/ae/2.bin", "review-ae", "2")
	require.NoError(t, db.Model(&api.Stream{}).Where("name = ?", "review-ae").Update("auto_expire_previous", true).Error)

	// Admin only.
	assert.Equal(t, http.StatusForbidden, Perform(t, router, "GET", "/_/api/system/janitor/pending", WithSession(plain)).Code)
	assert.Equal(t, http.StatusForbidden, Perform(t, router, "POST", "/_/api/system/janitor/resume", WithSession(plain)).Code)

	// Suspend; the janitor must not delete anything.
	w := Perform(t, router, "POST", "/_/api/system/janitor/suspend", WithSession(admin), WithJSON(map[string]string{"reason": "test"}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, Meta.RunCleanup())
	assert.FileExists(t, filepath.Join(baseDir, "review", "old", "a.bin"), "suspended janitor must not delete")

	p := getPending(t, admin)
	assert.True(t, p.Suspended)
	assert.Equal(t, "test", p.Reason)
	old := findPending(p, "review-s", "old")
	require.NotNil(t, old, "expired group must be listed")
	assert.Equal(t, api.PendingDue, old.Reason)
	assert.Len(t, old.Files, 2)
	ae1 := findPending(p, "review-ae", "1")
	require.NotNil(t, ae1, "older group of auto_expire stream must be listed")
	assert.Equal(t, api.PendingNextUpload, ae1.Reason)
	assert.Nil(t, findPending(p, "review-ae", "2"), "latest group is not pending")

	// Keep one file of the expired group: it gets locked, the other still goes.
	w = Perform(t, router, "POST", "/_/api/system/janitor/keep", WithSession(admin), WithJSON(map[string]any{"resource_ids": []uint{b.ID}}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	// Keep the whole next-upload group.
	w = Perform(t, router, "POST", "/_/api/system/janitor/keep", WithSession(admin), WithJSON(map[string]any{"group_ids": []uint{ae1.ID}}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	p = getPending(t, admin)
	old = findPending(p, "review-s", "old")
	require.NotNil(t, old)
	require.Len(t, old.Files, 1)
	assert.Equal(t, "/review/old/a.bin", old.Files[0].Path)
	assert.Nil(t, findPending(p, "review-ae", "1"), "kept group is no longer pending")

	var kept api.MetaResource
	require.NoError(t, db.First(&kept, b.ID).Error)
	assert.True(t, ptr.Val(kept.Immutable), "keep locks the file")

	// Resume; now the janitor deletes what was not kept.
	w = Perform(t, router, "POST", "/_/api/system/janitor/resume", WithSession(admin))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, Meta.RunCleanup())
	assert.NoFileExists(t, filepath.Join(baseDir, "review", "old", "a.bin"))
	assert.FileExists(t, filepath.Join(baseDir, "review", "old", "b.bin"), "kept file survives")

	w = Perform(t, router, "GET", "/_/api/system/janitor/status", WithSession(admin))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"suspended":false}`, w.Body.String())
}

// Admins can suspend the janitor at any time, not only after a migration; it
// stays suspended until resumed.
func TestJanitorManualSuspend(t *testing.T) {
	admin := PrepareAuth(t, db, "suspend_admin", true, nil, AuthH.Config.Server.JwtSecret)
	t.Cleanup(func() { _ = api.ResumeJanitor(db) })

	status := func() map[string]any {
		w := Perform(t, router, "GET", "/_/api/system/janitor/status", WithSession(admin))
		require.Equal(t, http.StatusOK, w.Code)
		var m map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
		return m
	}

	// No body: default reason.
	w := Perform(t, router, "POST", "/_/api/system/janitor/suspend", WithSession(admin))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "suspended by admin", status()["reason"])

	// Suspending again replaces the reason.
	w = Perform(t, router, "POST", "/_/api/system/janitor/suspend", WithSession(admin), WithJSON(map[string]string{"reason": "  release freeze  "}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	st := status()
	assert.Equal(t, true, st["suspended"])
	assert.Equal(t, "release freeze", st["reason"])

	w = Perform(t, router, "POST", "/_/api/system/janitor/resume", WithSession(admin))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, false, status()["suspended"])
}

// The janitor log reads the audit log with ?any= (OR of substrings).
func TestAuditLog_AnyFilter(t *testing.T) {
	admin := PrepareAuth(t, db, "anyfilter_admin", true, nil, AuthH.Config.Server.JwtSecret)
	t.Cleanup(func() { _ = api.ResumeJanitor(db) })

	require.Equal(t, http.StatusOK, Perform(t, router, "POST", "/_/api/system/janitor/suspend", WithSession(admin), WithJSON(map[string]string{"reason": "any-filter-check"})).Code)
	require.Equal(t, http.StatusOK, Perform(t, router, "POST", "/_/api/system/janitor/resume", WithSession(admin)).Code)

	w := Perform(t, router, "GET", "/_/api/v1/admin/audit-log?limit=50&any=janitor_suspend&any=janitor_resume", WithSession(admin))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var page struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.NotEmpty(t, page.Entries)
	ops := map[string]bool{}
	for _, e := range page.Entries {
		op, _ := e["operation"].(string)
		assert.Contains(t, []string{"janitor_suspend", "janitor_resume"}, op)
		ops[op] = true
	}
	assert.True(t, ops["janitor_suspend"] && ops["janitor_resume"], "both alternatives must match")

	// Resuming an already running janitor is a no-op: no extra audit entry.
	before := len(page.Entries)
	require.Equal(t, http.StatusOK, Perform(t, router, "POST", "/_/api/system/janitor/resume", WithSession(admin)).Code)
	w = Perform(t, router, "GET", "/_/api/v1/admin/audit-log?limit=50&any=janitor_suspend&any=janitor_resume", WithSession(admin))
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Len(t, page.Entries, before)

	tooMany := "/_/api/v1/admin/audit-log?any=a&any=b&any=c&any=d&any=e&any=f&any=g&any=h&any=i&any=j&any=k"
	assert.Equal(t, http.StatusBadRequest, Perform(t, router, "GET", tooMany, WithSession(admin)).Code)
}
