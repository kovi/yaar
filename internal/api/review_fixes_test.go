package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/models"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reloadGroup(t *testing.T, h *Handler, id uint) Group {
	t.Helper()
	var g Group
	require.NoError(t, h.DB.First(&g, id).Error)
	return g
}

// Removing a ghost member must recompute the group deadline from the members
// that remain, not clear it: a cleared deadline is never selected by the
// janitor again, so the remaining files would be kept forever.
func TestSyncGhostRemovalKeepsGroupDeadline(t *testing.T) {
	h := newRaceHandler(t, 0)
	s := Stream{Name: "s"}
	require.NoError(t, h.DB.Create(&s).Error)
	g := Group{StreamID: s.ID, Name: "g"}
	require.NoError(t, h.DB.Create(&g).Error)

	exp := now().Add(48 * time.Hour).Truncate(time.Second)
	var ghost MetaResource
	for _, p := range []string{"/s/a.bin", "/s/b.bin", "/s/c.bin"} {
		r := MetaResource{Path: p, GroupID: &g.ID, ExpiryAt: &exp}
		require.NoError(t, r.Save(h.DB, h.Config))
		if p == "/s/a.bin" {
			ghost = r
		} else {
			writeFile(t, h, p)
		}
	}
	require.NotNil(t, reloadGroup(t, h, g.ID).EffectiveDeleteAfter)

	require.NoError(t, h.DB.First(&ghost, ghost.ID).Error)
	h.reconcileMissing(raceLog(), map[string]MetaResource{ghost.Path: ghost}, map[string]bool{}, nil)

	_, ok := syncRecord(t, h, ghost.Path)
	require.False(t, ok, "ghost record removed (grace 0)")
	got := reloadGroup(t, h, g.ID).EffectiveDeleteAfter
	require.NotNil(t, got, "the remaining members still expire")
	assert.True(t, got.Equal(exp), "deadline is the remaining members' latest: got %v want %v", got, exp)
}

// An older group of an auto_expire_previous stream stays armed when a member
// leaves it.
func TestRefreshGroupDeadlineKeepsStreamPolicy(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	s := Stream{Name: "s", AutoExpirePrevious: ptr.Of(true)}
	require.NoError(t, h.DB.Create(&s).Error)
	old := Group{StreamID: s.ID, Name: "old", CreatedAt: now().Add(-time.Hour)}
	latest := Group{StreamID: s.ID, Name: "new", CreatedAt: now()}
	require.NoError(t, h.DB.Create(&old).Error)
	require.NoError(t, h.DB.Create(&latest).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/s/old.bin", GroupID: &old.ID}).Error)

	require.NoError(t, refreshGroupDeadline(h.DB, h.Config, old.ID))
	assert.NotNil(t, reloadGroup(t, h, old.ID).EffectiveDeleteAfter, "older group still expires")

	exp := now().Add(24 * time.Hour).Truncate(time.Second)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/s/new.bin", GroupID: &latest.ID, DeleteAfter: &exp}).Error)
	require.NoError(t, refreshGroupDeadline(h.DB, h.Config, latest.ID))
	got := reloadGroup(t, h, latest.ID).EffectiveDeleteAfter
	require.NotNil(t, got)
	assert.True(t, got.Equal(exp), "latest group follows its members: got %v", got)
}

func writeFile(t *testing.T, h *Handler, rel string) {
	t.Helper()
	p := filepath.Join(h.BaseDir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
}

func syncRecord(t *testing.T, h *Handler, path string) (MetaResource, bool) {
	t.Helper()
	var rs []MetaResource
	require.NoError(t, h.DB.Where("path = ?", path).Find(&rs).Error)
	if len(rs) == 0 {
		return MetaResource{}, false
	}
	return rs[0], true
}

// A failed commit after the upload went live puts the previous artifact back;
// a new file is removed again.
func TestPreservePreviousRestoresOnFailedCommit(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "a.bin")
	require.NoError(t, os.WriteFile(full, []byte("v1"), 0o644))
	tmp := filepath.Join(dir, ".a.bin.upload-123")
	require.NoError(t, os.WriteFile(tmp, []byte("v2"), 0o644))

	prev, err := preservePrevious(full, tmp)
	require.NoError(t, err)
	require.True(t, isTransientFile(filepath.Base(prev.link)), "the kept version is ignored by sync")
	require.NoError(t, os.Rename(tmp, full))
	b, _ := os.ReadFile(full)
	require.Equal(t, "v2", string(b))

	require.NoError(t, prev.restore(full))
	b, _ = os.ReadFile(full)
	assert.Equal(t, "v1", string(b), "previous artifact restored")
	_, err = os.Stat(prev.link)
	assert.True(t, os.IsNotExist(err), "link consumed by the restore")

	// Success path: the link is discarded and the new body stays.
	require.NoError(t, os.WriteFile(tmp, []byte("v3"), 0o644))
	prev, err = preservePrevious(full, tmp)
	require.NoError(t, err)
	require.NoError(t, os.Rename(tmp, full))
	prev.discard()
	b, _ = os.ReadFile(full)
	assert.Equal(t, "v3", string(b))
	_, err = os.Stat(prev.link)
	assert.True(t, os.IsNotExist(err))

	// Nothing there before: restoring removes the new file.
	fresh := filepath.Join(dir, "new.bin")
	tmp2 := filepath.Join(dir, ".new.bin.upload-7")
	require.NoError(t, os.WriteFile(tmp2, []byte("n"), 0o644))
	prev, err = preservePrevious(fresh, tmp2)
	require.NoError(t, err)
	require.Empty(t, prev.link)
	require.NoError(t, os.Rename(tmp2, fresh))
	require.NoError(t, prev.restore(fresh))
	_, err = os.Stat(fresh)
	assert.True(t, os.IsNotExist(err))
}

// A record under a subtree the walk could not read keeps its missing mark
// (and the grace clock keeps running); it is not reported as back.
func TestSyncUnreadableSubtreeKeepsMissingMark(t *testing.T) {
	h := newRaceHandler(t, 7*24*time.Hour)
	since := now().Add(-5 * 24 * time.Hour).Truncate(time.Second)
	r := MetaResource{Path: "/mnt/x/f.bin", MissingSince: &since}
	require.NoError(t, h.DB.Create(&r).Error)
	require.NoError(t, h.DB.First(&r, r.ID).Error)

	h.reconcileMissing(raceLog(), map[string]MetaResource{r.Path: r}, map[string]bool{}, []string{"/mnt/x"})

	got, ok := syncRecord(t, h, r.Path)
	require.True(t, ok)
	require.NotNil(t, got.MissingSince, "mark kept")
	assert.True(t, got.MissingSince.Equal(since), "grace clock not restarted")
}

func TestIsTransientFileMatchesOnlyOwnTempFiles(t *testing.T) {
	for name, want := range map[string]bool{
		".a.bin.upload-123":      true,
		".a.bin.upload-123-prev": true,
		".health-42":             true,
		".env.upload-backup":     false,
		".health-report.json":    false,
		".a.upload-1.txt":        false,
		"a.bin.upload-1":         false,
		".upload-1":              false,
	} {
		assert.Equal(t, want, isTransientFile(name), name)
	}
	assert.Error(t, checkReservedName("/x/.a.bin.upload-9"))
	assert.NoError(t, checkReservedName("/x/.env.upload-backup"))
}

// User dotfiles that merely resemble temp names are tracked like any file.
func TestSyncTracksLookalikeDotfiles(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	writeFile(t, h, "cfg/.env.upload-backup")
	writeFile(t, h, "ci/.health-report.json")
	h.SyncFilesystem(t.Context())
	for _, p := range []string{"/cfg/.env.upload-backup", "/ci/.health-report.json"} {
		_, ok := syncRecord(t, h, p)
		assert.True(t, ok, p)
	}
}

// An admin suspend on top of a migration suspension keeps the review flow.
func TestAdminSuspendKeepsMigrationReason(t *testing.T) {
	assert.Equal(t, "release freeze", adminSuspendReason(nil, "release freeze"))
	assert.Equal(t, "b", adminSuspendReason(ptr.Of("a"), "b"))
	m := "migration: backfilled group deletion deadlines"
	got := adminSuspendReason(&m, "release freeze")
	assert.Equal(t, m+adminReasonSep+"release freeze", got)
	assert.Equal(t, m+adminReasonSep+"second", adminSuspendReason(&got, "second"), "replaces the earlier admin reason")
	// A later migration still adds its reason and keeps the admin's.
	again := migrationSuspendReason(&got, "other")
	assert.Equal(t, "migration: backfilled group deletion deadlines, other"+adminReasonSep+"release freeze", again)

	h := newRaceHandler(t, time.Hour)
	require.NoError(t, SuspendJanitor(h.DB, m))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"reason":"release freeze"}`))
	h.SuspendJanitorHandler(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	st, err := janitorSuspension(h.DB)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(st.Value, migrationSuspendPrefix), st.Value)
}

// Older groups of an auto_expire_previous stream are due right after the
// backfill, as handleStreamRetention would make them, unless fully locked.
func TestBackfillExpiresOlderAutoExpireGroups(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	s := Stream{Name: "s", AutoExpirePrevious: ptr.Of(true)}
	require.NoError(t, h.DB.Create(&s).Error)
	base := now().Add(-72 * time.Hour)
	old := Group{StreamID: s.ID, Name: "old", CreatedAt: base}
	locked := Group{StreamID: s.ID, Name: "locked", CreatedAt: base.Add(time.Hour)}
	latest := Group{StreamID: s.ID, Name: "new", CreatedAt: base.Add(2 * time.Hour)}
	for _, g := range []*Group{&old, &locked, &latest} {
		require.NoError(t, h.DB.Create(g).Error)
	}
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/s/old", GroupID: &old.ID}).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/s/locked", GroupID: &locked.ID, Immutable: ptr.Of(true)}).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/s/new", GroupID: &latest.ID}).Error)

	_, err := backfillGroupDeadlines(h.DB, nil)
	require.NoError(t, err)

	assert.NotNil(t, reloadGroup(t, h, old.ID).EffectiveDeleteAfter, "older group due")
	assert.Nil(t, reloadGroup(t, h, locked.ID).EffectiveDeleteAfter, "fully locked group kept")
	assert.Nil(t, reloadGroup(t, h, latest.ID).EffectiveDeleteAfter, "latest group survives")
}

// Protected members count as locked: an older group made only of them is not
// re-armed on every save.
func TestStreamRetentionSkipsFullyProtectedGroup(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	h.Config.Storage.ProtectedPaths = []string{"/protected"}
	s := Stream{Name: "s", AutoExpirePrevious: ptr.Of(true)}
	require.NoError(t, h.DB.Create(&s).Error)
	old := Group{StreamID: s.ID, Name: "old", CreatedAt: now().Add(-time.Hour)}
	require.NoError(t, h.DB.Create(&old).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/protected/a", GroupID: &old.ID}).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/protected/b", GroupID: &old.ID, Immutable: ptr.Of(true)}).Error)
	latest := Group{StreamID: s.ID, Name: "new", CreatedAt: now()}
	require.NoError(t, h.DB.Create(&latest).Error)

	r := MetaResource{Path: "/s/new.bin", GroupID: &latest.ID}
	require.NoError(t, r.Save(h.DB, h.Config))
	assert.Nil(t, reloadGroup(t, h, old.ID).EffectiveDeleteAfter)

	// Without the protection the same group is expired.
	r2 := MetaResource{Path: "/s/new2.bin", GroupID: &latest.ID}
	require.NoError(t, r2.Save(h.DB, nil))
	assert.NotNil(t, reloadGroup(t, h, old.ID).EffectiveDeleteAfter)
}

func keepPending(t *testing.T, h *Handler, body string) int {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h.KeepPending(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var res struct{ Locked int }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	return res.Locked
}

// Keeping the files the review listed locks only those; a file uploaded into
// the group afterwards stays unlocked and the group keeps its deadline for it.
func TestKeepPendingLocksOnlyListedFiles(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	s := Stream{Name: "s"}
	require.NoError(t, h.DB.Create(&s).Error)
	past := now().Add(-time.Hour)
	g := Group{StreamID: s.ID, Name: "g", EffectiveDeleteAfter: &past}
	require.NoError(t, h.DB.Create(&g).Error)
	seen := MetaResource{Path: "/s/seen", GroupID: &g.ID}
	later := MetaResource{Path: "/s/later", GroupID: &g.ID}
	require.NoError(t, h.DB.Create(&seen).Error)
	require.NoError(t, h.DB.Create(&later).Error)

	body, _ := json.Marshal(map[string]any{"resource_ids": []uint{seen.ID}})
	assert.Equal(t, 1, keepPending(t, h, string(body)))

	s1, _ := syncRecord(t, h, "/s/seen")
	s2, _ := syncRecord(t, h, "/s/later")
	assert.True(t, ptr.Val(s1.Immutable))
	assert.False(t, ptr.Val(s2.Immutable), "file the admin never saw is not locked")
	assert.NotNil(t, reloadGroup(t, h, g.ID).EffectiveDeleteAfter, "group still has something to expire")

	body, _ = json.Marshal(map[string]any{"resource_ids": []uint{later.ID}})
	assert.Equal(t, 1, keepPending(t, h, string(body)))
	assert.Nil(t, reloadGroup(t, h, g.ID).EffectiveDeleteAfter, "fully kept group drops its deadline")
}

// Installs whose stream/group extraction ran in the original version get their
// retain_latest streams flagged for review; corrected runs do not.
func TestReviewLegacyKeepLatest(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	ok, err := extractionRanUnrevised(h.DB)
	require.NoError(t, err)
	require.False(t, ok, "fresh install ran the corrected extraction")

	// Simulate an install upgraded at the original release: extraction
	// recorded without the revision marker, review migration not yet run.
	require.NoError(t, h.DB.Where("key = ?", extractionRevisionKey).Delete(&models.SystemState{}).Error)
	require.NoError(t, h.DB.Table("migrations").Where("id = ?", "202611191200_review_legacy_keep_latest").Delete(nil).Error)
	require.NoError(t, h.DB.Create(&Stream{Name: "legacy", RetainLatest: ptr.Of(true)}).Error)
	require.NoError(t, h.DB.Create(&Stream{Name: "plain"}).Error)
	ok, err = extractionRanUnrevised(h.DB)
	require.NoError(t, err)
	require.True(t, ok)

	aud, err := audit.NewAuditor(filepath.Join(t.TempDir(), "review.log"))
	require.NoError(t, err)
	t.Cleanup(func() { aud.Close() })
	require.NoError(t, AutoMigrate(h.DB, aud))

	page, err := aud.ReadPage(audit.ReadOptions{BeforeOffset: -1, Limit: 50, AnyOf: []string{"migration_review_"}})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "legacy", page.Entries[0]["stream"])

	var st Stream
	require.NoError(t, h.DB.Where("name = ?", "legacy").First(&st).Error)
	assert.True(t, ptr.Val(st.RetainLatest), "flagged only, not changed")
}
