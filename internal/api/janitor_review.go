package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/models"
	"gorm.io/gorm"
)

// Janitor suspension.
//
// A migration that changes retention data (stream policies, expiry dates,
// group deadlines) can make the janitor delete a large amount of data on its
// first run. Such migrations suspend the janitor instead; an admin reviews the
// pending deletions, keeps (locks) what must survive, and then resumes it.
// Admins can also suspend it themselves at any time (e.g. during a release
// freeze). Either way it stays suspended until an admin resumes it.
// The flag lives in system_states so it survives restarts.

const janitorSuspendedKey = "janitor_suspended"

// SuspendJanitor stops the janitor from deleting anything until ResumeJanitor
// is called. reason is shown to the admin; suspending again replaces it.
func SuspendJanitor(db *gorm.DB, reason string) error {
	var st models.SystemState
	if err := db.Where("key = ?", janitorSuspendedKey).
		Assign(models.SystemState{Value: reason}).
		FirstOrCreate(&st, models.SystemState{Key: janitorSuspendedKey}).Error; err != nil {
		return fmt.Errorf("suspend janitor: %w", err)
	}
	return nil
}

// ResumeJanitor clears the suspension.
func ResumeJanitor(db *gorm.DB) error {
	return db.Where("key = ?", janitorSuspendedKey).Delete(&models.SystemState{}).Error
}

// janitorSuspension returns the active suspension, or nil if the janitor runs.
func janitorSuspension(db *gorm.DB) (*models.SystemState, error) {
	var states []models.SystemState
	if err := db.Where("key = ?", janitorSuspendedKey).Limit(1).Find(&states).Error; err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, nil
	}
	return &states[0], nil
}

type JanitorStatus struct {
	Suspended bool       `json:"suspended"`
	Reason    string     `json:"reason,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
}

func (h *Handler) janitorStatus() (JanitorStatus, error) {
	st, err := janitorSuspension(h.DB)
	if err != nil || st == nil {
		return JanitorStatus{}, err
	}
	since := st.UpdatedAt
	return JanitorStatus{Suspended: true, Reason: st.Value, Since: &since}, nil
}

// Pending deletions.

const (
	// PendingDue: the deadline has passed; the next janitor run deletes it.
	PendingDue = "due"
	// PendingNextUpload: an older group of an auto_expire_previous stream. It
	// is expired the next time any file in the stream is saved: an upload, a
	// metadata change, or a download of a file with expire-after-download
	// (handleStreamRetention runs on every MetaResource.Save). The value keeps
	// its original name for API compatibility.
	PendingNextUpload = "next_upload"
)

type PendingFile struct {
	ID       uint       `json:"id"`
	Path     string     `json:"path"`
	Size     int64      `json:"size"`
	Deadline *time.Time `json:"deadline,omitempty"`
}

type PendingGroup struct {
	ID       uint          `json:"id"`
	Stream   string        `json:"stream"`
	Group    string        `json:"group"`
	Reason   string        `json:"reason"`
	Deadline *time.Time    `json:"deadline,omitempty"`
	Size     int64         `json:"size"`
	Files    []PendingFile `json:"files"`
}

type PendingDeletions struct {
	JanitorStatus
	Groups []PendingGroup `json:"groups"`
	// Files without a group whose own deadline has passed.
	Files []PendingFile `json:"files"`
	Total struct {
		Files int   `json:"files"`
		Size  int64 `json:"size"`
	} `json:"total"`
}

// expirableMembers loads the members the janitor would actually delete:
// immutable and protected files are skipped by cleanupGroup.
func (h *Handler) expirableMembers(groupIDs []uint) (map[uint][]PendingFile, error) {
	out := make(map[uint][]PendingFile, len(groupIDs))
	if len(groupIDs) == 0 {
		return out, nil
	}
	var members []MetaResource
	if err := h.DB.Where("group_id IN ? AND (immutable IS NULL OR immutable = false)", groupIDs).
		Order("path").Find(&members).Error; err != nil {
		return nil, fmt.Errorf("load members: %w", err)
	}
	for _, m := range members {
		if h.Config.IsProtected(m.Path) {
			continue
		}
		out[*m.GroupID] = append(out[*m.GroupID], PendingFile{ID: m.ID, Path: m.Path, Size: m.Size, Deadline: m.DeleteAfter})
	}
	return out, nil
}

func (h *Handler) pendingDeletions(at time.Time) (*PendingDeletions, error) {
	res := &PendingDeletions{Groups: []PendingGroup{}, Files: []PendingFile{}}

	// 1. Groups past their deadline (what cleanupExpiredGroups selects).
	var due []Group
	if err := h.DB.Preload("Stream").Where("effective_delete_after < ?", at).Find(&due).Error; err != nil {
		return nil, fmt.Errorf("query expired groups: %w", err)
	}
	candidates := make([]PendingGroup, 0, len(due))
	seen := make(map[uint]bool, len(due))
	for _, g := range due {
		seen[g.ID] = true
		candidates = append(candidates, PendingGroup{ID: g.ID, Stream: g.Stream.Name, Group: g.Name, Reason: PendingDue, Deadline: g.EffectiveDeleteAfter})
	}

	// 2. Older groups of auto_expire_previous streams: handleStreamRetention
	// sets their deadline to "now" on the next save of any file in the stream.
	var streams []Stream
	if err := h.DB.Where("auto_expire_previous = ?", true).Find(&streams).Error; err != nil {
		return nil, fmt.Errorf("query streams: %w", err)
	}
	for _, s := range streams {
		var groups []Group
		if err := h.DB.Where("stream_id = ?", s.ID).Order("created_at DESC, id DESC").Find(&groups).Error; err != nil {
			return nil, fmt.Errorf("load stream groups: %w", err)
		}
		for i, g := range groups {
			if i == 0 || seen[g.ID] {
				continue // the latest group survives; due groups are listed already
			}
			seen[g.ID] = true
			candidates = append(candidates, PendingGroup{ID: g.ID, Stream: s.Name, Group: g.Name, Reason: PendingNextUpload, Deadline: g.EffectiveDeleteAfter})
		}
	}

	ids := make([]uint, len(candidates))
	for i, g := range candidates {
		ids[i] = g.ID
	}
	members, err := h.expirableMembers(ids)
	if err != nil {
		return nil, err
	}
	for _, g := range candidates {
		g.Files = members[g.ID]
		if len(g.Files) == 0 {
			continue // nothing the janitor would delete
		}
		for _, f := range g.Files {
			g.Size += f.Size
		}
		res.Groups = append(res.Groups, g)
		res.Total.Files += len(g.Files)
		res.Total.Size += g.Size
	}
	sort.SliceStable(res.Groups, func(i, j int) bool {
		a, b := res.Groups[i], res.Groups[j]
		if a.Reason != b.Reason {
			return a.Reason == PendingDue
		}
		if a.Stream != b.Stream {
			return a.Stream < b.Stream
		}
		return a.Group < b.Group
	})

	// 3. Ungrouped files past their own deadline (cleanupUngroupedResources).
	var files []MetaResource
	if err := h.DB.Where("group_id IS NULL AND delete_after < ? AND (immutable IS NULL OR immutable = false)", at).
		Order("path").Find(&files).Error; err != nil {
		return nil, fmt.Errorf("query ungrouped: %w", err)
	}
	for _, f := range files {
		if h.Config.IsProtected(f.Path) {
			continue
		}
		res.Files = append(res.Files, PendingFile{ID: f.ID, Path: f.Path, Size: f.Size, Deadline: f.DeleteAfter})
		res.Total.Files++
		res.Total.Size += f.Size
	}

	return res, nil
}

// Handlers (admin only, see routes.go).

func (h *Handler) GetJanitorStatus(c *gin.Context) {
	st, err := h.janitorStatus()
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

func (h *Handler) GetPendingDeletions(c *gin.Context) {
	p, err := h.pendingDeletions(now())
	if err != nil {
		respondErr(c, err)
		return
	}
	st, err := h.janitorStatus()
	if err != nil {
		respondErr(c, err)
		return
	}
	p.JanitorStatus = st
	c.JSON(http.StatusOK, p)
}

// KeepPending revokes pending deletions by locking the files (immutable), the
// one protection every janitor path and stream policy honours. It is
// reversible: unlock the file in the browser and its expiry applies again.
func (h *Handler) KeepPending(c *gin.Context) {
	var req struct {
		GroupIDs    []uint `json:"group_ids"`
		ResourceIDs []uint `json:"resource_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErr(c, ErrBadRequest, err)
		return
	}
	if len(req.GroupIDs) == 0 && len(req.ResourceIDs) == 0 {
		respondErr(c, &AppError{ErrBadRequest, "group_ids or resource_ids required", nil})
		return
	}

	var locked []MetaResource
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		q := tx.Where("immutable IS NULL OR immutable = false")
		switch {
		case len(req.GroupIDs) > 0 && len(req.ResourceIDs) > 0:
			q = q.Where(tx.Where("group_id IN ?", req.GroupIDs).Or("id IN ?", req.ResourceIDs))
		case len(req.GroupIDs) > 0:
			q = q.Where("group_id IN ?", req.GroupIDs)
		default:
			q = q.Where("id IN ?", req.ResourceIDs)
		}
		if err := q.Find(&locked).Error; err != nil {
			return err
		}
		if len(locked) == 0 {
			return nil
		}
		ids := make([]uint, len(locked))
		for i, r := range locked {
			ids[i] = r.ID
		}
		if err := tx.Model(&MetaResource{}).Where("id IN ?", ids).Update("immutable", true).Error; err != nil {
			return err
		}
		// A fully kept group no longer has anything to delete: drop its
		// deadline so the janitor doesn't select it every run. Partially kept
		// groups keep theirs; the janitor deletes the rest.
		if len(req.GroupIDs) > 0 {
			if err := tx.Model(&Group{}).Where("id IN ?", req.GroupIDs).
				Update("effective_delete_after", nil).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		respondErr(c, err)
		return
	}

	for _, r := range locked {
		h.Audit.WithContext(c).Success("SYSTEM_MAINTENANCE", r.Path, "operation", "janitor_review_keep", "immutable", true)
	}
	h.log(c).WithField("files", len(locked)).Info("janitor review: kept files by locking them")
	c.JSON(http.StatusOK, gin.H{"locked": len(locked)})
}

func (h *Handler) ResumeJanitorHandler(c *gin.Context) {
	st, err := h.janitorStatus()
	if err != nil {
		respondErr(c, err)
		return
	}
	if !st.Suspended {
		// Already running (e.g. a second admin resumed first): nothing to do
		// and nothing to audit.
		c.JSON(http.StatusOK, gin.H{"suspended": false})
		return
	}
	if err := ResumeJanitor(h.DB); err != nil {
		respondErr(c, err)
		return
	}
	h.Audit.WithContext(c).Success("SYSTEM_MAINTENANCE", "janitor", "operation", "janitor_resume", "was_suspended_for", st.Reason)
	h.log(c).Warn("janitor resumed by admin")
	c.JSON(http.StatusOK, gin.H{"suspended": false})
}

func (h *Handler) SuspendJanitorHandler(c *gin.Context) {
	var req struct {
		Reason string `json:"reason"`
	}
	// The body is optional: an empty POST suspends with the default reason.
	if c.Request.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
		if err != nil {
			respondErr(c, ErrBadRequest, err)
			return
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				respondErr(c, ErrBadRequest, err)
				return
			}
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		req.Reason = "suspended by admin"
	}

	if err := SuspendJanitor(h.DB, req.Reason); err != nil {
		respondErr(c, err)
		return
	}
	h.Audit.WithContext(c).Success("SYSTEM_MAINTENANCE", "janitor", "operation", "janitor_suspend", "reason", req.Reason)
	h.log(c).WithField("reason", req.Reason).Warn("janitor suspended by admin")

	st, err := h.janitorStatus()
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}
