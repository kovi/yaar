package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/utils"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func (f *FileResponse) updateWithFileInfo(i os.FileInfo) {
	f.IsDir = i.IsDir()
	f.Size = i.Size()
	f.ModTime = i.ModTime()
}

func (h *Handler) toResponseFromMeta(c *gin.Context, meta MetaResource) FileResponse {
	o := FileResponse{}
	o.Name = meta.Path
	o.IsDir = meta.Type == ResourceTypeDir
	allowedPaths := c.GetStringSlice("allowed_paths")
	o.Policy = ResourcePolicy{
		IsImmutable: meta.Immutable != nil && *meta.Immutable,
		IsProtected: h.Config.IsProtected(meta.Path),
		IsAllowed:   auth.IsInScopes(meta.Path, allowedPaths),
	}
	if meta.ExpiresAt != nil {
		o.ExpiresAt = *meta.ExpiresAt
	}
	o.Tags = meta.Tags
	o.ContentType = meta.ContentType
	if meta.Stream != nil {
		o.Stream = *meta.Stream
	}
	if meta.Group != nil {
		o.Group = *meta.Group
	}
	if meta.PolicyKeepLatest != nil {
		o.KeepLatest = *meta.PolicyKeepLatest
	}
	o.ChecksumMD5 = meta.MD5
	o.ChecksumSHA1 = meta.SHA1
	o.ChecksumSHA256 = meta.SHA256

	return o
}

func (h *Handler) toResponse(c *gin.Context, urlPath string, i os.FileInfo) FileResponse {
	allowedPaths := c.GetStringSlice("allowed_paths")

	o := FileResponse{}
	o.Name = i.Name()
	o.IsDir = i.IsDir()
	o.Size = i.Size()
	o.ModTime = i.ModTime()
	o.Policy = ResourcePolicy{
		IsProtected: h.Config.IsProtected(urlPath),
		IsAllowed:   auth.IsInScopes(urlPath, allowedPaths),
	}

	meta, _ := h.GetFileMeta(urlPath)
	if meta == nil {
		return o
	}

	if meta.ExpiresAt != nil {
		o.ExpiresAt = *meta.ExpiresAt
	}
	o.Tags = meta.Tags
	o.ContentType = meta.ContentType
	if meta.Stream != nil {
		o.Stream = *meta.Stream
	}
	if meta.Group != nil {
		o.Group = *meta.Group
	}
	if meta.PolicyKeepLatest != nil {
		o.KeepLatest = *meta.PolicyKeepLatest
	}
	o.ChecksumMD5 = meta.MD5
	o.ChecksumSHA1 = meta.SHA1
	o.ChecksumSHA256 = meta.SHA256
	o.Policy.IsImmutable = meta.Immutable != nil && *meta.Immutable

	return o
}

func (h *Handler) GetMeta(c *gin.Context) {
	path := dbPath(c.Param("path"))
	fsPath := h.fsPath(path)

	stat, err := os.Stat(fsPath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	// --- Directory listing ---
	if stat.IsDir() {
		entries, err := os.ReadDir(fsPath)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		result := make([]FileResponse, 0, len(entries))

		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}

			f := h.toResponse(c, filepath.Join(path, e.Name()), info)
			result = append(result, f)
		}

		c.JSON(http.StatusOK, result)
		return
	}

	f := h.toResponse(c, path, stat)
	c.JSON(http.StatusOK, f)
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

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	stream, group := "", ""
	var err error
	if req.Stream != nil {
		stream, group, err = utils.ParseStream(*req.Stream)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
	}

	// File must exist on filesystem
	fullPath := filepath.Join(h.BaseDir, filepath.Clean(path))
	stat, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		h.Log.WithField("path", fullPath).Warn("Patch attempted on non-existent file")
		c.JSON(http.StatusNotFound, gin.H{"error": "Physical path not found on disk"})
		return
	}

	var expiresAt time.Time
	if req.ExpiresAt != nil {
		expiresAt, err = utils.ParseExpiry(*req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expiry: " + err.Error()})
			return
		}
	}

	var resource MetaResource
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("path = ?", path).Limit(1).Find(&resource)

		if result.RowsAffected == 0 {
			h.Log.Infof("Initial creation for path: %s", path)
			resource = MetaResource{Path: path, Type: toResourceType(stat)}

			if err := tx.Create(&resource).Error; err != nil {
				h.Log.WithError(err).Error("Create failed inside tx")
				return err
			}
		}

		if resource.Immutable != nil && *resource.Immutable {
			if req.Immutable == nil || *req.Immutable == true {
				return errors.New("RESOURCE_LOCKED")
			}
			// If we reach here, resource is locked but req.Immutable is false (Unlocking)
			h.Audit.WithContext(c).Success(audit.ActionPatchMeta, path, "action", "unlocked")
		}

		updateData := map[string]any{}
		if req.ExpiresAt != nil {
			updateData["expires_at"] = expiresAt
		}
		if req.Immutable != nil {
			updateData["immutable"] = *req.Immutable
		}
		if req.KeepLatest != nil {
			updateData["policy_keep_latest"] = *req.KeepLatest
		}
		if req.ContentType != nil {
			updateData["content_type"] = *req.ContentType
		}
		if req.Stream != nil {
			updateData["stream"] = stream
			updateData["group"] = group
		}

		if len(updateData) > 0 {
			if err := tx.Model(&resource).Updates(updateData).Error; err != nil {
				return err
			}
		}

		if req.Tags != nil {
			// Delete old and add new as discussed before
			tx.Where("resource_id = ?", resource.ID).Delete(&MetaTag{})
			tags := parseTagString(*req.Tags)
			if len(tags) > 0 {
				// Manually set ResourceID and create
				for i := range tags {
					tags[i].ResourceID = resource.ID
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
		if err.Error() == "RESOURCE_LOCKED" {
			c.JSON(http.StatusForbidden, gin.H{"error": "This resource is locked and cannot be modified."})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resource)
}

func (h *Handler) PostMeta(c *gin.Context) {
	logrus.Infof("handle:postmeta: p=%v", c.Param("path"))
	var req struct {
		CreateDir bool   `json:"create_dir"`
		RenameTo  string `json:"rename_to"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}

	log := logger(c)
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

		// We already checked oldPath .
		// Now we must check if the NEW path is also in scope.
		if ok, msg := h.CanModify(newURLPath, scopes, ModifyOptions{}); !ok {
			h.Audit.WithContext(c).Failure(audit.ActionDelete, newURLPath, errors.New(msg))
			c.JSON(403, gin.H{"error": msg})
			return
		}

		// 1. Filesystem Rename
		if err := os.Rename(fullOldPath, fullNewPath); err != nil {
			log.WithError(err).Infof("Rename failed: req:%v p:%v -> %v, dbPath:%v", req.RenameTo, fullOldPath, fullNewPath, dbPath)
			c.JSON(500, gin.H{"error": "Filesystem rename failed: " + err.Error()})
			return
		}

		// 2. Database Update (Recursive)
		err := h.DB.Transaction(func(tx *gorm.DB) error {
			// We use a raw SQL REPLACE to update the prefix for the folder and all nested children
			// SQL: UPDATE meta_resources SET path = REPLACE(path, '/old', '/new')
			//      WHERE path = '/old' OR path LIKE '/old/%'

			oldPrefix := oldURLPath
			newPrefix := newURLPath

			// Important: append trailing slash for the LIKE match to avoid partial name matches
			// e.g., don't rename "/images-backup" when renaming "/images"
			childMatch := oldPrefix + "/%"

			result := tx.Model(&MetaResource{}).
				Where("path = ? OR path LIKE ?", oldPrefix, childMatch).
				Update("path", gorm.Expr("REPLACE(path, ?, ?)", oldPrefix, newPrefix))

			if result.Error != nil {
				return result.Error
			}

			h.Log.Infof("Renamed %d metadata records from %s to %s", result.RowsAffected, oldPrefix, newPrefix)
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

	c.JSON(400, gin.H{"error": "invalid action"})
}
