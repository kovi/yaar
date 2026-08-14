package api

import (
	"fmt"
	"time"

	"github.com/kovi/yaar/internal/ptr"
	"gorm.io/gorm"
)

func (r *MetaResource) Save(tx *gorm.DB) error {
	r.Compute()

	if err := tx.Save(r).Error; err != nil {
		return err
	}

	if r.GroupID != nil {
		if err := r.syncGroupDeadline(tx); err != nil {
			return err
		}
		if err := r.handleStreamRetention(tx); err != nil {
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

func (r *MetaResource) handleStreamRetention(tx *gorm.DB) error {
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

	var latestGroup Group
	if err := tx.Where("stream_id = ?", stream.ID).
		Order("created_at DESC").
		Limit(1).
		Find(&latestGroup).Error; err != nil {
		return fmt.Errorf("load latest group: %w", err)
	}

	var groups []Group
	if err := tx.Where("stream_id = ?", stream.ID).Find(&groups).Error; err != nil {
		return fmt.Errorf("load stream groups: %w", err)
	}

	for _, g := range groups {
		var effectiveDeadline *time.Time

		if g.ID == latestGroup.ID && ptr.Val(stream.RetainLatest) {
			effectiveDeadline = nil
			if stream.RetainLatestMaxExpiry != nil {
				if d, err := parseDuration(*stream.RetainLatestMaxExpiry); err == nil {
					cap := g.CreatedAt.Add(d)
					effectiveDeadline = &cap
				}
			}
		} else if ptr.Val(stream.AutoExpirePrevious) && g.ID != latestGroup.ID {
			effectiveDeadline = &now
		} else {
			var currentResourceDeadline *time.Time
			if g.ID == group.ID {
				currentResourceDeadline = r.DeleteAfter
			} else {
				currentResourceDeadline = &now
			}
			dl, err := computeGroupDeadline(tx, g.ID, currentResourceDeadline)
			if err != nil {
				return err
			}
			effectiveDeadline = dl
		}

		if err := tx.Model(&g).Update("effective_delete_after", effectiveDeadline).Error; err != nil {
			return fmt.Errorf("update group %d: %w", g.ID, err)
		}
	}

	return nil
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
		if d, err := parseDuration(*r.ExpiryAfterUpload); err == nil {
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
		if d, err := parseDuration(*r.ExpiryAfterDownload); err == nil {
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
