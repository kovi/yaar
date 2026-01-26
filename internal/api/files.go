package api

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/utils"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func (h *Handler) fsPath(path string) string {
	return filepath.Join(h.BaseDir, filepath.Clean(path))
}

func dbPath(path string) string {
	return filepath.Clean(path)
}

func logger(c *gin.Context) *logrus.Entry {
	return c.MustGet("logger").(*logrus.Entry)
}

// ServeFile handles serving file with GET/HEAD
// Adds headers from db and calls c.File on fsPath
func (h *Handler) ServeFile(c *gin.Context, path string) {
	log := logger(c)
	fsPath := h.fsPath(path)
	log.WithField("fspath", fsPath).Infof("ServeFile")

	p := dbPath(path)
	meta, err := h.GetFileMeta(p)
	if err == nil && meta != nil {
		if len(meta.SHA1) != 0 {
			c.Header("X-Checksum-Sha1", meta.SHA1)
			c.Header("ETag", meta.SHA256)
		}
		if len(meta.MD5) != 0 {
			c.Header("X-Checksum-Md5", meta.MD5)
		}
		if len(meta.SHA256) != 0 {
			c.Header("X-Checksum-Sha256", meta.SHA256)
		}
		if len(meta.ContentType) != 0 {
			c.Header("Content-Type", meta.ContentType)
		}
	}

	c.File(fsPath)
}

/* ===================== WRITE ===================== */

func (h *Handler) HandleUpload(c *gin.Context) {
	urlPath := dbPath(c.Request.URL.Path)
	method := c.Request.Method
	contentType := c.ContentType()
	log := logger(c).WithField("path", urlPath)
	allowed_paths := c.GetStringSlice("allowed_paths")

	if c.Request.ContentLength > h.Config.Storage.MaxUploadSizeBytes {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, errors.New("file too large"), "MaxUploadSizeBytes", h.Config.Storage.MaxUploadSizeBytes)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("File too large. Maximum allowed: %s", h.Config.Storage.MaxUploadSize),
		})
		return
	}

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
		f, _ := fileHeader.Open()
		fileReader = f
		contentType = fileHeader.Header.Get("Content-Type")
	} else {
		// Raw upload: path includes filename
		finalRelativePath = urlPath
		fileReader = c.Request.Body
	}
	defer fileReader.Close()

	fullPath := filepath.Join(h.BaseDir, filepath.Clean(finalRelativePath))

	// Overwrite Check
	stat, err := os.Stat(fullPath)
	if err == nil && method == http.MethodPost {
		var msg string
		if stat.IsDir() {
			msg = "directory with same name already exists"
		} else {
			msg = "file exists"
		}
		c.JSON(http.StatusConflict, gin.H{"error": msg})
		return
	}

	opts := ModifyOptions{
		IgnoreProtected: err != nil, // Allow if new file, block if overwrite
		IsUpload:        true,
	}

	if ok, msg := h.CanModify(finalRelativePath, allowed_paths, opts); !ok {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, finalRelativePath, fmt.Errorf(msg))
		c.JSON(403, gin.H{"error": msg})
		return
	}

	// Extract Policy Headers
	keepLatest := c.GetHeader("X-KeepLatest") == "true"
	tags := c.GetHeader("X-Tags")
	expiresHeader := c.GetHeader("X-Expires")
	clientSha256 := c.GetHeader("X-Checksum-Sha256")
	clientSha1 := c.GetHeader("X-Checksum-Sha1")
	clientMd5 := c.GetHeader("X-Checksum-Md5")
	streamHeader := c.GetHeader("X-Stream")
	stream, group, err := utils.ParseStream(streamHeader)
	// Validation: Stream requires Group
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// If KeepLatest is requested, Stream/Group MUST be present
	if keepLatest && stream == "" {
		c.JSON(400, gin.H{"error": "X-KeepLatest requires an X-Stream header"})
		return
	}
	expiresAt, err := utils.ParseExpiry(expiresHeader)
	if err != nil {
		c.JSON(400, gin.H{"error": "X-Expires: " + err.Error()})
		return
	}

	// Prepare Hashing and Saving
	os.MkdirAll(filepath.Dir(fullPath), 0755)
	out, err := os.Create(fullPath)
	if err != nil {
		log.WithError(err).Errorf("failed to open file: %v", fullPath)
		c.JSON(400, gin.H{"error": "X-Expires: " + err.Error()})
		return
	}
	defer out.Close()

	md5 := md5.New()
	sha1 := sha1.New()
	sha256 := sha256.New()

	// Stream to file and all hashers at once
	multi := io.MultiWriter(out, md5, sha1, sha256)
	limitedBody := io.LimitReader(fileReader, h.Config.Storage.MaxUploadSizeBytes)
	written, _ := io.Copy(multi, limitedBody)

	if written >= h.Config.Storage.MaxUploadSizeBytes {
		// If the next read returns data, they exceeded the limit
		// (Checking one extra byte to be sure)
		buf := make([]byte, 1)
		if n, _ := fileReader.Read(buf); n > 0 {
			os.Remove(fullPath) // Delete partial file
			h.Audit.WithContext(c).Failure(audit.ActionUpload, fullPath, errors.New("file contect exceeded limit"), "MaxUploadSizeBytes", h.Config.Storage.MaxUploadSizeBytes)
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "File content exceeded limit"})
			return
		}
	}

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
		os.Remove(fullPath)

		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, errors.New(mismatchErr), "status", "corrupted")

		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Integrity check failed",
			"details": mismatchErr,
		})
		return
	}

	// 4. Update Database
	var res MetaResource
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		res.Path = finalRelativePath
		res.Type = ResourceTypeFile
		res.ModTime = time.Now()
		res.Size = written
		if err := tx.Where(MetaResource{Path: finalRelativePath}).FirstOrCreate(&res).Error; err != nil {
			return err
		}

		res.MD5 = sumMD5
		res.SHA1 = sumSHA1
		res.SHA256 = sumSHA256
		res.ContentType = contentType
		if res.ContentType == "" {
			res.ContentType = "application/octet-stream"
		}

		if expiresHeader != "" {
			res.ExpiresAt = &expiresAt
		}

		if streamHeader != "" {
			res.Stream = &stream
			res.Group = &group
			res.PolicyKeepLatest = &keepLatest
			if keepLatest {
				// Update stale files to expire immediately
				// Same stream, flagged for KeepLatest, but NOT in the new group.
				err := tx.Model(&MetaResource{}).
					Where("stream = ? AND `group` != ? AND policy_keep_latest = ?", *res.Stream, *res.Group, true).
					Update("expires_at", time.Now()).Error
				if err != nil {
					return err
				}
			}
		}

		if tags != "" {
			if err := tx.Where("resource_id = ?", res.ID).Delete(&MetaTag{}).Error; err != nil {
				return err
			}

			ts := parseTagString(tags)
			for i := range ts {
				ts[i].ResourceID = res.ID
			}

			if len(ts) > 0 {
				if err := tx.Create(&ts).Error; err != nil {
					return err
				}
			}
		}

		// Save the final state (Updates existing or finishes the Create)
		return tx.Save(&res).Error
	})

	if err != nil {
		h.Audit.WithContext(c).Failure(audit.ActionUpload, urlPath, err)
		log.WithError(err).Error("db sync failed")
		c.JSON(500, gin.H{"error": "Database sync failed"})
		return
	}

	// 4. Audit Success
	h.Audit.WithContext(c).Success(audit.ActionUpload, urlPath, "size", written, "sha256", res.SHA256)

	status := http.StatusOK
	if method == http.MethodPost {
		status = http.StatusCreated
	}
	c.JSON(status, res)
}

func (h *Handler) DeleteEntry(c *gin.Context) {
	log := logger(c)
	path := dbPath(c.Request.URL.Path)
	fsPath := h.fsPath(path)
	log.WithField("fspath", fsPath).Infof("about to delete")
	_, err := os.Stat(fsPath)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	allowed_paths := c.GetStringSlice("allowed_paths")
	if ok, msg := h.CanModify(path, allowed_paths, ModifyOptions{}); !ok {
		h.Audit.WithContext(c).Failure(audit.ActionDelete, path, errors.New(msg))
		c.JSON(403, gin.H{"error": msg})
		return
	}

	// 2. COLLECT: Find all metadata paths that will be affected
	// We do this before physical deletion so we have a record of what we are losing
	var affectedPaths []string
	childPattern := path
	if !strings.HasSuffix(childPattern, "/") {
		childPattern += "/%"
	} else {
		childPattern += "%"
	}

	h.DB.Model(&MetaResource{}).
		Where("path = ? OR path LIKE ?", path, childPattern).
		Pluck("path", &affectedPaths)

	err = os.RemoveAll(fsPath)
	if err != nil {
		h.Audit.WithContext(c).Failure(audit.ActionDelete, path, err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	err = h.DB.Transaction(func(tx *gorm.DB) error {
		return tx.Where("path = ? OR path LIKE ?", path, childPattern).
			Delete(&MetaResource{}).Error
	})

	if err != nil {
		h.Log.WithError(err).Error("failed to clear metadata after physical delete")
		// We don't return 500 here because the physical files ARE gone.
	}

	h.Audit.WithContext(c).Success(
		audit.ActionDelete,
		path,
		"deleted_count", len(affectedPaths),
		"affected_paths", affectedPaths, // This will be a JSON array in the log
	)
	c.Status(http.StatusNoContent)
}
