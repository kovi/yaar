package api

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type SyncController struct {
	handler   *Handler
	isRunning int32         // Atomic flag to prevent concurrent runs
	trigger   chan struct{} // Channel to request a sync
}

func NewSyncController(h *Handler) *SyncController {
	return &SyncController{
		handler: h,
		trigger: make(chan struct{}, 1), // Buffered so trigger doesn't block
	}
}

// Trigger sends a signal to start a sync as soon as possible
func (sc *SyncController) Trigger() {
	select {
	case sc.trigger <- struct{}{}:
	default:
		// Already a trigger pending, do nothing
	}
}

func (sc *SyncController) Start(ctx context.Context, startupDelay time.Duration, interval time.Duration) {
	go func() {
		sc.handler.Log.Infof("Sync: background worker standby (startup delay: %v)", startupDelay)

		// 1. Initial Startup Delay
		select {
		case <-time.After(startupDelay):
		case <-ctx.Done():
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			// Run the sync
			sc.runRecursiveSync(ctx)

			select {
			case <-ticker.C:
				// Scheduled run
			case <-sc.trigger:
				sc.handler.Log.Info("Sync: manual trigger received")
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (sc *SyncController) runRecursiveSync(ctx context.Context) {
	// Prevent multiple syncs from running at once
	if !atomic.CompareAndSwapInt32(&sc.isRunning, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&sc.isRunning, 0)

	sc.handler.SyncFilesystem(ctx)

	// 2. Add a tiny delay to yield CPU/IO to other processes
	time.Sleep(50 * time.Millisecond)
}

func (h *Handler) SyncFilesystem(ctx context.Context) {
	h.Log.Info("Sync: Starting reconciliation...")

	// 1. Pre-load all DB records into memory for fast lookup
	dbFiles := make(map[string]MetaResource)
	var allMeta []MetaResource
	h.DB.Find(&allMeta)
	for _, m := range allMeta {
		dbFiles[m.Path] = m
	}

	foundOnDisk := make(map[string]bool)

	// 2. Walk the filesystem
	err := filepath.WalkDir(h.BaseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate virtual URL path
		rel, _ := filepath.Rel(h.BaseDir, path)
		urlPath := "/" + filepath.ToSlash(rel)
		if urlPath == "/." {
			urlPath = "/"
		}

		foundOnDisk[urlPath] = true

		// SKIP: We don't need to add directories to the DB during sync.
		// If they exist in DB (e.g. they have tags), cleanup will handle them if they vanish.
		if d.IsDir() {
			return nil
		}

		info, _ := d.Info()
		meta, exists := dbFiles[urlPath]

		// CHANGE DETECTION LOGIC:
		// We re-hash only if:
		// - File is missing from DB
		// - Physical size is different
		// - Physical ModTime is different (we use Unix timestamps for reliable comparison)
		if !exists || meta.Size != info.Size() || meta.ModTime.Unix() != info.ModTime().Unix() {
			h.Log.Infof("Sync: Processing %s (Size/Time mismatch) m.size:%v, f.size:%v, m.modtime:%v, f.modtime:%v", urlPath, meta.Size, info.Size(), meta.ModTime.Unix(), info.ModTime().Unix())
			h.processIncomingFile(ctx, path, urlPath, info)
		}

		return nil
	})

	if err != nil {
		h.Log.WithError(err).Error("Sync: Walk failed")
	}

	// 3. CLEANUP: If a record exists in DB but is not on disk, remove it.
	// This works for both Files and Directories (if the Dir was in the DB).
	for path, meta := range dbFiles {
		if !foundOnDisk[path] {
			h.Log.Infof("Sync: Removing ghost record from DB: %s", path)
			h.DB.Delete(&meta)
			// Pass "nil" for context as per our new Auditor interface
			h.Audit.Success("SYSTEM_SYNC_CLEANUP", path, "reason", "missing_on_disk")
		}
	}
}

func (h *Handler) processIncomingFile(ctx context.Context, fullDiskPath, urlPath string, info os.FileInfo) {
	f, err := os.Open(fullDiskPath)
	if err != nil {
		return
	}
	defer f.Close()

	// Read first 512 bytes once...
	sniffBuffer := make([]byte, 512)
	n, _ := f.Read(sniffBuffer)

	// Use it for type detection
	contentType := http.DetectContentType(sniffBuffer[:n])

	// Reset file pointer to beginning so the hashing logic can read the whole file
	f.Seek(0, 0)

	md5sum, sha1sum, sha256sum, written := h.calculateHashes(ctx, f)
	select {
	case <-ctx.Done():
		return
	default:
	}
	var meta MetaResource
	h.DB.Where(MetaResource{Path: urlPath}).FirstOrCreate(&meta)

	meta.Type = ResourceTypeFile
	meta.Size = written
	meta.MD5 = md5sum
	meta.SHA1 = sha1sum
	meta.SHA256 = sha256sum
	meta.ModTime = info.ModTime()
	meta.ContentType = contentType

	h.DB.Save(&meta)
}

func (h *Handler) calculateHashes(ctx context.Context, reader io.Reader) (string, string, string, int64) {
	md5 := md5.New()
	sha1 := sha1.New()
	sha256 := sha256.New()

	// Stream to file and all hashers at once
	multi := io.MultiWriter(md5, sha1, sha256)
	// written, _ := io.Copy(multi, reader)

	// Use a small buffer to avoid memory spikes
	buffer := make([]byte, 32*1024)

	written := int64(0)
	for {
		// Check if app is shutting down
		select {
		case <-ctx.Done():
			return "", "", "", 0
		default:
		}

		n, err := reader.Read(buffer)
		if n > 0 {
			n, err = multi.Write(buffer[:n])
			written += int64(n)
			if err != nil {
				return "", "", "", 0
			}

			// THROTTLING: Yield to other goroutines every chunk
			time.Sleep(5 * time.Millisecond)
		}
		if err == io.EOF {
			break
		}
	}

	return hex.EncodeToString(md5.Sum(nil)), hex.EncodeToString(sha1.Sum(nil)), hex.EncodeToString(sha256.Sum(nil)), written
}
