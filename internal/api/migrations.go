package api

import (
	"fmt"
	"math"
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/kovi/yaar/internal/models"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// wholeDaysFromUpload reports whether expiresAt is a clean whole-day offset
// from createdAt (within a small tolerance to absorb upload-processing jitter).
// Used to recover the original relative expiry form ("7d") during migration.
func wholeDaysFromUpload(createdAt, expiresAt time.Time) (int, bool) {
	const tolerance = 120 * time.Second
	delta := expiresAt.Sub(createdAt)
	if delta <= 0 {
		return 0, false
	}
	days := math.Round(delta.Hours() / 24)
	if days < 1 {
		return 0, false
	}
	ideal := time.Duration(days) * 24 * time.Hour
	if d := delta - ideal; d < -tolerance || d > tolerance {
		return 0, false
	}
	return int(days), true
}

func AutoMigrate(db *gorm.DB) error {
	// SQLite implements DropColumn by rebuilding the whole table (create new,
	// copy rows, drop old). If foreign_keys are ON, dropping the old
	// meta_resources table fires the ON DELETE CASCADE on meta_tags and wipes
	// every tag. Disable FK enforcement for the duration of the migration and
	// restore it afterwards. The pragma must be toggled on the connection
	// outside any transaction — SQLite ignores it mid-transaction.
	if err := db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		return err
	}
	defer func() {
		_ = db.Exec("PRAGMA foreign_keys = ON").Error
	}()

	m := gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{
		{
			ID: "202605311000_extract_stream_and_group",
			Migrate: func(tx *gorm.DB) error {
				logrus.Info("migration 202605311000_extract_stream_and_group: starting")
				// 1. Create the new tables
				if err := tx.AutoMigrate(&Stream{}, &Group{}); err != nil {
					return err
				}

				// Ensure GroupID is present on MetaResource
				type MetaResourceTemp struct {
					ID      uint
					GroupID *uint
				}
				if err := tx.Table("meta_resources").AutoMigrate(&MetaResourceTemp{}); err != nil {
					return err
				}

				// 2. Fetch unique streams and groups in Go
				type OldMeta struct {
					ID               uint   `gorm:"primaryKey"`
					Stream           string `gorm:"column:stream"`
					Group            string `gorm:"column:group"`
					PolicyKeepLatest *bool  `gorm:"column:policy_keep_latest"`
				}

				var oldData []OldMeta
				// We check if the columns exist first. If this is a fresh install, they won't.
				if tx.Migrator().HasColumn(&MetaResource{}, "stream") {
					if err := tx.Table("meta_resources").Select("id, stream, `group`, policy_keep_latest").Find(&oldData).Error; err != nil {
						return err
					}

					// Process in Go
					streams := make(map[string]uint)
					groups := make(map[string]uint)
					var assigned, retained int

					for _, row := range oldData {
						streamName := row.Stream
						groupName := row.Group

						// We only create streams/groups if they actually have values
						if streamName == "" && groupName != "" {
							streamName = "default" // fallback if group exists but stream doesn't
						}

						var streamID uint
						if streamName != "" {
							if id, ok := streams[streamName]; ok {
								streamID = id
							} else {
								var stream Stream
								if err := tx.Where("name = ?", streamName).FirstOrCreate(&stream, Stream{Name: streamName}).Error; err != nil {
									return err
								}
								streamID = stream.ID
								streams[streamName] = streamID
							}
						}

						if groupName != "" && streamID != 0 {
							groupKey := streamName + "|" + groupName
							var groupID uint
							if id, ok := groups[groupKey]; ok {
								groupID = id
							} else {
								var group Group
								if err := tx.Where("name = ? AND stream_id = ?", groupName, streamID).FirstOrCreate(&group, Group{Name: groupName, StreamID: streamID}).Error; err != nil {
									return err
								}
								groupID = group.ID
								groups[groupKey] = groupID
							}

							// Update record
							if err := tx.Table("meta_resources").Where("id = ?", row.ID).Update("group_id", groupID).Error; err != nil {
								return err
							}
							assigned++
						}

						// Migrate PolicyKeepLatest to Stream.RetainLatest
						if row.PolicyKeepLatest != nil && *row.PolicyKeepLatest && streamID != 0 {
							if err := tx.Model(&Stream{}).Where("id = ?", streamID).Update("retain_latest", true).Error; err != nil {
								return err
							}
							retained++
						}
					}

					// Drop old columns. Best effort in SQLite.
					_ = tx.Table("meta_resources").Migrator().DropColumn(&OldMeta{}, "stream")
					_ = tx.Table("meta_resources").Migrator().DropColumn(&OldMeta{}, "group")
					_ = tx.Table("meta_resources").Migrator().DropColumn(&OldMeta{}, "policy_keep_latest")

					logrus.WithFields(logrus.Fields{
						"resources_scanned":   len(oldData),
						"streams_created":     len(streams),
						"groups_created":      len(groups),
						"resources_assigned":  assigned,
						"retain_latest_flags": retained,
					}).Info("migration 202605311000_extract_stream_and_group: extracted streams and groups")
				} else {
					logrus.Info("migration 202605311000_extract_stream_and_group: no legacy stream column, nothing to extract")
				}

				return nil
			},
			Rollback: func(tx *gorm.DB) error {
				return nil
			},
		},
		{
			ID: "202606261200_migrate_expiry",
			Migrate: func(tx *gorm.DB) error {
				logrus.Info("migration 202606261200_migrate_expiry: starting")
				// Old schema stored expiry in the `expires_at` column. The new
				// model renamed it to `expiry_at` (ExpiryAt). Earlier migrations
				// never carried the value over, so absolute expiry dates were
				// silently lost. Recover them here.

				// Ensure the new columns exist before we write to them.
				type MetaResourceExpiryTemp struct {
					ID                uint
					ExpiryAt          *time.Time
					ExpiryAfterUpload *string `gorm:"type:text"`
					DeleteAfter       *time.Time
				}
				if err := tx.Table("meta_resources").AutoMigrate(&MetaResourceExpiryTemp{}); err != nil {
					return err
				}

				// Fresh install or already-migrated DB: nothing to recover.
				if !tx.Migrator().HasColumn(&MetaResource{}, "expires_at") {
					logrus.Info("migration 202606261200_migrate_expiry: no legacy expires_at column, nothing to recover")
					return nil
				}

				type OldExpiry struct {
					ID        uint       `gorm:"column:id"`
					ExpiresAt *time.Time `gorm:"column:expires_at"`
					CreatedAt time.Time  `gorm:"column:created_at"`
				}
				var rows []OldExpiry
				if err := tx.Table("meta_resources").
					Select("id, expires_at, created_at").
					Where("expires_at IS NOT NULL").
					Find(&rows).Error; err != nil {
					return err
				}

				var relative, absolute, skipped int
				for _, row := range rows {
					// Skip Go zero-time values that linger as non-NULL.
					if row.ExpiresAt == nil || row.ExpiresAt.IsZero() {
						skipped++
						continue
					}

					r := MetaResource{ID: row.ID, CreatedAt: row.CreatedAt}

					// Try to recover the original relative form ("7d", "14d", ...)
					// by inspecting expires_at - created_at. Only treat it as
					// relative when it snaps cleanly to a whole number of days;
					// otherwise keep it as an absolute deadline. This avoids
					// inventing a relative policy from a value that drifted.
					if days, ok := wholeDaysFromUpload(row.CreatedAt, *row.ExpiresAt); ok {
						r.ExpiryAfterUpload = ptr.Of(fmt.Sprintf("%dd", days))
						relative++
					} else {
						r.ExpiryAt = row.ExpiresAt
						absolute++
					}

					// Derive DeleteAfter from the policy fields the same way the
					// rest of the app does, so the janitor enforces it correctly.
					r.Compute()

					if err := tx.Table("meta_resources").
						Where("id = ?", row.ID).
						Updates(map[string]any{
							"expiry_at":           r.ExpiryAt,
							"expiry_after_upload": r.ExpiryAfterUpload,
							"delete_after":        r.DeleteAfter,
						}).Error; err != nil {
						return err
					}
				}

				// Drop the obsolete column. Best effort in SQLite.
				_ = tx.Table("meta_resources").Migrator().DropColumn(&OldExpiry{}, "expires_at")

				logrus.WithFields(logrus.Fields{
					"candidates":         len(rows),
					"recovered_relative": relative,
					"recovered_absolute": absolute,
					"skipped_zero_time":  skipped,
				}).Info("migration 202606261200_migrate_expiry: recovered expiry deadlines")

				return nil
			},
			Rollback: func(tx *gorm.DB) error {
				return nil
			},
		},
	})

	if err := m.Migrate(); err != nil {
		return err
	}

	return db.AutoMigrate(
		&Stream{},
		&Group{},
		&MetaResource{},
		&MetaTag{},
		&models.User{},
		&models.Token{},
		&models.SystemState{},
	)
}
