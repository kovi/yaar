package api

import (
	"fmt"
	"time"

	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/kovi/yaar/internal/utils"
	"gorm.io/gorm"
)

// Save persists the resource and recomputes its group's deadline and the
// stream retention. cfg supplies the protected paths (nil: none), which, like
// immutable files, the janitor never deletes.
func (r *MetaResource) Save(tx *gorm.DB, cfg *config.Config) error {
	r.Compute()

	if err := tx.Save(r).Error; err != nil {
		return err
	}

	if r.GroupID != nil {
		if err := r.syncGroupDeadline(tx); err != nil {
			return err
		}
		if err := r.handleStreamRetention(tx, cfg); err != nil {
			return err
		}
	}

	return nil
}

func parseNullableTime(s *string) *time.Time {
	if s == nil {
		return nil
	}
	if t, err := parseTimeString(*s); err == nil {
		return &t
	}
	return nil
}

// computeGroupDeadline returns the deadline a group's members give it: none
// if any member is immutable or has no deadline of its own, otherwise the
// latest member deadline. currentResourceDeadline is the deadline of the
// member being saved (nil means it is kept forever, so the group is too) or,
// for a group with no member being saved, a lower bound such as "now".
func computeGroupDeadline(tx *gorm.DB, groupID uint, currentResourceDeadline *time.Time) (*time.Time, error) {
	var immutableCount int64
	if err := tx.Model(&MetaResource{}).
		Where("group_id = ? AND immutable = true", groupID).
		Count(&immutableCount).Error; err != nil {
		return nil, fmt.Errorf("count immutable members: %w", err)
	}

	var nullCount int64
	if err := tx.Model(&MetaResource{}).
		Where("group_id = ? AND delete_after IS NULL", groupID).
		Count(&nullCount).Error; err != nil {
		return nil, fmt.Errorf("count null deadlines: %w", err)
	}

	if immutableCount > 0 || nullCount > 0 || currentResourceDeadline == nil {
		return nil, nil
	}

	var result struct{ Max *string }
	if err := tx.Model(&MetaResource{}).
		Where("group_id = ?", groupID).
		Select("MAX(delete_after) as max").
		Scan(&result).Error; err != nil {
		return nil, fmt.Errorf("compute group deadline: %w", err)
	}

	maxTime := parseNullableTime(result.Max)
	if maxTime == nil || currentResourceDeadline.After(*maxTime) {
		maxTime = currentResourceDeadline
	}

	return maxTime, nil
}

func (r *MetaResource) syncGroupDeadline(tx *gorm.DB) error {
	maxTime, err := computeGroupDeadline(tx, *r.GroupID, r.DeleteAfter)
	if err != nil {
		return err
	}

	return tx.Model(&Group{}).
		Where("id = ?", *r.GroupID).
		Update("effective_delete_after", maxTime).Error
}

// latestGroupID returns the stream's latest group. id breaks created_at ties,
// matching pendingDeletions and the group-deadline backfill, so the review
// page predicts the same group.
func latestGroupID(tx *gorm.DB, streamID uint) (uint, error) {
	var latest Group
	if err := tx.Where("stream_id = ?", streamID).
		Order("created_at DESC, id DESC").
		Limit(1).
		Find(&latest).Error; err != nil {
		return 0, fmt.Errorf("load latest group: %w", err)
	}
	return latest.ID, nil
}

// streamGroupDeadline applies the stream retention policy to group g:
//   - the latest group of a RetainLatest stream has no deadline, or its
//     RetainLatestMaxExpiry cap;
//   - every older group of an AutoExpirePrevious stream expires now, unless
//     the janitor could not delete any of its members anyway;
//   - any other group gets its members' deadline (computeGroupDeadline, with
//     memberDeadline as the deadline of the member being saved or a floor).
func streamGroupDeadline(tx *gorm.DB, cfg *config.Config, g *Group, stream *Stream, latestID uint, memberDeadline *time.Time, now time.Time) (*time.Time, error) {
	switch {
	case g.ID == latestID && ptr.Val(stream.RetainLatest):
		if stream.RetainLatestMaxExpiry != nil {
			if d, err := utils.ParseDuration(*stream.RetainLatestMaxExpiry); err == nil {
				return ptr.Of(g.CreatedAt.Add(d)), nil
			}
		}
		return nil, nil
	case g.ID != latestID && ptr.Val(stream.AutoExpirePrevious):
		// A group whose members are all locked (e.g. kept in the janitor
		// review) or protected has nothing to expire. Re-arming it on every
		// save would only make the janitor skip it again and log a failure per
		// file.
		retained, err := allMembersRetained(tx, cfg, g.ID)
		if err != nil || retained {
			return nil, err
		}
		return &now, nil
	default:
		return computeGroupDeadline(tx, g.ID, memberDeadline)
	}
}

func (r *MetaResource) handleStreamRetention(tx *gorm.DB, cfg *config.Config) error {
	if r.Group == nil || r.Group.Stream.ID == 0 {
		r.Group = &Group{}
		if err := tx.Preload("Stream").First(r.Group, r.GroupID).Error; err != nil {
			return fmt.Errorf("load group: %w", err)
		}
	}

	group := *r.Group
	stream := r.Group.Stream
	if !ptr.Val(stream.AutoExpirePrevious) && !ptr.Val(stream.RetainLatest) {
		return nil
	}

	now := now()

	latestID, err := latestGroupID(tx, stream.ID)
	if err != nil {
		return err
	}

	var groups []Group
	if err := tx.Where("stream_id = ?", stream.ID).Find(&groups).Error; err != nil {
		return fmt.Errorf("load stream groups: %w", err)
	}

	for _, g := range groups {
		memberDeadline := &now
		if g.ID == group.ID {
			memberDeadline = r.DeleteAfter
		}
		effectiveDeadline, err := streamGroupDeadline(tx, cfg, &g, &stream, latestID, memberDeadline, now)
		if err != nil {
			return err
		}

		if err := tx.Model(&g).Update("effective_delete_after", effectiveDeadline).Error; err != nil {
			return fmt.Errorf("update group %d: %w", g.ID, err)
		}
	}

	return nil
}

// refreshGroupDeadline recomputes a group's deadline after a member was
// removed from it (deleted by the janitor or the sync worker), from the
// members that remain and the stream policy. syncGroupDeadline does not fit:
// it treats the departed member's missing deadline as "keep forever" and so
// cleared the deadline of a group whose remaining members still expire.
func refreshGroupDeadline(tx *gorm.DB, cfg *config.Config, groupID uint) error {
	var groups []Group
	if err := tx.Preload("Stream").Where("id = ?", groupID).Limit(1).Find(&groups).Error; err != nil {
		return fmt.Errorf("load group: %w", err)
	}
	if len(groups) == 0 {
		return nil
	}
	g := groups[0]

	now := now()
	latestID := g.ID
	if ptr.Val(g.Stream.AutoExpirePrevious) || ptr.Val(g.Stream.RetainLatest) {
		var err error
		if latestID, err = latestGroupID(tx, g.StreamID); err != nil {
			return err
		}
	}
	deadline, err := streamGroupDeadline(tx, cfg, &g, &g.Stream, latestID, &now, now)
	if err != nil {
		return err
	}
	return tx.Model(&Group{}).Where("id = ?", g.ID).Update("effective_delete_after", deadline).Error
}

// isRetained reports whether the janitor must never delete r: it is immutable
// or on a protected path.
func isRetained(cfg *config.Config, r *MetaResource) bool {
	return ptr.Val(r.Immutable) || (cfg != nil && cfg.IsProtected(r.Path))
}

// allMembersRetained reports whether the group has members and the janitor
// could delete none of them (see isRetained). An empty group is not
// "retained": the janitor removes it.
func allMembersRetained(tx *gorm.DB, cfg *config.Config, groupID uint) (bool, error) {
	var total int64
	if err := tx.Model(&MetaResource{}).Where("group_id = ?", groupID).Count(&total).Error; err != nil {
		return false, fmt.Errorf("count members: %w", err)
	}
	if total == 0 {
		return false, nil
	}
	var unlocked []MetaResource
	q := tx.Model(&MetaResource{}).Select("id", "path", "immutable").
		Where("group_id = ? AND (immutable IS NULL OR immutable = false)", groupID)
	if cfg == nil || len(cfg.Storage.ProtectedPaths) == 0 {
		// Without protected paths only immutability counts; one unlocked
		// member is enough to answer.
		q = q.Limit(1)
	}
	if err := q.Find(&unlocked).Error; err != nil {
		return false, fmt.Errorf("load unlocked members: %w", err)
	}
	for i := range unlocked {
		if !isRetained(cfg, &unlocked[i]) {
			return false, nil
		}
	}
	return true, nil
}

func (r *MetaResource) computeOwnDeadline() *time.Time {
	var latest *time.Time

	consider := func(t time.Time) {
		if latest == nil || t.After(*latest) {
			latest = &t
		}
	}

	if r.ExpiryAt != nil {
		consider(*r.ExpiryAt)
	}
	if r.ExpiryAfterUpload != nil {
		if d, err := utils.ParseDuration(*r.ExpiryAfterUpload); err == nil {
			consider(r.CreatedAt.Add(d))
		}
	}
	if r.ExpiryAfterDownload != nil {
		// Use last download as baseline; fall back to updated_at (≈ when policy was set)
		// so files that are never downloaded still have a finite deadline.
		baseline := r.LastDownloadedAt
		if baseline == nil {
			baseline = &r.UpdatedAt
		}
		if d, err := utils.ParseDuration(*r.ExpiryAfterDownload); err == nil {
			consider(baseline.Add(d))
		}
	}

	return latest
}

// Compute sets DeleteAfter on the struct from its own policies only.
// Does not touch the DB. Call this after any policy field changes.
func (r *MetaResource) Compute() {
	r.DeleteAfter = r.computeOwnDeadline()
}
