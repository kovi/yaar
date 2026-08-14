package api

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/ptr"
	"gorm.io/gorm"
)

func (h *Handler) fsPath(path string) string {
	return filepath.Join(h.BaseDir, filepath.Clean(path))
}

func dbPath(path string) string {
	return filepath.Clean(path)
}

// resourceAuditMap converts a MetaResource to a map[string]any using the same
// JSON representation served to clients. Group, Tags, and retention fields are
// included when preloaded. Policy (permission computation) is excluded.
func resourceAuditMap(r MetaResource, cfg *config.Config) map[string]any {
	resp := r.ToResourceResponse(nil, cfg)
	b, _ := json.Marshal(resp)
	var m map[string]any
	json.Unmarshal(b, &m)
	delete(m, "policy")
	return m
}

// ServeFile handles serving file with GET/HEAD
// Adds headers from db and calls c.File on fsPath
func (h *Handler) ServeFile(c *gin.Context, path string) {
	log := h.log(c)
	fsPath := h.fsPath(path)
	log.WithField("fspath", fsPath).Infof("ServeFile")

	p := dbPath(path)
	meta, err := h.GetFileMeta(p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if meta != nil {
		if len(meta.SHA1) != 0 {
			c.Header("X-Checksum-Sha1", meta.SHA1)
		}
		if len(meta.MD5) != 0 {
			c.Header("X-Checksum-Md5", meta.MD5)
		}
		if len(meta.SHA256) != 0 {
			c.Header("X-Checksum-Sha256", meta.SHA256)
			// The ETag is the SHA256, so it hangs off the SHA256 check. It used
			// to be nested inside the SHA1 branch, which silently dropped it for
			// any file hashed with SHA256 but not SHA1.
			c.Header("ETag", meta.SHA256)
		}
		if len(meta.ContentType) != 0 {
			c.Header("Content-Type", meta.ContentType)
		}
	}

	c.File(fsPath)

	// Update sliding window expiry
	if meta != nil && meta.ExpiryAfterDownload != nil {
		meta.LastDownloadedAt = ptr.Of(time.Now())
		if err := meta.Save(h.DB); err != nil {
			log.WithError(err).Errorf("saving after onDownload")
		}
	}
}

/* ===================== WRITE ===================== */

func (h *Handler) HandleUpload(c *gin.Context) {
	urlPath := dbPath(c.Request.URL.Path)
	method := c.Request.Method
	contentType := c.ContentType()
	log := h.log(c).WithField("path", urlPath)
	allowed_paths := c.GetStringSlice("allowed_paths")
	expectedSize := c.Request.ContentLength

	var fileReader io.ReadCloser
	var finalRelativePath string

	// 1. Resolve Filename/Path
	if strings.HasPrefix(contentType, "multipart/form-data") {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			c.JSON(400, gin.H{"error": "missing file in form"})
			return
		}
		// Filename comes from form, directory comes from URL
		finalRelativePath = filepath.Join(urlPath, fileHeader.Filename)
		fileReader, err = fileHeader.Open()
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to open uploaded file"})
			return
		}
		contentType = fileHeader.Header.Get("Content-Type")
		expectedSize = fileHeader.Size
	} else {
		// Raw upload: path includes filename
		finalRelativePath = urlPath
		fileReader = c.Request.Body
	}
	if fileReader == nil {
		c.JSON(400, gin.H{"error": "missing file"})
		return
	}
	defer fileReader.Close()

	if h.Config.Storage.MaxUploadSizeBytes > 0 && expectedSize > h.Config.Storage.MaxUploadSizeBytes {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, errors.New("file too large"), "MaxUploadSizeBytes", h.Config.Storage.MaxUploadSizeBytes)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("File too large. Maximum allowed: %s", h.Config.Storage.MaxUploadSize),
		})
		return
	}

	finalRelativePath = filepath.Clean(finalRelativePath)
	fullPath := filepath.Join(h.BaseDir, finalRelativePath)
	log.Infof("upload request: %v expSize:%v", fullPath, expectedSize)

	// Overwrite Check
	stat, err := os.Stat(fullPath)
	if err == nil && method == http.MethodPost {
		var msg, existing string
		if stat.IsDir() {
			msg, existing = "directory with same name already exists", "directory"
		} else {
			msg, existing = "file exists", "file"
		}
		// POST is create-only, so a collision is a rejected upload like any other
		// and belongs in the audit trail alongside the size and permission checks.
		h.Audit.WithContext(c).Failure(audit.ActionUpload, finalRelativePath, errors.New(msg), "existing", existing)
		c.JSON(http.StatusConflict, gin.H{"error": msg})
		return
	}

	opts := ModifyOptions{
		IsNewFile: err != nil,
		IsUpload:  true,
	}

	if ok, msg := h.CanModify(finalRelativePath, allowed_paths, opts); !ok {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, finalRelativePath, errors.New(msg))
		c.JSON(403, gin.H{"error": msg})
		return
	}

	retentionFromHeaders, err := ExtractRetentionHeaders(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tags, tagsExists := c.Request.Header["X-Tags"]
	clientSha256 := c.GetHeader("X-Checksum-Sha256")
	clientSha1 := c.GetHeader("X-Checksum-Sha1")
	clientMd5 := c.GetHeader("X-Checksum-Md5")

	streamName := strings.TrimSpace(c.GetHeader(HeaderStream))
	groupName := strings.TrimSpace(c.GetHeader(HeaderGroup))
	if streamName == "" && groupName != "" || streamName != "" && groupName == "" {
		respondErr(c, ErrStreamGroupBothRequired)
		return
	}

	// Prepare Hashing and Saving
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		// Without this check the failure surfaced later as a confusing
		// os.Create error against a path whose parent was never created.
		log.WithError(err).Errorf("failed to create parent directory for: %v", fullPath)
		respondErr(c, ErrBadRequest, err)
		return
	}

	// Upload to a temporary file next to the target and rename into place only
	// once the body is fully written and verified. Writing straight to fullPath
	// meant a crash, a truncated body or a failed checksum left a partial
	// artifact live at its final URL. os.Rename is atomic within a directory, so
	// a reader sees either the previous file or the complete new one.
	tmpFile, err := os.CreateTemp(filepath.Dir(fullPath), "."+filepath.Base(fullPath)+".upload-*")
	if err != nil {
		log.WithError(err).Errorf("failed to create temp file for: %v", fullPath)
		respondErr(c, ErrBadRequest, err)
		return
	}
	tmpPath := tmpFile.Name()
	out := tmpFile

	// Any early return below leaves the temp file behind; clean it up unless the
	// rename already consumed it.
	committed := false
	outClosed := false
	defer func() {
		if !outClosed {
			out.Close()
		}
		if !committed {
			os.Remove(tmpPath)
		}
	}()

	md5 := md5.New()
	sha1 := sha1.New()
	sha256 := sha256.New()

	// Stream to file and all hashers at once
	multi := io.MultiWriter(out, md5, sha1, sha256)
	var r io.Reader
	if h.Config.Storage.MaxUploadSizeBytes > 0 {
		r = io.LimitReader(fileReader, h.Config.Storage.MaxUploadSizeBytes)
	} else {
		r = fileReader
	}
	// Every failure path below simply returns: the deferred cleanup discards the
	// temp file, and because nothing was written to fullPath, an overwrite that
	// fails midway leaves the previous artifact intact.
	written, err := io.Copy(multi, r)
	if err != nil {
		log.WithError(err).Errorf("Upload interrupted after %d bytes", written)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if h.Config.Storage.MaxUploadSizeBytes > 0 && written >= h.Config.Storage.MaxUploadSizeBytes {
		// If the next read returns data, they exceeded the limit
		// (Checking one extra byte to be sure)
		buf := make([]byte, 1)
		if n, _ := fileReader.Read(buf); n > 0 {
			h.Audit.WithContext(c).Failure(audit.ActionUpload, fullPath, errors.New("file contents exceeded limit"), "MaxUploadSizeBytes", h.Config.Storage.MaxUploadSizeBytes)
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "File content exceeded limit"})
			return
		}
	}

	if expectedSize > 0 && written != expectedSize {
		log.Errorf("Incomplete upload: expected %d, got %d", expectedSize, written)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Request body was truncated. Please retry the upload.",
		})
		return
	}

	// Finalize the file handle
	// We close manually here instead of relying solely on 'defer'
	// so we can check for errors (e.g., "disk full" often only shows up on Close)
	if err := out.Close(); err != nil {
		log.WithError(err).Error("Failed to finalize file on disk")
		c.JSON(500, gin.H{"error": "Failed to save file"})
		return
	}
	outClosed = true

	// inbound integrity check
	sumMD5 := hex.EncodeToString(md5.Sum(nil))
	sumSHA1 := hex.EncodeToString(sha1.Sum(nil))
	sumSHA256 := hex.EncodeToString(sha256.Sum(nil))
	mismatchErr := ""
	if clientSha256 != "" && !strings.EqualFold(clientSha256, sumSHA256) {
		mismatchErr = fmt.Sprintf("SHA256 mismatch: expected %s, got %s", clientSha256, sumSHA256)
	} else if clientSha1 != "" && !strings.EqualFold(clientSha1, sumSHA1) {
		mismatchErr = fmt.Sprintf("SHA1 mismatch: expected %s, got %s", clientSha1, sumSHA1)
	} else if clientMd5 != "" && !strings.EqualFold(clientMd5, sumMD5) {
		mismatchErr = fmt.Sprintf("MD5 mismatch: expected %s, got %s", clientMd5, sumMD5)
	}

	if mismatchErr != "" {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, errors.New(mismatchErr), "status", "corrupted")

		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Integrity check failed",
			"details": mismatchErr,
		})
		return
	}

	// Commit: the body is complete and verified, so publish it atomically.
	// os.Rename replaces any existing file in one step, so a concurrent reader
	// sees either the old artifact or the new one — never a partial write.
	if err := os.Rename(tmpPath, fullPath); err != nil {
		log.WithError(err).Errorf("failed to publish upload to %v", fullPath)
		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, err)
		c.JSON(500, gin.H{"error": "Failed to save file"})
		return
	}
	committed = true

	// CreateTemp uses 0600; the published artifact needs the ordinary 0644 that
	// os.Create would have produced (minus umask, which does not apply here).
	if err := os.Chmod(fullPath, 0644); err != nil {
		log.WithError(err).Warnf("failed to set permissions on %v", fullPath)
	}

	// 4. Update Database
	var res MetaResource

	res.Type = ResourceTypeFile
	// Use the filesystem's actual mtime so the sync worker sees no modtime
	// mismatch and doesn't needlessly re-hash and re-log this file.
	if fi, err := os.Stat(fullPath); err == nil {
		res.ModTime = fi.ModTime()
	} else {
		res.ModTime = now()
	}
	res.Size = written
	res.MD5 = sumMD5
	res.SHA1 = sumSHA1
	res.SHA256 = sumSHA256
	res.ContentType = contentType
	if res.ContentType == "" {
		res.ContentType = "application/octet-stream"
	}

	if retentionFromHeaders != nil {
		if err := retentionFromHeaders.Validate(&res); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where(MetaResource{Path: finalRelativePath}).
			Assign(res).
			FirstOrCreate(&res).Error; err != nil {
			return err
		}

		if streamName != "" && groupName != "" {
			if res.GroupID != nil {
				return errors.New("RESOURCE_ALREADY_IN_GROUP")
			}

			var stream Stream
			if err := tx.Where("name = ?", streamName).FirstOrCreate(&stream, Stream{Name: streamName}).Error; err != nil {
				return fmt.Errorf("resolve stream: %w", err)
			}

			var group Group
			if err := tx.Where("stream_id = ? AND name = ?", stream.ID, groupName).
				FirstOrCreate(&group, Group{StreamID: stream.ID, Name: groupName}).Error; err != nil {
				return fmt.Errorf("resolve group: %w", err)
			}

			res.GroupID = &group.ID
		}

		if retentionFromHeaders != nil {
			if err = ApplyRetentionPatch(&res, retentionFromHeaders); err != nil {
				return &AppError{ErrBadRequest, err.Error(), nil}
			}
		}

		if tagsExists {
			if err := tx.Where("resource_id = ?", res.ID).Delete(&MetaTag{}).Error; err != nil {
				return err
			}

			var ts []MetaTag
			for tag := range tags {
				t := parseTagString(tags[tag])
				ts = append(ts, t...)
			}

			for i := range ts {
				ts[i].ResourceID = res.ID
			}

			if len(ts) > 0 {
				if err := tx.Create(&ts).Error; err != nil {
					return err
				}
			}
		}

		return res.Save(tx)
	})

	if err != nil {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, err)
		log.WithError(err).Error("db sync failed")
		c.JSON(500, gin.H{"error": "Database sync failed"})
		return
	}

	// 4. Audit Success
	meta := resourceAuditMap(res, h.Config)
	if streamName != "" {
		meta["stream"] = streamName
		meta["group"] = groupName
	}
	if len(tags) > 0 {
		meta["tags"] = tags
	}
	h.Audit.WithContext(c).Success(audit.ActionUpload, urlPath, "meta", meta)

	status := http.StatusOK
	if method == http.MethodPost {
		status = http.StatusCreated
	}
	c.JSON(status, res.ToResourceResponse(allowed_paths, h.Config))
}

func (h *Handler) DeleteEntry(c *gin.Context) {
	log := h.log(c)
	path := dbPath(c.Request.URL.Path)
	fsPath := h.fsPath(path)
	log.WithField("fspath", fsPath).Infof("about to delete")

	if _, err := os.Stat(fsPath); err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	allowedPaths := c.GetStringSlice("allowed_paths")
	if ok, msg := h.CanModify(path, allowedPaths, ModifyOptions{}); !ok {
		h.Audit.WithContext(c).Failure(audit.ActionDelete, path, errors.New(msg))
		c.JSON(http.StatusForbidden, gin.H{"error": msg})
		return
	}

	childPattern := path
	if !strings.HasSuffix(childPattern, "/") {
		childPattern += "/%"
	} else {
		childPattern += "%"
	}

	// Load all affected resources before deletion so we can run cleanup per entry.
	var affected []MetaResource
	h.DB.Preload("Group.Stream").Preload("Tags").
		Where("path = ? OR path LIKE ?", path, childPattern).
		Find(&affected)

	if ok, msg := h.canDeleteChildren(path, affected); !ok {
		h.Audit.WithContext(c).Failure(audit.ActionDelete, path, errors.New(msg))
		c.JSON(http.StatusForbidden, gin.H{"error": msg})
		return
	}

	if err := os.RemoveAll(fsPath); err != nil {
		h.Audit.WithContext(c).Failure(audit.ActionDelete, path, err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Delete all DB records in one shot.
	if err := h.DB.Where("path = ? OR path LIKE ?", path, childPattern).
		Delete(&MetaResource{}).Error; err != nil {
		log.WithError(err).Error("failed to clear metadata after physical delete")
	}

	// Per-resource cleanup: groups, audit.
	affectedPaths := make([]string, 0, len(affected))
	for _, r := range affected {
		affectedPaths = append(affectedPaths, r.Path)
		h.Audit.WithContext(c).Success(audit.ActionDelete, r.Path, "reason", "manual", "meta", resourceAuditMap(r, h.Config))
		if r.GroupID != nil {
			if err := h.cleanupGroupIfEmpty(r.GroupID); err != nil {
				log.WithError(err).WithField("group_id", *r.GroupID).Warn("delete: group cleanup failed")
			}
		}
	}

	h.Audit.WithContext(c).Success(
		audit.ActionDelete,
		path,
		"deleted_count", len(affectedPaths),
		"affected_paths", affectedPaths,
	)

	if err := h.pruneEmptyDirs(filepath.Dir(path)); err != nil {
		log.WithError(err).Error("delete: failed to prune parents")
	}

	c.Status(http.StatusNoContent)
}

type BatchDeleteRequest struct {
	Paths []string `json:"paths"`
}

type BatchDeleteResponse struct {
	Deleted []string          `json:"deleted"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// deduplicatePaths removes paths that are descendants of other paths in the
// slice, since deleting a parent already removes all children recursively.
func deduplicatePaths(paths []string) []string {
	sort.Strings(paths) // parents always sort before their children
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		covered := false
		for _, kept := range result {
			if strings.HasPrefix(p, kept+"/") {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, p)
		}
	}
	return result
}

func (h *Handler) BatchDelete(c *gin.Context) {
	var req BatchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "paths required"})
		return
	}

	// Normalize all paths first, then deduplicate to avoid redundant child deletes.
	normalized := make([]string, 0, len(req.Paths))
	seen := make(map[string]struct{}, len(req.Paths))
	for _, rawPath := range req.Paths {
		p := dbPath(rawPath)
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			normalized = append(normalized, p)
		}
	}
	paths := deduplicatePaths(normalized)

	allowedPaths := c.GetStringSlice("allowed_paths")
	deleted := make([]string, 0, len(paths))
	allAffected := make([]string, 0)
	errs := make(map[string]string)

	for _, path := range paths {
		fsPath := h.fsPath(path)

		if _, err := os.Stat(fsPath); err != nil {
			h.Audit.WithContext(c).Failure(audit.ActionDelete, path, errors.New("not found"))
			errs[path] = "not found"
			continue
		}

		if ok, msg := h.CanModify(path, allowedPaths, ModifyOptions{}); !ok {
			h.Audit.WithContext(c).Failure(audit.ActionDelete, path, errors.New(msg))
			errs[path] = msg
			continue
		}

		childPattern := path
		if !strings.HasSuffix(childPattern, "/") {
			childPattern += "/%"
		} else {
			childPattern += "%"
		}

		var affected []MetaResource
		h.DB.Preload("Group.Stream").Preload("Tags").
			Where("path = ? OR path LIKE ?", path, childPattern).
			Find(&affected)

		if ok, msg := h.canDeleteChildren(path, affected); !ok {
			h.Audit.WithContext(c).Failure(audit.ActionDelete, path, errors.New(msg))
			errs[path] = msg
			continue
		}

		if err := os.RemoveAll(fsPath); err != nil {
			h.Audit.WithContext(c).Failure(audit.ActionDelete, path, err)
			errs[path] = err.Error()
			continue
		}

		if err := h.DB.Where("path = ? OR path LIKE ?", path, childPattern).
			Delete(&MetaResource{}).Error; err != nil {
			h.log(c).WithError(err).Error("batch delete: failed to clear metadata")
		}

		for _, r := range affected {
			allAffected = append(allAffected, r.Path)
			h.Audit.WithContext(c).Success(audit.ActionDelete, r.Path, "reason", "batch_delete", "meta", resourceAuditMap(r, h.Config))
			if r.GroupID != nil {
				if err := h.cleanupGroupIfEmpty(r.GroupID); err != nil {
					h.log(c).WithError(err).WithField("group_id", *r.GroupID).Warn("batch delete: group cleanup failed")
				}
			}
		}

		if err := h.pruneEmptyDirs(filepath.Dir(path)); err != nil {
			h.log(c).WithError(err).Error("batch delete: failed to prune parents")
		}

		deleted = append(deleted, path)
	}

	// Per-path outcomes are already audited inside the loop. Only emit a
	// batch-level summary when something was actually deleted, so a batch that
	// affected nothing (e.g. every path immutable) doesn't produce a
	// "SUCCESS, affected_paths:[]" entry. If some paths errored alongside
	// successes, record the summary as a FAILURE so its status is honest.
	if len(allAffected) > 0 {
		summary := h.Audit.WithContext(c)
		kv := []any{
			"deleted_count", len(allAffected),
			"affected_paths", allAffected,
			"reason", "batch_delete",
		}
		if len(errs) > 0 {
			summary.Failure(
				audit.ActionDelete,
				"batch",
				fmt.Errorf("%d of %d paths failed", len(errs), len(paths)),
				append(kv, "error_count", len(errs))...,
			)
		} else {
			summary.Success(audit.ActionDelete, "batch", kv...)
		}
	}

	c.JSON(http.StatusOK, BatchDeleteResponse{Deleted: deleted, Errors: errs})
}
