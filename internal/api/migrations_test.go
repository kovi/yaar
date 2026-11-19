package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/models"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// freshDB opens an isolated in-memory SQLite database. Each call uses a unique
// name so tests in this package don't share state via cache=shared.
func freshDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	return db
}

// oldExpirySchema mirrors the pre-migration meta_resources table, including the
// legacy `expires_at` column (renamed to `expiry_at` in the new model) and the
// stream/group/policy_keep_latest columns the first migration consumes.
type oldExpirySchema struct {
	ID               uint   `gorm:"primaryKey"`
	Path             string `gorm:"type:text;not null;uniqueIndex"`
	Stream           string
	Group            string
	PolicyKeepLatest *bool
	ExpiresAt        *time.Time `gorm:"column:expires_at"`
	Immutable        *bool
	CreatedAt        time.Time
}

func (oldExpirySchema) TableName() string { return "meta_resources" }

// resourceByPath loads a migrated MetaResource by path.
func resourceByPath(t *testing.T, db *gorm.DB, path string) api.MetaResource {
	t.Helper()
	var r api.MetaResource
	require.NoError(t, db.Table("meta_resources").First(&r, "path = ?", path).Error)
	return r
}

// TestMigrateTagsSurviveWithForeignKeysOn guards against the cascade-delete
// footgun: SQLite implements DropColumn by rebuilding meta_resources, and with
// foreign_keys=ON the ON DELETE CASCADE on meta_tags would wipe every tag.
// The real app runs with foreign_keys=ON, so the test does too.
func TestMigrateTagsSurviveWithForeignKeysOn(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)

	// Old meta_resources with a column the migration will drop, forcing the
	// SQLite table rebuild that triggers the cascade.
	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))
	// Create meta_tags with the ON DELETE CASCADE FK exactly as the old DB had
	// it, so the cascade can fire during the column-drop table rebuild.
	require.NoError(t, db.Exec(
		"CREATE TABLE `meta_tags` (`id` integer PRIMARY KEY AUTOINCREMENT,"+
			"`resource_id` integer,`key` text,`value` text,"+
			"CONSTRAINT `fk_meta_resources_tags` FOREIGN KEY (`resource_id`) "+
			"REFERENCES `meta_resources`(`id`) ON DELETE CASCADE)").Error)

	rows := []oldExpirySchema{
		{ID: 1, Path: "/a", Stream: "s", Group: "g"},
		{ID: 2, Path: "/b", Stream: "s", Group: "g"},
	}
	require.NoError(t, db.Table("meta_resources").Create(&rows).Error)

	require.NoError(t, db.Exec(
		"INSERT INTO meta_tags (resource_id, key, value) VALUES (1,'uploader','mark.pal'),(1,'branch','master'),(2,'uploader','helly.r')").Error)

	require.NoError(t, api.AutoMigrate(db, nil))

	var tagCount int64
	require.NoError(t, db.Table("meta_tags").Count(&tagCount).Error)
	assert.Equal(t, int64(3), tagCount, "tags must survive the column-drop table rebuild")

	// Tags must still point at the right resources.
	var r1 api.MetaResource
	require.NoError(t, db.Preload("Tags").First(&r1, "path = ?", "/a").Error)
	assert.Len(t, r1.Tags, 2)

	// And foreign-key enforcement must be left ON for normal operation.
	var fk int
	require.NoError(t, db.Raw("PRAGMA foreign_keys").Scan(&fk).Error)
	assert.Equal(t, 1, fk, "foreign_keys must be restored after migration")
}

func TestMigrateExpiry(t *testing.T) {
	db := freshDB(t)

	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))

	now := time.Now().UTC().Truncate(time.Second)

	// A clean whole-day offset from created_at -> recovered as relative "7d".
	clean7d := now.Add(7 * 24 * time.Hour)
	// Slight upload jitter (within tolerance) still snaps to "14d".
	jitter14d := now.Add(14*24*time.Hour + 30*time.Second)
	// Drifted ~6h past 7d -> outside tolerance, kept as absolute.
	drifted := now.Add(7*24*time.Hour + 6*time.Hour)
	// Already-expired (in the past) but a clean 5-day offset -> still recovered.
	pastCreated := now.Add(-30 * 24 * time.Hour)
	pastExpires := pastCreated.Add(5 * 24 * time.Hour)
	// expires_at == created_at (zero delta) -> absolute, never "0d".
	zeroDelta := now

	rows := []oldExpirySchema{
		{ID: 1, Path: "/clean7d", CreatedAt: now, ExpiresAt: &clean7d},
		{ID: 2, Path: "/jitter14d", CreatedAt: now, ExpiresAt: &jitter14d},
		{ID: 3, Path: "/drifted", CreatedAt: now, ExpiresAt: &drifted},
		{ID: 4, Path: "/past", CreatedAt: pastCreated, ExpiresAt: &pastExpires},
		{ID: 5, Path: "/zerodelta", CreatedAt: now, ExpiresAt: &zeroDelta},
		{ID: 6, Path: "/noexpiry", CreatedAt: now, ExpiresAt: nil},
	}
	require.NoError(t, db.Table("meta_resources").Create(&rows).Error)

	require.NoError(t, api.AutoMigrate(db, nil), "migration should run without errors")

	// The legacy column must be gone.
	assert.False(t, db.Migrator().HasColumn("meta_resources", "expires_at"),
		"legacy expires_at column should be dropped")

	// 1: clean 7d -> relative after_upload, delete_after = created + 7d.
	r1 := resourceByPath(t, db, "/clean7d")
	require.NotNil(t, r1.ExpiryAfterUpload)
	assert.Equal(t, "7d", *r1.ExpiryAfterUpload)
	assert.Nil(t, r1.ExpiryAt, "relative recovery should not also set absolute ExpiryAt")
	require.NotNil(t, r1.DeleteAfter)
	assert.WithinDuration(t, clean7d, *r1.DeleteAfter, time.Minute)

	// 2: jitter within tolerance -> "14d".
	r2 := resourceByPath(t, db, "/jitter14d")
	require.NotNil(t, r2.ExpiryAfterUpload)
	assert.Equal(t, "14d", *r2.ExpiryAfterUpload)

	// 3: drifted beyond tolerance -> absolute ExpiryAt.
	r3 := resourceByPath(t, db, "/drifted")
	assert.Nil(t, r3.ExpiryAfterUpload, "drifted offset should not be treated as relative")
	require.NotNil(t, r3.ExpiryAt)
	assert.WithinDuration(t, drifted, *r3.ExpiryAt, time.Second)
	require.NotNil(t, r3.DeleteAfter)
	assert.WithinDuration(t, drifted, *r3.DeleteAfter, time.Second)

	// 4: already-expired clean 5d -> still recovered as relative.
	r4 := resourceByPath(t, db, "/past")
	require.NotNil(t, r4.ExpiryAfterUpload)
	assert.Equal(t, "5d", *r4.ExpiryAfterUpload)
	require.NotNil(t, r4.DeleteAfter, "past deadlines must still be recovered for the janitor")
	assert.True(t, r4.DeleteAfter.Before(now), "recovered deadline should be in the past")

	// 5: zero delta -> absolute, never "0d".
	r5 := resourceByPath(t, db, "/zerodelta")
	assert.Nil(t, r5.ExpiryAfterUpload, "zero delta must not produce a 0d relative policy")
	require.NotNil(t, r5.ExpiryAt)
	assert.WithinDuration(t, zeroDelta, *r5.ExpiryAt, time.Second)

	// 6: no expiry -> nothing set.
	r6 := resourceByPath(t, db, "/noexpiry")
	assert.Nil(t, r6.ExpiryAfterUpload)
	assert.Nil(t, r6.ExpiryAt)
	assert.Nil(t, r6.DeleteAfter)
}

// TestMigrateExpiryFreshInstall ensures the migration is a no-op when there is
// no legacy expires_at column (a brand new database).
func TestMigrateExpiryFreshInstall(t *testing.T) {
	db := freshDB(t)

	// Run migrations on an empty DB (no pre-existing tables/columns).
	require.NoError(t, api.AutoMigrate(db, nil))

	// The new schema should exist with expiry_at, and no legacy column.
	assert.True(t, db.Migrator().HasColumn(&api.MetaResource{}, "expiry_at"))
	assert.False(t, db.Migrator().HasColumn("meta_resources", "expires_at"))

	// And it should be idempotent: running again changes nothing.
	require.NoError(t, api.AutoMigrate(db, nil))
}

// TestMigrateExpiryWithStreamAndGroup verifies the expiry migration composes
// with the stream/group extraction migration on the same row — the real DB has
// hundreds of rows carrying stream, group, and expires_at together.
func TestMigrateExpiryWithStreamAndGroup(t *testing.T) {
	db := freshDB(t)

	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))

	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(7 * 24 * time.Hour)
	trueVal := true

	rows := []oldExpirySchema{
		{
			ID: 1, Path: "/lumon/artifact.tar.gz",
			Stream: "lumon:lib:master", Group: "17083",
			PolicyKeepLatest: &trueVal,
			CreatedAt:        now, ExpiresAt: &expires,
			Immutable: &trueVal,
		},
	}
	require.NoError(t, db.Table("meta_resources").Create(&rows).Error)

	require.NoError(t, api.AutoMigrate(db, nil))

	r := resourceByPath(t, db, "/lumon/artifact.tar.gz")

	// Stream/group extraction.
	require.NotNil(t, r.GroupID, "row should be assigned to a group")
	var group api.Group
	require.NoError(t, db.First(&group, *r.GroupID).Error)
	assert.Equal(t, "17083", group.Name)
	var stream api.Stream
	require.NoError(t, db.First(&stream, group.StreamID).Error)
	assert.Equal(t, "lumon:lib:master", stream.Name)
	require.NotNil(t, stream.AutoExpirePrevious)
	assert.True(t, *stream.AutoExpirePrevious, "policy_keep_latest should map to stream AutoExpirePrevious")
	assert.Nil(t, stream.RetainLatest, "policy_keep_latest must not map to RetainLatest")

	// Expiry recovery on the same row.
	require.NotNil(t, r.ExpiryAfterUpload)
	assert.Equal(t, "7d", *r.ExpiryAfterUpload)
	require.NotNil(t, r.DeleteAfter)
	assert.WithinDuration(t, expires, *r.DeleteAfter, time.Minute)

	// Immutable flag survives by column name.
	require.NotNil(t, r.Immutable)
	assert.True(t, *r.Immutable)
}

func TestExtractStreamAndGroupMigration(t *testing.T) {
	// 1. Setup an in-memory SQLite database for testing
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	// --- SETUP "BEFORE" STATE ---

	// Define the OLD schema exactly as it was before the migration
	type OldMetaResource struct {
		ID               uint   `gorm:"primaryKey"`
		Path             string `gorm:"type:text;not null;uniqueIndex"`
		Stream           string
		Group            string
		PolicyKeepLatest *bool
	}

	// Create the old table
	err = db.Table("meta_resources").AutoMigrate(&OldMetaResource{})
	require.NoError(t, err)

	// Insert mock data representing the database before migration
	trueVal := true
	oldData := []OldMetaResource{
		{ID: 1, Path: "/file1.txt", Stream: "movies", Group: "media", PolicyKeepLatest: &trueVal},
		{ID: 2, Path: "/file2.txt", Stream: "movies", Group: "media"}, // Duplicate stream/group
		{ID: 3, Path: "/file3.txt", Stream: "music", Group: "media"},
		{ID: 4, Path: "/file4.txt", Stream: "", Group: ""},           // Null/empty values
		{ID: 5, Path: "/file5.txt", Stream: "", Group: "standalone"}, // Group without stream
		{ID: 6, Path: "/file6.txt", Stream: "docs", Group: ""},       // Stream without group
	}
	err = db.Table("meta_resources").Create(&oldData).Error
	require.NoError(t, err)

	// --- RUN MIGRATION ---

	err = api.AutoMigrate(db, nil)
	require.NoError(t, err, "Migration should run without errors")

	// --- ASSERT "AFTER" STATE ---

	var streams []api.Stream
	db.Find(&streams)
	// We expect movies, music, default, docs
	assert.Len(t, streams, 4, "Should have created exactly 4 unique streams (movies, music, default, docs)")

	var groups []api.Group
	db.Find(&groups)
	// We expect media (movies), media (music), standalone (default)
	assert.Len(t, groups, 3, "Should have created exactly 3 unique groups")

	// Verify the records point to the correct IDs
	var file1 api.MetaResource
	db.Table("meta_resources").First(&file1, "path = ?", "/file1.txt")
	require.NotNil(t, file1.GroupID)

	var group1 api.Group
	db.First(&group1, file1.GroupID)
	assert.Equal(t, "media", group1.Name)

	var stream1 api.Stream
	db.First(&stream1, group1.StreamID)
	assert.Equal(t, "movies", stream1.Name)
	// file1 is keep-latest but file2 in the same stream is not: converting
	// would let the next upload expire file2, which eit-1 never did.
	assert.False(t, ptr.Val(stream1.AutoExpirePrevious), "Stream 'movies' mixes keep-latest and plain files, so AutoExpirePrevious must stay unset")

	// Verify 'music' stream does not have AutoExpirePrevious set to true
	var file3 api.MetaResource
	db.Table("meta_resources").First(&file3, "path = ?", "/file3.txt")
	var group3 api.Group
	db.First(&group3, file3.GroupID)
	var stream3 api.Stream
	db.First(&stream3, group3.StreamID)
	assert.Equal(t, "music", stream3.Name)
	if stream3.AutoExpirePrevious != nil {
		assert.False(t, *stream3.AutoExpirePrevious, "Stream 'music' should not have AutoExpirePrevious=true")
	}

	// file 4: empty
	var file4 api.MetaResource
	db.Table("meta_resources").First(&file4, "path = ?", "/file4.txt")
	assert.Nil(t, file4.GroupID, "Empty group should result in nil GroupID")

	// file 5: group but no stream -> should map to 'default' stream
	var file5 api.MetaResource
	db.Table("meta_resources").First(&file5, "path = ?", "/file5.txt")
	require.NotNil(t, file5.GroupID)
	var group5 api.Group
	db.First(&group5, file5.GroupID)
	assert.Equal(t, "standalone", group5.Name)
	var stream5 api.Stream
	db.First(&stream5, group5.StreamID)
	assert.Equal(t, "default", stream5.Name)

	// file 6: stream but no group -> should result in nil GroupID, but stream 'docs' should exist
	var file6 api.MetaResource
	db.Table("meta_resources").First(&file6, "path = ?", "/file6.txt")
	assert.Nil(t, file6.GroupID)

	// Verify the old columns were dropped
	var columnTypes []string
	cols, _ := db.Migrator().ColumnTypes("meta_resources")
	for _, c := range cols {
		columnTypes = append(columnTypes, c.Name())
	}
	assert.NotContains(t, columnTypes, "stream", "Old stream column should be dropped")
	assert.NotContains(t, columnTypes, "group", "Old group column should be dropped")
	assert.NotContains(t, columnTypes, "policy_keep_latest", "Old policy_keep_latest column should be dropped")
}

// TestMigrateBackfillsGroupDeadlines covers the eit-1 upgrade gap: the janitor
// deletes grouped files only via Group.EffectiveDeleteAfter, which the earlier
// migrations never set, so migrated groups were kept forever.
func TestMigrateBackfillsGroupDeadlines(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))

	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour
	trueVal := true
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }

	rows := []oldExpirySchema{
		// Finished feature branch: 14d expiry long past, no further uploads.
		{ID: 1, Path: "/feat/a.bin", Stream: "feat", Group: "1", CreatedAt: now.Add(-30 * day), ExpiresAt: at(-16 * day)},
		{ID: 2, Path: "/feat/b.bin", Stream: "feat", Group: "1", CreatedAt: now.Add(-30 * day), ExpiresAt: at(-16 * day)},
		// Active branch: expiry still in the future.
		{ID: 3, Path: "/live/a.bin", Stream: "live", Group: "9", CreatedAt: now.Add(-2 * day), ExpiresAt: at(12 * day)},
		// Group with a file that never expires: group must stay without deadline.
		{ID: 4, Path: "/keep/a.bin", Stream: "keep", Group: "1", CreatedAt: now.Add(-2 * day), ExpiresAt: at(12 * day)},
		{ID: 5, Path: "/keep/b.bin", Stream: "keep", Group: "1", CreatedAt: now.Add(-2 * day)},
		// Immutable member: group must stay without deadline.
		{ID: 6, Path: "/lock/a.bin", Stream: "lock", Group: "1", CreatedAt: now.Add(-30 * day), ExpiresAt: at(-16 * day), Immutable: &trueVal},
		// Keep-latest stream, inserted newest-first so row order != upload order.
		// The old group was already expired by eit-1 rotation (non-whole-day offset).
		{ID: 7, Path: "/master/new.bin", Stream: "master", Group: "200", PolicyKeepLatest: &trueVal, CreatedAt: now.Add(-1 * day)},
		{ID: 8, Path: "/master/old.bin", Stream: "master", Group: "100", PolicyKeepLatest: &trueVal, CreatedAt: now.Add(-5 * day), ExpiresAt: at(-1*day + 37*time.Second)},
	}
	require.NoError(t, db.Table("meta_resources").Create(&rows).Error)

	require.NoError(t, api.AutoMigrate(db, nil))

	group := func(path string) api.Group {
		t.Helper()
		r := resourceByPath(t, db, path)
		require.NotNil(t, r.GroupID)
		var g api.Group
		require.NoError(t, db.Preload("Stream").First(&g, *r.GroupID).Error)
		return g
	}

	feat := group("/feat/a.bin")
	require.NotNil(t, feat.EffectiveDeleteAfter, "expired migrated group must get a deadline")
	assert.False(t, feat.EffectiveDeleteAfter.After(time.Now()), "expired group deadline should be due")
	assert.WithinDuration(t, now.Add(-30*day), feat.CreatedAt, time.Second, "group created_at should come from its oldest member")

	live := group("/live/a.bin")
	require.NotNil(t, live.EffectiveDeleteAfter)
	assert.WithinDuration(t, now.Add(12*day), *live.EffectiveDeleteAfter, time.Minute)

	assert.Nil(t, group("/keep/a.bin").EffectiveDeleteAfter, "group with a non-expiring file keeps no deadline")
	assert.Nil(t, group("/lock/a.bin").EffectiveDeleteAfter, "group with an immutable file keeps no deadline")

	newest := group("/master/new.bin")
	oldest := group("/master/old.bin")
	assert.True(t, newest.CreatedAt.After(oldest.CreatedAt), "latest group must follow upload time, not row order")
	assert.Nil(t, newest.Stream.RetainLatest)
	require.NotNil(t, newest.Stream.AutoExpirePrevious)
	assert.True(t, *newest.Stream.AutoExpirePrevious)
	assert.Nil(t, newest.EffectiveDeleteAfter, "newest keep-latest group keeps its own (absent) expiry")
	require.NotNil(t, oldest.EffectiveDeleteAfter)
	assert.False(t, oldest.EffectiveDeleteAfter.After(time.Now()), "rotated-out group should be due for deletion")
}

// TestBackfillLeavesRetainLatestHead checks the backfill respects a
// RetainLatest stream configured on main: its latest group is never given a
// deadline from member expiry, while older groups are.
func TestBackfillLeavesRetainLatestHead(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, api.AutoMigrate(db, nil))

	trueVal := true
	s := api.Stream{Name: "rl", RetainLatest: &trueVal}
	require.NoError(t, db.Create(&s).Error)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	g1 := api.Group{StreamID: s.ID, Name: "1", CreatedAt: now.Add(-48 * time.Hour)}
	g2 := api.Group{StreamID: s.ID, Name: "2", CreatedAt: now.Add(-24 * time.Hour)}
	require.NoError(t, db.Create(&g1).Error)
	require.NoError(t, db.Create(&g2).Error)
	require.NoError(t, db.Create(&api.MetaResource{Path: "/rl/1", GroupID: &g1.ID, DeleteAfter: &past}).Error)
	require.NoError(t, db.Create(&api.MetaResource{Path: "/rl/2", GroupID: &g2.ID, DeleteAfter: &past}).Error)

	// Re-run just the backfill migration.
	require.NoError(t, db.Exec("DELETE FROM migrations WHERE id = ?", "202611111200_backfill_group_deadlines").Error)
	require.NoError(t, api.AutoMigrate(db, nil))

	require.NoError(t, db.First(&g1, g1.ID).Error)
	require.NoError(t, db.First(&g2, g2.ID).Error)
	assert.NotNil(t, g1.EffectiveDeleteAfter, "older group gets its members' deadline")
	assert.Nil(t, g2.EffectiveDeleteAfter, "latest group of a RetainLatest stream keeps no deadline")
}

func janitorSuspendedState(t *testing.T, db *gorm.DB) *models.SystemState {
	t.Helper()
	var states []models.SystemState
	require.NoError(t, db.Where("key = ?", "janitor_suspended").Find(&states).Error)
	if len(states) == 0 {
		return nil
	}
	return &states[0]
}

// A migration that rewrites retention data must leave the janitor suspended so
// an admin can review what it is about to delete.
func TestMigrateSuspendsJanitorOnLegacyData(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))
	now := time.Now().UTC()
	exp := now.Add(-time.Hour)
	require.NoError(t, db.Table("meta_resources").Create(&oldExpirySchema{
		ID: 1, Path: "/a", Stream: "s", Group: "1", CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: &exp,
	}).Error)

	require.NoError(t, api.AutoMigrate(db, nil))
	st := janitorSuspendedState(t, db)
	require.NotNil(t, st, "janitor must be suspended after migrating legacy data")
	assert.Contains(t, st.Value, "migration")

	// Re-running AutoMigrate (next start) must not re-suspend after a resume.
	require.NoError(t, api.ResumeJanitor(db))
	require.NoError(t, api.AutoMigrate(db, nil))
	assert.Nil(t, janitorSuspendedState(t, db), "no migration ran, janitor stays resumed")
}

func TestMigrateFreshInstallDoesNotSuspendJanitor(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, api.AutoMigrate(db, nil))
	assert.Nil(t, janitorSuspendedState(t, db))
}

// Only streams whose every file was keep-latest become AutoExpirePrevious.
func TestMigrateMixedKeepLatestStream(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))
	trueVal := true
	now := time.Now().UTC()
	require.NoError(t, db.Table("meta_resources").Create(&[]oldExpirySchema{
		{ID: 1, Path: "/nav/0.1.0.zip", Stream: "nav", Group: "0.1.0", CreatedAt: now.Add(-24 * time.Hour)},
		{ID: 2, Path: "/nav/abc.zip", Stream: "nav", Group: "abc", PolicyKeepLatest: &trueVal, CreatedAt: now},
		{ID: 3, Path: "/ci/x.zip", Stream: "ci", Group: "1", PolicyKeepLatest: &trueVal, CreatedAt: now},
	}).Error)
	require.NoError(t, api.AutoMigrate(db, nil))

	var nav, ci api.Stream
	require.NoError(t, db.Where("name = ?", "nav").First(&nav).Error)
	require.NoError(t, db.Where("name = ?", "ci").First(&ci).Error)
	assert.False(t, ptr.Val(nav.AutoExpirePrevious), "mixed stream must not expire its unflagged groups")
	assert.True(t, ptr.Val(ci.AutoExpirePrevious))
	assert.Nil(t, nav.RetainLatest)
}

// legacyDownloadModeSchema is the pre-streams meta_resources with its
// per-directory batch download mode.
type legacyDownloadModeSchema struct {
	ID           uint   `gorm:"primaryKey"`
	Path         string `gorm:"type:text;not null;uniqueIndex"`
	Type         string `gorm:"type:text;not null;default:'file'"`
	DownloadMode string `gorm:"column:download_mode;type:text;default:'literal'"`
	CreatedAt    time.Time
}

func (legacyDownloadModeSchema) TableName() string { return "meta_resources" }

// The per-directory download mode has no equivalent any more. The migration
// must drop it visibly: every directory that was "merge" (the only value that
// changed behaviour) is recorded in the audit log.
func TestMigrateDropsDownloadModeAndAuditsMergeDirs(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.AutoMigrate(&legacyDownloadModeSchema{}))
	require.NoError(t, db.Create(&[]legacyDownloadModeSchema{
		{Path: "/releases", Type: "dir", DownloadMode: "merge"},
		{Path: "/nightly", Type: "dir", DownloadMode: "literal"},
		{Path: "/tools", Type: "dir", DownloadMode: " MERGE "},
		{Path: "/releases/a.bin", Type: "file", DownloadMode: "literal"},
	}).Error)

	logPath := filepath.Join(t.TempDir(), "audit.log")
	aud, err := audit.NewAuditor(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { aud.Close() })

	require.NoError(t, api.AutoMigrate(db, aud))

	assert.False(t, db.Migrator().HasColumn(&api.MetaResource{}, "download_mode"), "column should be dropped")
	var count int64
	db.Model(&api.MetaResource{}).Count(&count)
	assert.EqualValues(t, 4, count, "dropping the column must keep every row")

	logData, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(logData)
	assert.Contains(t, log, `"resource":"/releases"`)
	assert.Contains(t, log, `"resource":"/tools"`)
	assert.NotContains(t, log, `"resource":"/nightly"`)
	assert.Equal(t, 2, strings.Count(log, "migration_download_mode_dropped"))

	// Not a retention change: the janitor keeps running.
	var states []models.SystemState
	db.Where("key = ?", "janitor_suspended").Find(&states)
	assert.Empty(t, states)

	// Idempotent: a second start does nothing.
	require.NoError(t, api.AutoMigrate(db, aud))
	logData, _ = os.ReadFile(logPath)
	assert.Equal(t, 2, strings.Count(string(logData), "migration_download_mode_dropped"))
}

// legacyNoPathSchema is a legacy table the full model cannot be applied to
// (adding the NOT NULL path column to existing rows fails), so the migrations
// after the stream/group extraction error out.
type legacyNoPathSchema struct {
	ID               uint `gorm:"primaryKey"`
	Stream           string
	Group            string
	PolicyKeepLatest *bool
	CreatedAt        time.Time
}

func (legacyNoPathSchema) TableName() string { return "meta_resources" }

// gormigrate records each migration as soon as it finishes. If a later step
// fails, the restart skips the migrations that already changed retention data,
// so the suspension must be written by those migrations, not at the end.
func TestMigrateSuspensionSurvivesLaterFailure(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.AutoMigrate(&legacyNoPathSchema{}))
	require.NoError(t, db.Create(&legacyNoPathSchema{ID: 1, Stream: "s", Group: "1", CreatedAt: time.Now().UTC()}).Error)

	require.Error(t, api.AutoMigrate(db, nil), "the injected schema problem must fail a later migration")

	var applied []string
	require.NoError(t, db.Table("migrations").Pluck("id", &applied).Error)
	require.Contains(t, applied, "202605311000_extract_stream_and_group", "the extraction itself succeeded and is recorded")

	st := janitorSuspendedState(t, db)
	require.NotNil(t, st, "janitor must stay suspended although the migration run failed")
	assert.Equal(t, "migration: migrated legacy streams and groups", st.Value)
}

// A migration suspending an already suspended janitor keeps the admin's reason
// and still leads with "migration: " so the UI offers the review flow.
func TestMigrateKeepsAdminSuspensionReason(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.AutoMigrate(&models.SystemState{}))
	require.NoError(t, api.SuspendJanitor(db, "release freeze"))
	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))
	now := time.Now().UTC()
	exp := now.Add(-time.Hour)
	require.NoError(t, db.Table("meta_resources").Create(&oldExpirySchema{
		ID: 1, Path: "/a", Stream: "s", Group: "1", CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: &exp,
	}).Error)

	require.NoError(t, api.AutoMigrate(db, nil))

	st := janitorSuspendedState(t, db)
	require.NotNil(t, st)
	assert.True(t, strings.HasPrefix(st.Value, "migration: "), st.Value)
	assert.Contains(t, st.Value, "migrated legacy streams and groups")
	assert.Contains(t, st.Value, "recovered legacy expiry dates")
	assert.True(t, strings.HasSuffix(st.Value, "; suspended before migration: release freeze"), st.Value)
	assert.Equal(t, 1, strings.Count(st.Value, "migrated legacy streams and groups"), "reasons are not repeated")
}

// The suspension a migration writes is audited like an admin's, so the janitor
// log shows when and why the janitor stopped, not only the later resume.
func TestMigrateSuspensionIsAudited(t *testing.T) {
	db := freshDB(t)
	require.NoError(t, db.Table("meta_resources").AutoMigrate(&oldExpirySchema{}))
	now := time.Now().UTC()
	exp := now.Add(-time.Hour)
	require.NoError(t, db.Table("meta_resources").Create(&oldExpirySchema{
		ID: 1, Path: "/a", Stream: "s", Group: "1", CreatedAt: now.Add(-48 * time.Hour), ExpiresAt: &exp,
	}).Error)

	logPath := filepath.Join(t.TempDir(), "audit.log")
	aud, err := audit.NewAuditor(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { aud.Close() })

	require.NoError(t, api.AutoMigrate(db, aud))

	page, err := aud.ReadPage(audit.ReadOptions{BeforeOffset: -1, Limit: 50, AnyOf: []string{"janitor_"}})
	require.NoError(t, err)
	var reasons []string
	for _, e := range page.Entries {
		assert.Equal(t, "janitor_suspend", e["operation"])
		assert.Equal(t, "system", e["user"])
		reasons = append(reasons, e["migration_reason"].(string))
	}
	assert.ElementsMatch(t, []string{
		"migrated legacy streams and groups",
		"recovered legacy expiry dates",
		"backfilled group deletion deadlines",
	}, reasons, "one entry per migration that suspended")

	// A second start runs no migration and audits nothing new.
	require.NoError(t, api.AutoMigrate(db, aud))
	page, err = aud.ReadPage(audit.ReadOptions{BeforeOffset: -1, Limit: 50, AnyOf: []string{"janitor_"}})
	require.NoError(t, err)
	assert.Len(t, page.Entries, len(reasons))
}
