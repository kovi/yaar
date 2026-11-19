package api_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSyncHandler(t *testing.T, grace time.Duration) *api.Handler {
	t.Helper()
	db := freshDB(t)
	require.NoError(t, api.AutoMigrate(db, nil))
	aud, err := audit.NewAuditor(filepath.Join(t.TempDir(), "audit.log"))
	require.NoError(t, err)
	t.Cleanup(func() { aud.Close() })

	cfg := config.NewConfig()
	cfg.Storage.MissingGracePeriodDuration = grace
	return &api.Handler{
		BaseDir: t.TempDir(),
		DB:      db,
		Config:  cfg,
		Log:     logrus.NewEntry(logrus.StandardLogger()),
		Audit:   aud,
	}
}

func writeDataFile(t *testing.T, h *api.Handler, rel, content string) {
	t.Helper()
	p := filepath.Join(h.BaseDir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// syncedRecord loads a record by path; ok is false if it does not exist.
func syncedRecord(t *testing.T, h *api.Handler, path string) (api.MetaResource, bool) {
	t.Helper()
	var rs []api.MetaResource
	require.NoError(t, h.DB.Where("path = ?", path).Find(&rs).Error)
	if len(rs) == 0 {
		return api.MetaResource{}, false
	}
	return rs[0], true
}

func TestSyncMarksMissingFileAndRecoversIt(t *testing.T) {
	h := newSyncHandler(t, 7*24*time.Hour)
	ctx := context.Background()

	writeDataFile(t, h, "a/build.bin", "artifact")
	h.SyncFilesystem(ctx)
	r, ok := syncedRecord(t, h, "/a/build.bin")
	require.True(t, ok)
	require.NoError(t, h.DB.Create(&api.MetaTag{ResourceID: r.ID, Key: "branch", Value: "main"}).Error)

	// Gone (e.g. the mount is missing): kept and marked, not deleted.
	require.NoError(t, os.Remove(filepath.Join(h.BaseDir, "a/build.bin")))
	h.SyncFilesystem(ctx)
	r, ok = syncedRecord(t, h, "/a/build.bin")
	require.True(t, ok, "record must survive the first miss")
	require.NotNil(t, r.MissingSince)
	firstMiss := *r.MissingSince

	// A later pass within the grace period keeps the original timestamp.
	h.SyncFilesystem(ctx)
	r, _ = syncedRecord(t, h, "/a/build.bin")
	require.NotNil(t, r.MissingSince)
	assert.True(t, r.MissingSince.Equal(firstMiss), "missing_since must not move on repeated misses")

	// Back on disk: mark cleared, metadata intact.
	writeDataFile(t, h, "a/build.bin", "artifact")
	h.SyncFilesystem(ctx)
	r, ok = syncedRecord(t, h, "/a/build.bin")
	require.True(t, ok)
	assert.Nil(t, r.MissingSince)
	var tags int64
	h.DB.Model(&api.MetaTag{}).Where("resource_id = ?", r.ID).Count(&tags)
	assert.EqualValues(t, 1, tags, "tags must survive a temporary disappearance")
}

func TestSyncDeletesRecordAfterGracePeriod(t *testing.T) {
	h := newSyncHandler(t, 7*24*time.Hour)
	ctx := context.Background()

	writeDataFile(t, h, "old.bin", "x")
	h.SyncFilesystem(ctx)
	require.NoError(t, os.Remove(filepath.Join(h.BaseDir, "old.bin")))
	h.SyncFilesystem(ctx)

	r, ok := syncedRecord(t, h, "/old.bin")
	require.True(t, ok)
	require.NotNil(t, r.MissingSince)

	// Not yet expired: still there.
	require.NoError(t, h.DB.Model(&api.MetaResource{}).Where("id = ?", r.ID).
		UpdateColumn("missing_since", time.Now().UTC().Add(-6*24*time.Hour)).Error)
	h.SyncFilesystem(ctx)
	_, ok = syncedRecord(t, h, "/old.bin")
	require.True(t, ok)

	require.NoError(t, h.DB.Model(&api.MetaResource{}).Where("id = ?", r.ID).
		UpdateColumn("missing_since", time.Now().UTC().Add(-8*24*time.Hour)).Error)
	h.SyncFilesystem(ctx)
	_, ok = syncedRecord(t, h, "/old.bin")
	assert.False(t, ok, "record missing for longer than the grace period is removed")
}

func TestSyncZeroGraceDeletesOnFirstMiss(t *testing.T) {
	h := newSyncHandler(t, 0)
	ctx := context.Background()

	writeDataFile(t, h, "f.bin", "x")
	h.SyncFilesystem(ctx)
	require.NoError(t, os.Remove(filepath.Join(h.BaseDir, "f.bin")))
	h.SyncFilesystem(ctx)

	_, ok := syncedRecord(t, h, "/f.bin")
	assert.False(t, ok)
}

func TestSyncSkipsUnavailableDataDir(t *testing.T) {
	h := newSyncHandler(t, 0) // even with no grace period
	ctx := context.Background()

	writeDataFile(t, h, "f.bin", "x")
	h.SyncFilesystem(ctx)

	h.BaseDir = filepath.Join(t.TempDir(), "not-mounted")
	h.SyncFilesystem(ctx)

	r, ok := syncedRecord(t, h, "/f.bin")
	require.True(t, ok, "a missing data dir must not delete records")
	assert.Nil(t, r.MissingSince, "a missing data dir must not mark records missing")
}

func TestSyncUnreadableSubdirKeepsRecordsAndContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 directory")
	}
	h := newSyncHandler(t, 0) // no grace period, so any wrong "missing" deletes
	ctx := context.Background()

	writeDataFile(t, h, "locked/inner.bin", "x")
	writeDataFile(t, h, "z-after.bin", "x")
	writeDataFile(t, h, "z-gone.bin", "x")
	h.SyncFilesystem(ctx)
	require.NoError(t, os.Remove(filepath.Join(h.BaseDir, "z-gone.bin")))

	locked := filepath.Join(h.BaseDir, "locked")
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	h.SyncFilesystem(ctx)

	_, ok := syncedRecord(t, h, "/locked/inner.bin")
	assert.True(t, ok, "records under an unreadable directory must be left alone")
	_, ok = syncedRecord(t, h, "/z-after.bin")
	assert.True(t, ok, "files after the unreadable directory are still seen")
	_, ok = syncedRecord(t, h, "/z-gone.bin")
	assert.False(t, ok, "the walk continues past the error, so real misses are still handled")
}

func TestSyncIgnoresTransientFiles(t *testing.T) {
	h := newSyncHandler(t, 7*24*time.Hour)
	ctx := context.Background()

	writeDataFile(t, h, "d/.build.bin.upload-123", "partial")
	writeDataFile(t, h, ".health-456", "")
	// A record left behind for a temp file by an older version.
	require.NoError(t, h.DB.Create(&api.MetaResource{Path: "/d/.x.bin.upload-999", Type: api.ResourceTypeFile}).Error)

	h.SyncFilesystem(ctx)

	_, ok := syncedRecord(t, h, "/d/.build.bin.upload-123")
	assert.False(t, ok, "in-flight upload temp files are not artifacts")
	_, ok = syncedRecord(t, h, "/.health-456")
	assert.False(t, ok)
	_, ok = syncedRecord(t, h, "/d/.x.bin.upload-999")
	assert.False(t, ok, "stale temp-file records are dropped without a grace period")
}
