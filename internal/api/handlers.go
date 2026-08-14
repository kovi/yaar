package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/ptr"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (h *Handler) GetMeta(c *gin.Context) {
	scopes := c.GetStringSlice("allowed_paths")
	path := dbPath(c.Param("path"))
	fsPath := h.fsPath(path)

	stat, err := os.Stat(fsPath)
	h.log(c).Infof("GetMeta path=%q fsPath=%q err=%v", path, fsPath, err)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	// --- Directory listing ---
	if stat.IsDir() {
		limit, offset, err := parseListPagination(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		entries, err := os.ReadDir(fsPath)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		// os.ReadDir already sorts by filename, so the window is stable across
		// requests. Slice before doing any per-entry work: a directory holding
		// 50k artifacts should cost one page, not a full materialization.
		total := len(entries)
		if offset > total {
			offset = total
		}
		entries = entries[offset:]
		if limit > 0 && limit < len(entries) {
			entries = entries[:limit]
		}

		// Collect this page's paths and stat them once, then resolve all
		// metadata in a single batched query instead of one per entry.
		type dirEntry struct {
			path string
			info os.FileInfo
		}
		pageEntries := make([]dirEntry, 0, len(entries))
		paths := make([]string, 0, len(entries))
		for _, e := range entries {
			entryPath := filepath.Join(path, e.Name())
			info, err := e.Info()
			if err != nil {
				h.log(c).WithError(err).WithField("path", entryPath).Warnf("requesting getmeta err'd")
				continue
			}
			pageEntries = append(pageEntries, dirEntry{path: entryPath, info: info})
			paths = append(paths, entryPath)
		}

		metas, err := h.GetFileMetaBatch(paths)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		result := make([]ResourceResponse, 0, len(pageEntries))
		for _, e := range pageEntries {
			var res ResourceResponse
			r := metas[e.path]
			if r == nil {
				res = ToResponse(e.path, e.info, scopes, h.Config)
			} else {
				if !e.info.IsDir() && r.Size != e.info.Size() {
					h.log(c).WithField("path", e.path).Warnf("different size in db and fs: %v != %v", r.Size, e.info.Size())
				}
				res = r.ToResourceResponse(scopes, h.Config)
			}
			result = append(result, res)
		}

		// X-Total-Count lets a client page without a body-shape change; the
		// response stays a bare array for existing consumers.
		c.Header("X-Total-Count", strconv.Itoa(total))
		c.JSON(http.StatusOK, result)
		return
	}

	r, err := h.GetFileMeta(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var res ResourceResponse
	if r == nil {
		res = ToResponse(path, stat, scopes, h.Config)
	} else {
		if !stat.IsDir() && r.Size != stat.Size() {
			h.log(c).WithField("path", path).Warnf("different size in db and fs: %v != %v", r.Size, stat.Size())
		}
		res = r.ToResourceResponse(scopes, h.Config)
	}

	c.JSON(http.StatusOK, res)
}

func toResourceType(s os.FileInfo) ResourceType {
	if s.IsDir() {
		return ResourceTypeDir
	}
	return ResourceTypeFile
}

func (h *Handler) PatchMeta(c *gin.Context) {
	path := dbPath(c.Param("path"))
	var req MetaPatchRequest

	if err := BindJSONStrict(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	// File must exist on filesystem
	fullPath := filepath.Join(h.BaseDir, filepath.Clean(path))
	stat, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		h.log(c).WithField("path", fullPath).Warn("Patch attempted on non-existent file")
		c.JSON(http.StatusNotFound, gin.H{"error": "Physical path not found on disk"})
		return
	}

	var r MetaResource
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("path = ?", path).Limit(1).Find(&r)

		if result.RowsAffected == 0 {
			h.log(c).Infof("Initial creation for path: %s", path)
			r = MetaResource{
				Path:    path,
				Type:    toResourceType(stat),
				Size:    stat.Size(),
				ModTime: stat.ModTime(),
			}

			if err := tx.Create(&r).Error; err != nil {
				h.log(c).WithError(err).Error("Create failed inside tx")
				return err
			}
		} else {
			if r.Size != stat.Size() || r.ModTime.Unix() != stat.ModTime().Unix() {
				r.Size = stat.Size()
				r.ModTime = stat.ModTime()
			}
		}

		if r.Immutable != nil && *r.Immutable {
			if req.Retention == nil || req.Retention.Immutable == nil || *req.Retention.Immutable == true {
				return &AppError{ErrResourceLocked, "", nil}
			}
			// If we reach here, resource is locked but req.Immutable is false (Unlocking)
			h.Audit.WithContext(c).Success(audit.ActionPatchMeta, path, "operation", "unlocked")
		}

		if req.Stream != nil && req.Group != nil {
			if r.GroupID != nil {
				// Changing group is only allowed when the stream does not have
				// auto_expire_previous set — moving would demote the old group and
				// trigger its immediate deletion.
				var currentGroup Group
				if err := tx.Preload("Stream").First(&currentGroup, r.GroupID).Error; err != nil {
					return fmt.Errorf("load current group: %w", err)
				}
				if ptr.Val(currentGroup.Stream.AutoExpirePrevious) {
					return &AppError{ErrResourceAlreadyInGroup, "", nil}
				}
			}

			var stream Stream
			if err := tx.Where("name = ?", *req.Stream).FirstOrCreate(&stream, Stream{Name: *req.Stream}).Error; err != nil {
				return fmt.Errorf("resolve stream: %w", err)
			}

			var group Group
			if err := tx.Where("stream_id = ? AND name = ?", stream.ID, *req.Group).
				FirstOrCreate(&group, Group{StreamID: stream.ID, Name: *req.Group}).Error; err != nil {
				return fmt.Errorf("resolve group: %w", err)
			}

			r.GroupID = &group.ID

		} else if req.Stream != nil || req.Group != nil {
			return &AppError{ErrStreamGroupBothRequired, "", nil}

		}
		if req.ContentType != nil {
			r.ContentType = *req.ContentType
		}

		if req.Retention != nil {
			err = ApplyRetentionPatch(&r, req.Retention)
			if err != nil {
				return &AppError{ErrBadRequest, err.Error(), nil}
			}
		}

		if err := r.Save(tx); err != nil {
			return err
		}

		if req.Tags != nil {
			// Delete old and add new as discussed before
			tx.Where("resource_id = ?", r.ID).Delete(&MetaTag{})
			tags := parseTagString(*req.Tags)
			if len(tags) > 0 {
				// Manually set ResourceID and create
				for i := range tags {
					tags[i].ResourceID = r.ID
				}
				if err := tx.Create(&tags).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})

	// Handle the custom error
	if err != nil {
		respondErr(c, err)
		return
	}

	scopes := c.GetStringSlice("allowed_paths")
	c.JSON(http.StatusOK, r.ToResourceResponse(scopes, h.Config))
}

func (h *Handler) PostMeta(c *gin.Context) {
	logrus.Infof("handle:postmeta: p=%v", c.Param("path"))
	var req struct {
		CreateDir bool   `json:"create_dir"`
		RenameTo  string `json:"rename_to"`
		MoveTo    string `json:"move_to"`
	}

	if err := BindJSONStrict(c, &req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	log := h.log(c)
	dbPath := dbPath(c.Param("path"))

	scopes := c.GetStringSlice("allowed_paths")
	if ok, msg := h.CanModify(dbPath, scopes, ModifyOptions{}); !ok {
		h.Audit.WithContext(c).Failure(audit.ActionPatchMeta, dbPath, errors.New(msg))
		c.JSON(403, gin.H{"error": msg})
		return
	}
	fsPath := h.fsPath(dbPath)
	if req.CreateDir {
		if err := os.MkdirAll(fsPath, 0755); err != nil {
			c.JSON(500, gin.H{"error": "Failed to create directory"})
			return
		}

		h.Audit.WithContext(c).Success(audit.ActionMkdir, dbPath)
		c.JSON(201, gin.H{"status": "created"})
		return
	}

	if req.RenameTo != "" {
		oldURLPath := dbPath
		fullOldPath := h.fsPath(dbPath)
		newURLPath := filepath.Join(filepath.Dir(oldURLPath), filepath.Clean(req.RenameTo))
		fullNewPath := h.fsPath(newURLPath)

		if h.Config.IsProtected(oldURLPath) {
			h.Audit.WithContext(c).Failure(audit.ActionRename, oldURLPath, fmt.Errorf("protected path"))
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Overwriting files in this directory is prohibited by system policy.",
			})
			return
		}

		if filepath.Dir(fullOldPath) != filepath.Dir(fullNewPath) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "rename cannot change directory",
			})
			return
		}

		// We already checked oldPath .
		// Now we must check if the NEW path is also in scope.
		if ok, msg := h.CanModify(newURLPath, scopes, ModifyOptions{}); !ok {
			h.Audit.WithContext(c).Failure(audit.ActionRename, newURLPath, errors.New(msg))
			c.JSON(403, gin.H{"error": msg})
			return
		}

		// 1. Filesystem Rename
		if err := os.Rename(fullOldPath, fullNewPath); err != nil {
			// The os error embeds absolute on-disk paths, which would hand the
			// storage root and directory layout to any caller. It goes to the log;
			// the client gets the outcome only.
			log.WithError(err).Infof("Rename failed: req:%v p:%v -> %v, dbPath:%v", req.RenameTo, fullOldPath, fullNewPath, dbPath)
			c.JSON(500, gin.H{"error": "Filesystem rename failed"})
			return
		}

		// 2. Database Update (Recursive)
		err := h.DB.Transaction(func(tx *gorm.DB) error {
			// Rewrite only the leading prefix for the folder and all nested children.
			// SQL: UPDATE meta_resources SET path = '/new' || substr(path, length('/old') + 1)
			//      WHERE path = '/old' OR path LIKE '/old/%'

			oldPrefix := oldURLPath
			newPrefix := newURLPath

			// Important: append trailing slash for the LIKE match to avoid partial name matches
			// e.g., don't rename "/images-backup" when renaming "/images"
			childMatch := oldPrefix + "/%"

			result := tx.Model(&MetaResource{}).
				Where("path = ? OR path LIKE ?", oldPrefix, childMatch).
				Update("path", rewritePathPrefix(oldPrefix, newPrefix))

			if result.Error != nil {
				return result.Error
			}

			log.Infof("Renamed %d metadata records from %s to %s", result.RowsAffected, oldPrefix, newPrefix)
			return nil
		})

		if err != nil {
			c.JSON(500, gin.H{"error": "Database path update failed"})
			return
		}

		h.Audit.WithContext(c).Success(audit.ActionRename, newURLPath)
		c.JSON(200, gin.H{"status": "renamed", "new_path": newURLPath})
		return
	}

	if req.MoveTo != "" {
		h.HandleMove(c, req.MoveTo)
		return
	}

	c.JSON(400, gin.H{"error": "invalid action"})
}

// rewritePathPrefix builds the SQL expression that replaces the leading
// oldPrefix of a path with newPrefix, leaving the rest of the path untouched.
//
// REPLACE() cannot be used here: it rewrites *every* occurrence of oldPrefix in
// the string, so renaming "/data" → "/renamed" turned "/data/data/inner/f.txt"
// into "/renamed/renamed/inner/f.txt" while the file on disk became
// "/renamed/data/inner/f.txt" — silently orphaning the metadata. Slicing off the
// prefix by length and concatenating the new one is occurrence-independent.
//
// Callers must restrict the UPDATE to rows that actually carry the prefix
// (path = oldPrefix OR path LIKE oldPrefix || '/%'); this expression assumes it.
func rewritePathPrefix(oldPrefix, newPrefix string) clause.Expr {
	// length() counts characters, and substr() indexes by character, so the two
	// agree on multi-byte paths. len(oldPrefix) would count bytes — hence the
	// SQL-side length() rather than a Go-side constant.
	return gorm.Expr("? || substr(path, length(?) + 1)", newPrefix, oldPrefix)
}

func (h *Handler) HandleMove(c *gin.Context, moveTo string) {
	oldURLPath := c.Param("path")
	newURLPath := filepath.Clean("/" + moveTo)

	fullOldPath := filepath.Join(h.BaseDir, filepath.Clean(oldURLPath))
	fullNewPath := filepath.Join(h.BaseDir, filepath.Clean(newURLPath))

	allowedPaths := c.GetStringSlice("allowed_paths")

	// 1. SECURITY CHECK: Source
	// User must be able to "Delete/Modify" the source
	if ok, msg := h.CanModify(oldURLPath, allowedPaths, ModifyOptions{}); !ok {
		c.JSON(403, gin.H{"error": "Source permission denied: " + msg})
		return
	}

	// 2. SECURITY CHECK: Destination
	// User must be able to "Create/Write" at the destination
	if ok, msg := h.CanModify(newURLPath, allowedPaths, ModifyOptions{IsNewFile: true}); !ok {
		c.JSON(403, gin.H{"error": "Destination permission denied: " + msg})
		return
	}

	// 3. PREVENT CIRCULAR MOVE
	// You cannot move a folder inside itself
	if strings.HasPrefix(newURLPath, oldURLPath+"/") {
		c.JSON(400, gin.H{"error": "Cannot move a directory into its own subdirectory"})
		return
	}

	// 4. FILESYSTEM MOVE
	// Ensure destination parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullNewPath), 0755); err != nil {
		c.JSON(500, gin.H{"error": "Failed to create destination parent directory"})
		return
	}

	if err := os.Rename(fullOldPath, fullNewPath); err != nil {
		// As in the rename path above: the os error names absolute disk paths, so
		// it is logged rather than returned.
		h.log(c).WithError(err).WithFields(logrus.Fields{
			"from": fullOldPath,
			"to":   fullNewPath,
		}).Error("Move: filesystem move failed")
		c.JSON(500, gin.H{"error": "Filesystem move failed"})
		return
	}

	// 5. RECURSIVE DATABASE UPDATE
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		oldPrefix := oldURLPath
		newPrefix := newURLPath
		childMatch := oldPrefix + "/%"

		// Update the path for the item and all its children (if it's a directory)
		result := tx.Model(&MetaResource{}).
			Where("path = ? OR path LIKE ?", oldPrefix, childMatch).
			Update("path", rewritePathPrefix(oldPrefix, newPrefix))

		return result.Error
	})

	if err != nil {
		h.log(c).WithError(err).Error("Move: Database path update failed")
		c.JSON(500, gin.H{"error": "Metadata sync failed"})
		return
	}

	h.Audit.WithContext(c).Success("FILE_MOVE", oldURLPath, "to", newURLPath)
	c.JSON(200, gin.H{"status": "moved", "from": oldURLPath, "to": newURLPath})
}
