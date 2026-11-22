package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Groups with the same created_at (e.g. created in one migration run) must
// resolve "latest" the same way in handleStreamRetention as on the review
// page, which breaks ties by id.
func TestStreamRetentionLatestGroupTieBreaksByID(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	ts := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := Stream{Name: "tie", AutoExpirePrevious: ptr.Of(true)}
	require.NoError(t, h.DB.Create(&s).Error)
	older := Group{StreamID: s.ID, Name: "a", CreatedAt: ts}
	newer := Group{StreamID: s.ID, Name: "b", CreatedAt: ts}
	require.NoError(t, h.DB.Create(&older).Error)
	require.NoError(t, h.DB.Create(&newer).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/tie/a.bin", GroupID: &older.ID}).Error)

	pd, err := h.pendingDeletions(now())
	require.NoError(t, err)
	require.Len(t, pd.Groups, 1)
	require.Equal(t, older.ID, pd.Groups[0].ID, "review page: the higher id is the latest group")

	r := MetaResource{Path: "/tie/b.bin", GroupID: &newer.ID}
	require.NoError(t, r.Save(h.DB, h.Config))

	require.NoError(t, h.DB.First(&older, older.ID).Error)
	require.NoError(t, h.DB.First(&newer, newer.ID).Error)
	assert.NotNil(t, older.EffectiveDeleteAfter, "the group the review page listed is the one expired")
	assert.Nil(t, newer.EffectiveDeleteAfter, "the latest group survives")
}

// Deleting one member must not try to delete a group that still has members
// with deadlines; the group goes once its last member is gone.
func TestCleanupGroupIfEmptyKeepsGroupWithMembers(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	s := Stream{Name: "g"}
	require.NoError(t, h.DB.Create(&s).Error)
	g := Group{StreamID: s.ID, Name: "1"}
	require.NoError(t, h.DB.Create(&g).Error)
	deadline := time.Now().UTC().Add(24 * time.Hour)
	a := MetaResource{Path: "/g/a.bin", GroupID: &g.ID, DeleteAfter: &deadline}
	b := MetaResource{Path: "/g/b.bin", GroupID: &g.ID, DeleteAfter: &deadline}
	require.NoError(t, h.DB.Create(&a).Error)
	require.NoError(t, h.DB.Create(&b).Error)
	for _, p := range []string{"g/a.bin", "g/b.bin"} {
		require.NoError(t, os.MkdirAll(filepath.Join(h.BaseDir, "g"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(h.BaseDir, p), []byte("x"), 0o644))
	}

	require.NoError(t, h.deleteResource(raceLog(), &a, "test"))
	var n int64
	require.NoError(t, h.DB.Model(&Group{}).Where("id = ?", g.ID).Count(&n).Error)
	assert.EqualValues(t, 1, n, "group with a remaining member is kept")

	require.NoError(t, h.deleteResource(raceLog(), &b, "test"))
	require.NoError(t, h.DB.Model(&Group{}).Where("id = ?", g.ID).Count(&n).Error)
	assert.Zero(t, n, "group is removed with its last member")
}

// An older group whose files were all kept (locked) in the janitor review is
// not re-expired by every save in an auto_expire_previous stream; one with an
// unlocked file, or no files at all, still is.
func TestStreamRetentionSkipsFullyLockedOlderGroups(t *testing.T) {
	h := newRaceHandler(t, time.Hour)
	s := Stream{Name: "kept", AutoExpirePrevious: ptr.Of(true)}
	require.NoError(t, h.DB.Create(&s).Error)
	base := time.Now().UTC().Add(-time.Hour)
	mk := func(name string, offset time.Duration) Group {
		g := Group{StreamID: s.ID, Name: name, CreatedAt: base.Add(offset)}
		require.NoError(t, h.DB.Create(&g).Error)
		return g
	}
	locked, mixed, empty, latest := mk("locked", 0), mk("mixed", time.Minute), mk("empty", 2*time.Minute), mk("latest", 3*time.Minute)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/k/l1", GroupID: &locked.ID, Immutable: ptr.Of(true)}).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/k/l2", GroupID: &locked.ID, Immutable: ptr.Of(true)}).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/k/m1", GroupID: &mixed.ID, Immutable: ptr.Of(true)}).Error)
	require.NoError(t, h.DB.Create(&MetaResource{Path: "/k/m2", GroupID: &mixed.ID}).Error)

	r := MetaResource{Path: "/k/new", GroupID: &latest.ID}
	require.NoError(t, r.Save(h.DB, h.Config))

	reload := func(g *Group) { require.NoError(t, h.DB.First(g, g.ID).Error) }
	reload(&locked)
	reload(&mixed)
	reload(&empty)
	assert.Nil(t, locked.EffectiveDeleteAfter, "fully locked group has nothing to expire")
	assert.NotNil(t, mixed.EffectiveDeleteAfter, "unlocked member still expires")
	assert.NotNil(t, empty.EffectiveDeleteAfter, "empty group is still cleaned up")
}
