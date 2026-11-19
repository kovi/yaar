package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests drive reconcileMissing with a stale snapshot, standing in for an
// upload that lands while a (slow, throttled) sync walk is in progress: the
// walk did not see the file, but by the time the pass reconciles, the file and
// its freshly written record are back.

func newRaceHandler(t *testing.T, grace time.Duration) *Handler {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, AutoMigrate(db, nil))
	aud, err := audit.NewAuditor(filepath.Join(t.TempDir(), "audit.log"))
	require.NoError(t, err)
	t.Cleanup(func() { aud.Close() })
	cfg := config.NewConfig()
	cfg.Storage.MissingGracePeriodDuration = grace
	return &Handler{BaseDir: t.TempDir(), DB: db, Config: cfg, Log: logrus.NewEntry(logrus.StandardLogger()), Audit: aud}
}

func raceLog() *logrus.Entry { return logrus.NewEntry(logrus.StandardLogger()) }

func snapshot(t *testing.T, h *Handler, path string) map[string]MetaResource {
	t.Helper()
	var r MetaResource
	require.NoError(t, h.DB.Where("path = ?", path).First(&r).Error)
	return map[string]MetaResource{path: r}
}

// The record expired its grace period in the snapshot, but an upload has since
// rewritten it: it must not be deleted.
func TestReconcileKeepsRecordRewrittenAfterSnapshot(t *testing.T) {
	h := newRaceHandler(t, 7*24*time.Hour)
	long := time.Now().UTC().Add(-30 * 24 * time.Hour)
	r := MetaResource{Path: "/ci/app.zip", MissingSince: &long, ExpiryAfterUpload: ptr.Of("7d")}
	require.NoError(t, h.DB.Create(&r).Error)
	snap := snapshot(t, h, r.Path)

	// The upload: record rewritten (missing mark cleared), file not on disk
	// yet from the pass's point of view.
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, h.DB.Model(&MetaResource{}).Where("id = ?", r.ID).
		Updates(map[string]any{"missing_since": nil, "expiry_after_upload": "14d"}).Error)

	h.reconcileMissing(raceLog(), snap, map[string]bool{}, nil)

	var got []MetaResource
	require.NoError(t, h.DB.Where("id = ?", r.ID).Find(&got).Error)
	require.Len(t, got, 1, "a record rewritten after the snapshot must survive")
	assert.Equal(t, "14d", ptr.Val(got[0].ExpiryAfterUpload))
	assert.Nil(t, got[0].MissingSince, "and must not be marked missing again")
}

// The walk missed the file, but it is on disk by the time the pass reconciles.
func TestReconcileKeepsFileThatAppearedAfterWalk(t *testing.T) {
	for _, grace := range []time.Duration{0, 7 * 24 * time.Hour} {
		t.Run(grace.String(), func(t *testing.T) {
			h := newRaceHandler(t, grace)
			r := MetaResource{Path: "/late.bin"}
			require.NoError(t, h.DB.Create(&r).Error)
			snap := snapshot(t, h, r.Path)
			require.NoError(t, os.WriteFile(filepath.Join(h.BaseDir, "late.bin"), []byte("x"), 0o644))

			h.reconcileMissing(raceLog(), snap, map[string]bool{}, nil)

			var got []MetaResource
			require.NoError(t, h.DB.Where("id = ?", r.ID).Find(&got).Error)
			require.Len(t, got, 1)
			assert.Nil(t, got[0].MissingSince)
		})
	}
}

// An unchanged record whose grace period has passed is still deleted.
func TestReconcileDeletesUnchangedExpiredRecord(t *testing.T) {
	h := newRaceHandler(t, 7*24*time.Hour)
	long := time.Now().UTC().Add(-30 * 24 * time.Hour)
	r := MetaResource{Path: "/gone.bin", MissingSince: &long}
	require.NoError(t, h.DB.Create(&r).Error)

	h.reconcileMissing(raceLog(), snapshot(t, h, r.Path), map[string]bool{}, nil)

	var n int64
	require.NoError(t, h.DB.Model(&MetaResource{}).Where("id = ?", r.ID).Count(&n).Error)
	assert.Zero(t, n)
}
