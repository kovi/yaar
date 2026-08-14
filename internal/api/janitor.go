package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/sirupsen/logrus"
)

func (h *Handler) StartJanitor(ctx context.Context, period time.Duration) {
	ticker := time.NewTicker(period)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Per-item failures are tracked by registerCleanupFailure; this
				// catches the top-level query failures, which have no per-item
				// key and would otherwise vanish silently.
				if err := h.RunCleanup(); err != nil {
					h.Log.WithError(err).Error("Janitor: cleanup run failed")
				}
			case <-ctx.Done():
				h.Log.Info("Janitor: shutting down")
				return
			}
		}
	}()
}

// now returns now as stored in DB
func now() time.Time {
	return time.Now().UTC()
}

func (h *Handler) shouldSkipCleanup(key string) bool {
	h.cleanupMutex.RLock()
	defer h.cleanupMutex.RUnlock()

	if h.failedCleanups == nil {
		return false
	}

	state, exists := h.failedCleanups[key]
	if !exists {
		return false
	}
	return time.Now().Before(state.RetryAfter)
}

func (h *Handler) registerCleanupFailure(key string, cleanType string, err error) {
	h.cleanupMutex.Lock()
	defer h.cleanupMutex.Unlock()

	if h.failedCleanups == nil {
		h.failedCleanups = make(map[string]*CleanupFailure)
	}

	state, exists := h.failedCleanups[key]
	if !exists {
		state = &CleanupFailure{
			Key:  key,
			Type: cleanType,
		}
		h.failedCleanups[key] = state
	}

	state.Attempts++
	state.LastError = err.Error()

	// Exponential backoff: 2m, 4m, 8m, 16m... capped at 24 hours
	backoffMin := 2 * (1 << (state.Attempts - 1))
	if backoffMin > 1440 { // 24 hours
		backoffMin = 1440
	}

	state.RetryAfter = time.Now().Add(time.Duration(backoffMin) * time.Minute)
	h.Log.WithError(err).Errorf("Janitor cleanup failure on %s (%s). Scheduled retry at %v (attempt %d)", key, cleanType, state.RetryAfter, state.Attempts)
}

func (h *Handler) clearCleanupFailure(key string) {
	h.cleanupMutex.Lock()
	defer h.cleanupMutex.Unlock()

	if h.failedCleanups != nil {
		delete(h.failedCleanups, key)
	}
}

// RunCleanup performs one janitor pass.
//
// Every line it logs carries a run_id so the (potentially many) deletions from a
// single pass can be read as one unit instead of as unrelated lines interleaved
// with the sync worker's. The logger is threaded as a parameter rather than
// stored on Handler because the janitor and sync goroutines run concurrently.
func (h *Handler) RunCleanup() error {
	now := now()
	log := h.Log.WithFields(logrus.Fields{
		"worker": "janitor",
		"run_id": uuid.NewString(),
	})

	if err := h.cleanupExpiredGroups(log, now); err != nil {
		return fmt.Errorf("group cleanup: %w", err)
	}

	if err := h.cleanupUngroupedResources(log, now); err != nil {
		return fmt.Errorf("ungrouped cleanup: %w", err)
	}

	return nil
}

func (h *Handler) cleanupExpiredGroups(log *logrus.Entry, now time.Time) error {
	var groups []Group
	if err := h.DB.Preload("Stream").
		Where("effective_delete_after < ?", now).
		Find(&groups).Error; err != nil {
		return fmt.Errorf("query expired groups: %w", err)
	}

	for _, group := range groups {
		key := fmt.Sprintf("group:%d", group.ID)
		if h.shouldSkipCleanup(key) {
			continue
		}
		if err := h.cleanupGroup(log, &group); err != nil {
			log.WithError(err).WithField("group_id", group.ID).Error("janitor: failed to clean up group")
			h.registerCleanupFailure(key, "group", err)
		} else {
			h.clearCleanupFailure(key)
		}
	}

	return nil
}

func (h *Handler) cleanupGroup(log *logrus.Entry, group *Group) error {
	var members []MetaResource
	if err := h.DB.Where("group_id = ?", group.ID).Find(&members).Error; err != nil {
		return fmt.Errorf("load members: %w", err)
	}

	// A single immutable or protected member must not stop the rest of the group
	// from expiring: partition first, clean the expirable members, and keep the
	// group record alive for whatever remains.
	expirable := make([]MetaResource, 0, len(members))
	var retained []MetaResource
	for _, r := range members {
		if ptr.Val(r.Immutable) || h.Config.IsProtected(r.Path) {
			retained = append(retained, r)
			continue
		}
		expirable = append(expirable, r)
	}

	if len(retained) > 0 {
		paths := make([]string, 0, len(retained))
		for _, r := range retained {
			paths = append(paths, r.Path)
		}
		log.WithFields(logrus.Fields{
			"group_id": group.ID,
			"retained": paths,
			"expiring": len(expirable),
		}).Warn("janitor: group has protected or immutable members, expiring the rest")
	}

	for _, r := range expirable {
		fullPath := filepath.Join(h.BaseDir, r.Path)
		log.Infof("removing %v", fullPath)
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			log.WithError(err).WithField("path", r.Path).Error("janitor: failed to delete file, proceeding with metadata cleanup")
			return fmt.Errorf("delete file %s: %w", r.Path, err)
		}
	}

	if len(expirable) > 0 {
		ids := make([]uint, 0, len(expirable))
		for _, r := range expirable {
			ids = append(ids, r.ID)
		}
		if err := h.DB.Where("id IN ?", ids).Delete(&MetaResource{}).Error; err != nil {
			return fmt.Errorf("delete members: %w", err)
		}
	}

	if len(retained) == 0 {
		if err := h.DB.Delete(group).Error; err != nil {
			return fmt.Errorf("delete group: %w", err)
		}
	} else {
		// The group outlives this run. Clear its deadline so the janitor stops
		// re-selecting it every tick and re-reporting the same skip; a later
		// member change recomputes it via syncGroupDeadline.
		if err := h.DB.Model(group).Update("effective_delete_after", nil).Error; err != nil {
			return fmt.Errorf("clear group deadline: %w", err)
		}
	}

	for _, r := range expirable {
		meta := resourceAuditMap(r, h.Config)
		meta["stream"] = group.Stream.Name
		meta["group"] = group.Name
		h.Audit.Success("SYSTEM_CLEANUP", r.Path, "reason", "group_expired", "meta", meta)
	}

	for _, r := range retained {
		meta := resourceAuditMap(r, h.Config)
		meta["stream"] = group.Stream.Name
		meta["group"] = group.Name
		h.Audit.Failure("SYSTEM_CLEANUP", r.Path, errors.New("member is immutable or protected"), "reason", "group_expired_skipped", "meta", meta)
	}

	candidates := make(map[string]struct{}, len(expirable))
	for _, r := range expirable {
		candidates[filepath.Dir(r.Path)] = struct{}{}
	}
	h.pruneCandidates(log, candidates)

	return nil
}

func (h *Handler) cleanupUngroupedResources(log *logrus.Entry, now time.Time) error {
	var resources []MetaResource
	if err := h.DB.Where("group_id IS NULL AND delete_after < ? AND (immutable IS NULL OR immutable = false)", now).
		Find(&resources).Error; err != nil {
		return fmt.Errorf("query ungrouped: %w", err)
	}

	candidates := make(map[string]struct{}, len(resources))
	for _, r := range resources {
		key := fmt.Sprintf("file:%s", r.Path)
		if h.shouldSkipCleanup(key) {
			continue
		}

		if h.Config.IsProtected(r.Path) {
			log.WithField("path", r.Path).Warn("janitor: skipping protected resource, clearing deadline")
			h.DB.Model(&r).Update("delete_after", nil)
			h.clearCleanupFailure(key)
			continue
		}
		if err := h.deleteResource(log, &r, "expired"); err != nil {
			log.WithError(err).WithField("path", r.Path).Error("janitor: failed to expire resource")
			h.registerCleanupFailure(key, "file", err)
		} else {
			h.clearCleanupFailure(key)
			candidates[filepath.Dir(r.Path)] = struct{}{}
		}
	}

	// Prune emptied directories once per unique candidate, after all files
	// for this run are removed — so a wholly-expired subtree is walked once,
	// not once per file.
	h.pruneCandidates(log, candidates)

	return nil
}

func (h *Handler) deleteResource(log *logrus.Entry, r *MetaResource, reason string) error {
	fullPath := filepath.Join(h.BaseDir, r.Path)

	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete file: %w", err)
	}

	if err := h.DB.Delete(r).Error; err != nil {
		return fmt.Errorf("delete record: %w", err)
	}

	h.clearCleanupFailure(fmt.Sprintf("file:%s", r.Path))

	h.Audit.Success("SYSTEM_CLEANUP", r.Path, "reason", reason, "meta", resourceAuditMap(*r, h.Config))

	if r.GroupID != nil {
		r.DeleteAfter = nil // resource is gone; don't let its old deadline extend the group's
		if err := r.syncGroupDeadline(h.DB); err != nil {
			log.WithError(err).WithField("group_id", *r.GroupID).Warn("delete: sync group deadline failed")
		}
		if err := h.cleanupGroupIfEmpty(r.GroupID); err != nil {
			log.WithError(err).WithField("group_id", *r.GroupID).Warn("delete: group cleanup failed")
		}
	}

	return nil
}

func (h *Handler) cleanupGroupIfEmpty(groupID *uint) error {
	var count int64
	if err := h.DB.Model(&MetaResource{}).
		Where("group_id = ? AND delete_after IS NULL", *groupID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return h.DB.Delete(&Group{}, *groupID).Error
}

// pruneCandidates prunes each candidate directory, processing the deepest
// paths first so an upward walk consumes shallower candidates as it climbs
// (a later, already-removed candidate then costs a single ReadDir syscall).
func (h *Handler) pruneCandidates(log *logrus.Entry, candidates map[string]struct{}) {
	if len(candidates) == 0 {
		return
	}

	dirs := make([]string, 0, len(candidates))
	for d := range candidates {
		dirs = append(dirs, d)
	}
	// Deepest first: more path separators sorts earlier.
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/")
	})

	for _, d := range dirs {
		if err := h.pruneEmptyDirs(d); err != nil {
			log.WithError(err).WithField("path", d).Warn("janitor: prune failed")
		}
	}
}

func (h *Handler) pruneEmptyDirs(dirPath string) error {
	// pruneRoot is the nearest ancestor with PruneChildren, resolved lazily once.
	// Its authority covers every descendant — i.e. every level strictly below it
	// that we climb through — so we walk the ancestors a single time instead of
	// re-querying per directory. Authority ends when we reach pruneRoot itself.
	pruneRoot := ""
	resolvedPruneRoot := false

	for dirPath != "." && dirPath != "/" {
		fullPath := filepath.Join(h.BaseDir, dirPath)

		entries, err := os.ReadDir(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return fmt.Errorf("read dir %s: %w", dirPath, err)
		}

		if len(entries) > 0 {
			break
		}

		var dirResource MetaResource
		h.DB.Where("path = ?", dirPath).Limit(1).Find(&dirResource)
		hasDirRecord := dirResource.ID != 0

		if hasDirRecord && (ptr.Val(dirResource.Immutable) || h.Config.IsProtected(dirPath)) {
			break
		} else if !hasDirRecord && h.Config.IsProtected(dirPath) {
			break
		}

		parent := filepath.Dir(dirPath)

		// Remove if this dir has AutoPrune, or a PruneChildren ancestor authorizes
		// it (resolved once, then inherited for the rest of the upward walk).
		autoPrune := hasDirRecord && ptr.Val(dirResource.AutoPrune)

		// A directory that carries its own PruneChildren is user-configured and
		// must be kept (pruning it would silently discard that config), even
		// though its emptied descendants below were pruned to reach here.
		if hasDirRecord && ptr.Val(dirResource.PruneChildren) {
			break
		}

		// Resolve the nearest PruneChildren ancestor once and reuse it while we
		// stay strictly below it; its authority covers all descendants.
		if !resolvedPruneRoot {
			pruneRoot = h.pruneChildrenAncestor(dirPath)
			resolvedPruneRoot = true
		}
		coveredByPruneChildren := pruneRoot != "" && strings.HasPrefix(dirPath+"/", pruneRoot+"/") && dirPath != pruneRoot

		if !autoPrune && !coveredByPruneChildren {
			break
		}

		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove dir %s: %w", dirPath, err)
		}

		if hasDirRecord {
			h.DB.Delete(&dirResource)
		}

		dirPath = parent

	}

	return nil
}

// pruneChildrenAncestor returns the path of the nearest ancestor of dirPath
// with PruneChildren set, walking upward, or "" if none. A protected or
// immutable ancestor acts as a hard boundary: pruning authority does not
// cross it.
func (h *Handler) pruneChildrenAncestor(dirPath string) string {
	for ancestor := filepath.Dir(dirPath); ancestor != "." && ancestor != "/"; ancestor = filepath.Dir(ancestor) {
		var res MetaResource
		h.DB.Where("path = ?", ancestor).Limit(1).Find(&res)

		// A protected or immutable ancestor is a hard boundary.
		if h.Config.IsProtected(ancestor) || (res.ID != 0 && ptr.Val(res.Immutable)) {
			return ""
		}

		if res.ID != 0 && ptr.Val(res.PruneChildren) {
			return ancestor
		}
	}
	return ""
}
