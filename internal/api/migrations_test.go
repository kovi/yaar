package api_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kovi/yaar/internal/api"
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

	require.NoError(t, api.AutoMigrate(db))

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

	require.NoError(t, api.AutoMigrate(db), "migration should run without errors")

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
	require.NoError(t, api.AutoMigrate(db))

	// The new schema should exist with expiry_at, and no legacy column.
	assert.True(t, db.Migrator().HasColumn(&api.MetaResource{}, "expiry_at"))
	assert.False(t, db.Migrator().HasColumn("meta_resources", "expires_at"))

	// And it should be idempotent: running again changes nothing.
	require.NoError(t, api.AutoMigrate(db))
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

	require.NoError(t, api.AutoMigrate(db))

	r := resourceByPath(t, db, "/lumon/artifact.tar.gz")

	// Stream/group extraction.
	require.NotNil(t, r.GroupID, "row should be assigned to a group")
	var group api.Group
	require.NoError(t, db.First(&group, *r.GroupID).Error)
	assert.Equal(t, "17083", group.Name)
	var stream api.Stream
	require.NoError(t, db.First(&stream, group.StreamID).Error)
	assert.Equal(t, "lumon:lib:master", stream.Name)
	require.NotNil(t, stream.RetainLatest)
	assert.True(t, *stream.RetainLatest, "policy_keep_latest should map to stream RetainLatest")

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

	err = api.AutoMigrate(db)
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
	assert.NotNil(t, stream1.RetainLatest)
	assert.True(t, *stream1.RetainLatest, "Stream 'movies' should have RetainLatest set to true because file1 had PolicyKeepLatest=true")

	// Verify 'music' stream does not have RetainLatest set to true
	var file3 api.MetaResource
	db.Table("meta_resources").First(&file3, "path = ?", "/file3.txt")
	var group3 api.Group
	db.First(&group3, file3.GroupID)
	var stream3 api.Stream
	db.First(&stream3, group3.StreamID)
	assert.Equal(t, "music", stream3.Name)
	if stream3.RetainLatest != nil {
		assert.False(t, *stream3.RetainLatest, "Stream 'music' should not have RetainLatest=true")
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
