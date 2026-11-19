package api

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/kovi/yaar/internal/audit"
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

// AutoMigrate brings the schema and data up to date. auditor, when non-nil,
// records data changes a migration made that an admin should know about.
func AutoMigrate(db *gorm.DB, auditor *audit.Auditor) error {
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

	// Set by any migration that changed what the janitor will delete. The
	// janitor is then suspended until an admin has reviewed the pending
	// deletions and resumed it; see janitor_review.go.
	var suspendReasons []string

	// Directories whose per-directory batch download mode was "merge"; recorded
	// in the audit log once the migrations have committed.
	var mergeDirs []string

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
					ID               uint      `gorm:"primaryKey"`
					Stream           string    `gorm:"column:stream"`
					Group            string    `gorm:"column:group"`
					PolicyKeepLatest *bool     `gorm:"column:policy_keep_latest"`
					CreatedAt        time.Time `gorm:"column:created_at"`
				}

				var oldData []OldMeta
				// We check if the columns exist first. If this is a fresh install, they won't.
				if tx.Migrator().HasColumn(&MetaResource{}, "stream") {
					cols := "id, stream, `group`, policy_keep_latest"
					if tx.Migrator().HasColumn(&MetaResource{}, "created_at") {
						cols += ", created_at"
					}
					if err := tx.Table("meta_resources").Select(cols).Find(&oldData).Error; err != nil {
						return err
					}

					// Process in Go
					streams := make(map[string]uint)
					groups := make(map[string]uint)
					// Earliest member upload per group. Groups are created here at
					// migration time, which would make every migrated group look
					// equally new and leave "latest group in stream" to row order.
					groupCreated := make(map[uint]time.Time)
					// Per stream: rows total and rows flagged keep-latest.
					type keepCount struct{ total, keep int }
					keepLatest := make(map[uint]*keepCount)
					var assigned int

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

							if !row.CreatedAt.IsZero() {
								if t, ok := groupCreated[groupID]; !ok || row.CreatedAt.Before(t) {
									groupCreated[groupID] = row.CreatedAt
								}
							}

							// Update record
							if err := tx.Table("meta_resources").Where("id = ?", row.ID).Update("group_id", groupID).Error; err != nil {
								return err
							}
							assigned++
						}

						if streamID != 0 {
							kc := keepLatest[streamID]
							if kc == nil {
								kc = &keepCount{}
								keepLatest[streamID] = kc
							}
							kc.total++
							if row.PolicyKeepLatest != nil && *row.PolicyKeepLatest {
								kc.keep++
							}
						}
					}

					// Migrate PolicyKeepLatest to Stream.AutoExpirePrevious. In the
					// old schema keep-latest expired the stream's previous groups as
					// soon as a new group arrived, while the newest kept its own
					// expiry — that is AutoExpirePrevious, not RetainLatest (which
					// would make the newest group never expire).
					//
					// Old rotation only expired files that were themselves flagged,
					// while AutoExpirePrevious expires every older group in the
					// stream. A stream mixing flagged and unflagged files would lose
					// the unflagged ones on its next upload, so only fully flagged
					// streams are converted; mixed ones are logged for review.
					var autoExpireStreams int
					for streamID, kc := range keepLatest {
						if kc.keep == 0 {
							continue
						}
						if kc.keep < kc.total {
							var st Stream
							tx.First(&st, streamID)
							logrus.WithFields(logrus.Fields{
								"stream":      st.Name,
								"keep_latest": kc.keep,
								"resources":   kc.total,
							}).Warn("migration 202605311000_extract_stream_and_group: stream mixes keep-latest and plain uploads, auto_expire_previous NOT set")
							continue
						}
						if err := tx.Model(&Stream{}).Where("id = ?", streamID).Update("auto_expire_previous", true).Error; err != nil {
							return err
						}
						autoExpireStreams++
					}
					if len(oldData) > 0 {
						suspendReasons = append(suspendReasons, "migrated legacy streams and groups")
					}

					for groupID, createdAt := range groupCreated {
						if err := tx.Model(&Group{}).Where("id = ?", groupID).UpdateColumn("created_at", createdAt).Error; err != nil {
							return err
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
						"auto_expire_streams": autoExpireStreams,
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

				if len(rows) > 0 {
					suspendReasons = append(suspendReasons, "recovered legacy expiry dates")
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
		{
			ID: "202611111200_backfill_group_deadlines",
			Migrate: func(tx *gorm.DB) error {
				set, err := backfillGroupDeadlines(tx)
				if set > 0 {
					suspendReasons = append(suspendReasons, "backfilled group deletion deadlines")
				}
				return err
			},
			Rollback: func(tx *gorm.DB) error {
				return nil
			},
		},
		{
			ID: "202611161200_drop_download_mode",
			Migrate: func(tx *gorm.DB) error {
				var err error
				mergeDirs, err = dropDownloadMode(tx)
				return err
			},
			Rollback: func(tx *gorm.DB) error {
				return nil
			},
		},
	})

	if err := m.Migrate(); err != nil {
		return err
	}

	if auditor != nil {
		for _, p := range mergeDirs {
			auditor.Success("SYSTEM_MAINTENANCE", p,
				"operation", "migration_download_mode_dropped",
				"download_mode", "merge",
				"replacement", "batch download with ?mode=merge (\"Download merged\" in the UI)")
		}
	}

	if err := db.AutoMigrate(
		&Stream{},
		&Group{},
		&MetaResource{},
		&MetaTag{},
		&models.User{},
		&models.Token{},
		&models.SystemState{},
	); err != nil {
		return err
	}

	if len(suspendReasons) > 0 {
		reason := "migration: " + strings.Join(suspendReasons, ", ")
		logrus.WithField("reason", reason).Warn("migration changed retention data: janitor suspended until an admin reviews pending deletions and resumes it")
		return SuspendJanitor(db, reason)
	}
	return nil
}

// backfillGroupDeadlines fills in Group.EffectiveDeleteAfter for groups that
// never got one. The janitor deletes grouped files only via the group deadline,
// and earlier migrations created groups without computing it, so migrated
// groups that never received another upload were kept forever regardless of
// their files' expiry.
//
// Applies the same rule handleStreamRetention uses for groups other than the
// one being saved: the latest group of a RetainLatest stream keeps no deadline
// (or its RetainLatestMaxExpiry cap); every other group gets the latest of its
// members' deadlines, or none if any member is immutable or has no expiry.
// Groups whose deadline has already passed are deleted by the next janitor run.
func backfillGroupDeadlines(tx *gorm.DB) (int, error) {
	logrus.Info("migration 202611111200_backfill_group_deadlines: starting")

	// On a fresh install meta_resources only has the columns earlier
	// migrations touched; computeGroupDeadline needs the full model.
	if err := tx.AutoMigrate(&Stream{}, &Group{}, &MetaResource{}); err != nil {
		return 0, err
	}

	var groups []Group
	if err := tx.Preload("Stream").Where("effective_delete_after IS NULL").Find(&groups).Error; err != nil {
		return 0, fmt.Errorf("load groups: %w", err)
	}

	latestByStream := make(map[uint]uint)
	latestGroup := func(streamID uint) (uint, error) {
		if id, ok := latestByStream[streamID]; ok {
			return id, nil
		}
		var g Group
		if err := tx.Where("stream_id = ?", streamID).Order("created_at DESC, id DESC").Limit(1).Find(&g).Error; err != nil {
			return 0, fmt.Errorf("load latest group: %w", err)
		}
		latestByStream[streamID] = g.ID
		return g.ID, nil
	}

	ts := now()
	var set, kept, past int
	for _, g := range groups {
		var deadline *time.Time

		latestID, err := latestGroup(g.StreamID)
		if err != nil {
			return set, err
		}
		if g.ID == latestID && ptr.Val(g.Stream.RetainLatest) {
			if g.Stream.RetainLatestMaxExpiry != nil {
				if d, err := parseDuration(*g.Stream.RetainLatestMaxExpiry); err == nil {
					deadline = ptr.Of(g.CreatedAt.Add(d))
				}
			}
		} else {
			deadline, err = computeGroupDeadline(tx, g.ID, &ts)
			if err != nil {
				return set, err
			}
		}

		if deadline == nil {
			kept++
			continue
		}
		if err := tx.Model(&Group{}).Where("id = ?", g.ID).Update("effective_delete_after", deadline).Error; err != nil {
			return set, fmt.Errorf("update group %d: %w", g.ID, err)
		}
		set++
		if !deadline.After(ts) {
			past++
		}
	}

	logrus.WithFields(logrus.Fields{
		"groups_without_deadline": len(groups),
		"deadlines_set":           set,
		"already_past":            past,
		"still_no_deadline":       kept,
	}).Info("migration 202611111200_backfill_group_deadlines: backfilled group deadlines")

	return set, nil
}

// dropDownloadMode removes the legacy per-directory batch download mode.
//
// The old version stored download_mode on each resource and used the common
// parent's value when a batch download did not say which mode it wanted. The
// current version has no per-directory setting: the caller chooses with
// ?mode=merge (the UI's "Download merged"), and the default is literal. Only
// "merge" ever changed behaviour, so those directories are returned (and
// logged) for the audit trail before the column is dropped.
func dropDownloadMode(tx *gorm.DB) ([]string, error) {
	const id = "migration 202611161200_drop_download_mode"
	if !tx.Migrator().HasColumn(&MetaResource{}, "download_mode") {
		logrus.Info(id + ": no legacy download_mode column, nothing to do")
		return nil, nil
	}

	var paths []string
	if err := tx.Table("meta_resources").
		Where("LOWER(TRIM(download_mode)) = ?", "merge").
		Order("path").
		Pluck("path", &paths).Error; err != nil {
		return nil, fmt.Errorf("load merge directories: %w", err)
	}
	for _, p := range paths {
		logrus.WithField("path", p).Warn(id + ": per-directory download mode \"merge\" dropped; batch downloads now default to literal, use ?mode=merge")
	}

	type OldDownloadMode struct {
		DownloadMode string `gorm:"column:download_mode"`
	}
	// Best effort, like the other legacy column drops: a column left behind is
	// unused and harmless.
	if err := tx.Table("meta_resources").Migrator().DropColumn(&OldDownloadMode{}, "download_mode"); err != nil {
		logrus.WithError(err).Warn(id + ": could not drop download_mode column")
	}

	logrus.WithField("merge_directories", len(paths)).Info(id + ": dropped per-directory download mode")
	return paths, nil
}
