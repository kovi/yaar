package api

import (
	"fmt"
	"math"
	"slices"
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

	// Migrations that change what the janitor will delete suspend it until an
	// admin has reviewed the pending deletions and resumed it (see
	// janitor_review.go). The suspension is written by the migration itself,
	// before it changes any data: gormigrate records each migration as soon as
	// it finishes (no surrounding transaction), so a suspension deferred to the
	// end would be lost if a later step failed and the restart skipped the
	// already-applied migrations.
	if err := db.AutoMigrate(&models.SystemState{}); err != nil {
		return err
	}

	// Directories whose per-directory batch download mode was "merge"; recorded
	// in the audit log once the migrations have committed.
	var mergeDirs []string

	// Decided before any migration runs: a revision marker written later in
	// this run must not hide an extraction an earlier version did.
	unrevisedExtraction, err := extractionRanUnrevised(db)
	if err != nil {
		return err
	}

	m := gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{
		{
			ID: "202605311000_extract_stream_and_group",
			Migrate: func(tx *gorm.DB) error {
				logrus.Info("migration 202605311000_extract_stream_and_group: starting")
				// This migration was corrected after it shipped (keep-latest
				// conversion, group created_at). The marker tells
				// 202611191200_review_legacy_keep_latest which version ran.
				if err := tx.Where("key = ?", extractionRevisionKey).
					Assign(models.SystemState{Value: extractionRevision}).
					FirstOrCreate(&models.SystemState{}, models.SystemState{Key: extractionRevisionKey}).Error; err != nil {
					return fmt.Errorf("record extraction revision: %w", err)
				}
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
					if len(oldData) > 0 {
						if err := suspendJanitorForMigration(tx, auditor, "migrated legacy streams and groups"); err != nil {
							return err
						}
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
					if err := suspendJanitorForMigration(tx, auditor, "recovered legacy expiry dates"); err != nil {
						return err
					}
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
				_, err := backfillGroupDeadlines(tx, auditor)
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
		{
			ID: "202611191200_review_legacy_keep_latest",
			Migrate: func(tx *gorm.DB) error {
				return reviewLegacyKeepLatest(tx, auditor, unrevisedExtraction)
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

// The stream/group extraction writes extractionRevisionKey since it was
// corrected; an install whose extraction is recorded without it ran the
// original version.
const (
	extractionRevisionKey = "migration_202605311000_revision"
	extractionRevision    = "2"
)

// extractionRanUnrevised reports whether 202605311000_extract_stream_and_group
// already ran in the version that first shipped, before it was corrected.
func extractionRanUnrevised(db *gorm.DB) (bool, error) {
	if !db.Migrator().HasTable("migrations") {
		return false, nil
	}
	var applied int64
	if err := db.Table("migrations").Where("id = ?", "202605311000_extract_stream_and_group").Count(&applied).Error; err != nil {
		return false, fmt.Errorf("check applied migrations: %w", err)
	}
	if applied == 0 {
		return false, nil
	}
	var marked int64
	if err := db.Model(&models.SystemState{}).Where("key = ?", extractionRevisionKey).Count(&marked).Error; err != nil {
		return false, fmt.Errorf("check extraction revision: %w", err)
	}
	return marked == 0, nil
}

// reviewLegacyKeepLatest flags streams that the original stream/group
// extraction may have converted wrongly, on installs where that version ran.
//
// It turned the legacy per-file keep-latest flag into RetainLatest, which
// keeps the newest group forever and stops expiring the older ones on a new
// upload. Legacy keep-latest meant AutoExpirePrevious: older groups expire
// when a new one arrives and the newest keeps its own expiry. The legacy
// column is gone, and RetainLatest may since have been set on purpose, so the
// streams cannot be corrected automatically: each RetainLatest stream is
// logged and audited for an admin to check. Nothing is deleted either way
// (RetainLatest only keeps more), so the janitor is not suspended.
//
// The original version also created groups with the migration time as
// created_at, but in legacy row (upload) order, so "latest group in stream"
// already resolves to the same group the corrected version picks; that part
// needs no repair.
func reviewLegacyKeepLatest(tx *gorm.DB, auditor *audit.Auditor, unrevised bool) error {
	const id = "migration 202611191200_review_legacy_keep_latest"
	if !unrevised {
		logrus.Info(id + ": stream/group extraction ran in its corrected version, nothing to review")
		return nil
	}
	var streams []Stream
	if err := tx.Where("retain_latest = ?", true).Order("name").Find(&streams).Error; err != nil {
		return fmt.Errorf("load retain_latest streams: %w", err)
	}
	for _, st := range streams {
		logrus.WithField("stream", st.Name).Warn(id + ": stream has retain_latest, possibly converted from legacy keep-latest; " +
			"if it should expire previous groups on upload instead, set auto_expire_previous and clear retain_latest")
		if auditor != nil {
			auditor.Success("SYSTEM_MAINTENANCE", st.Name,
				"operation", "migration_review_retain_latest",
				"stream", st.Name,
				"retain_latest", true,
				"auto_expire_previous", ptr.Val(st.AutoExpirePrevious),
				"note", "the original stream/group migration mapped legacy keep-latest to retain_latest; legacy behaviour was auto_expire_previous")
		}
	}
	logrus.WithField("streams", len(streams)).Info(id + ": flagged retain_latest streams for review")
	return nil
}

// Separates the migration reasons from an admin's own suspension reason, given
// before or after the migration suspended the janitor.
const adminReasonSep = "; admin reason: "

// migrationSuspendPrefix starts every suspension a migration wrote; the UI keys
// the review flow off it.
const migrationSuspendPrefix = "migration: "

// migrationSuspendReason adds reason to the stored suspension. The result
// always starts with migrationSuspendPrefix;
// reasons already present are not repeated (a failed migration re-runs), and
// an earlier admin reason is kept rather than overwritten.
func migrationSuspendReason(existing *string, reason string) string {
	const prefix = migrationSuspendPrefix
	if existing == nil {
		return prefix + reason
	}
	prev := *existing
	if !strings.HasPrefix(prev, prefix) {
		return prefix + reason + adminReasonSep + prev
	}
	head, admin, hasAdmin := strings.Cut(prev, adminReasonSep)
	reasons := strings.Split(strings.TrimPrefix(head, prefix), ", ")
	if !slices.Contains(reasons, reason) {
		reasons = append(reasons, reason)
	}
	out := prefix + strings.Join(reasons, ", ")
	if hasAdmin {
		out += adminReasonSep + admin
	}
	return out
}

// adminSuspendReason is the stored suspension after an admin suspends with
// reason. A pending migration review is kept, with reason replacing any
// earlier admin reason: losing the "migration: " prefix would drop the
// review warning in the UI, and a later resume would delete what the
// migration changed without anyone having seen that it was a migration.
func adminSuspendReason(existing *string, reason string) string {
	if existing == nil || !strings.HasPrefix(*existing, migrationSuspendPrefix) {
		return reason
	}
	head, _, _ := strings.Cut(*existing, adminReasonSep)
	return head + adminReasonSep + reason
}

// suspendJanitorForMigration suspends the janitor on behalf of a migration
// that is about to change retention data. Called before the change, so a
// migration that fails part-way still leaves the janitor suspended.
//
// The suspension is also audited (when auditor is non-nil) as a
// janitor_suspend by "system", so the janitor log shows when and why it
// stopped, not only the later resume.
func suspendJanitorForMigration(tx *gorm.DB, auditor *audit.Auditor, reason string) error {
	st, err := janitorSuspension(tx)
	if err != nil {
		return fmt.Errorf("check janitor suspension: %w", err)
	}
	var existing *string
	if st != nil {
		existing = &st.Value
	}
	value := migrationSuspendReason(existing, reason)
	if existing != nil && *existing == value {
		return nil
	}
	logrus.WithField("reason", value).Warn("migration changes retention data: janitor suspended until an admin reviews pending deletions and resumes it")
	if err := SuspendJanitor(tx, value); err != nil {
		return err
	}
	if auditor != nil {
		auditor.Success("SYSTEM_MAINTENANCE", "janitor",
			"operation", "janitor_suspend",
			"reason", value,
			"migration_reason", reason)
	}
	return nil
}

// backfillGroupDeadlines fills in Group.EffectiveDeleteAfter for groups that
// never got one. The janitor deletes grouped files only via the group deadline,
// and earlier migrations created groups without computing it, so migrated
// groups that never received another upload were kept forever regardless of
// their files' expiry.
//
// Applies the rule handleStreamRetention uses for groups other than the one
// being saved (streamGroupDeadline): the latest group of a RetainLatest stream
// keeps no deadline (or its RetainLatestMaxExpiry cap); older groups of an
// AutoExpirePrevious stream expire now unless every member is locked; every
// other group gets the latest of its members' deadlines, or none if any member
// is immutable or has no expiry. Groups whose deadline has already passed are
// deleted by the next janitor run, which is why the janitor is suspended for
// review first.
//
// Protected paths are not known here (the config is not passed to
// migrations), so an older group made up only of protected files still gets a
// deadline; the janitor skips its members once and clears it.
func backfillGroupDeadlines(tx *gorm.DB, auditor *audit.Auditor) (int, error) {
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
		id, err := latestGroupID(tx, streamID)
		if err != nil {
			return 0, err
		}
		latestByStream[streamID] = id
		return id, nil
	}

	ts := now()
	var set, kept, past int
	suspended := false
	for _, g := range groups {
		latestID, err := latestGroup(g.StreamID)
		if err != nil {
			return set, err
		}
		deadline, err := streamGroupDeadline(tx, nil, &g, &g.Stream, latestID, &ts, ts)
		if err != nil {
			return set, err
		}

		if deadline == nil {
			kept++
			continue
		}
		if !suspended {
			if err := suspendJanitorForMigration(tx, auditor, "backfilled group deletion deadlines"); err != nil {
				return set, err
			}
			suspended = true
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
