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
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
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

// SyncFilesystem reconciles the database against what is on disk.
//
// Records whose file has disappeared are not deleted straight away: they are
// marked missing and removed only after storage.missing_grace_period, so an
// unmounted volume or a data dir that is briefly unavailable does not wipe
// tags, expiry and stream/group membership. A walk that fails, or a subtree
// that cannot be read, never counts as "missing".
//
// Like RunCleanup, one pass tags all of its lines with a run_id so a
// reconciliation that touches many paths reads as a single unit.
func (h *Handler) SyncFilesystem(ctx context.Context) {
	log := h.Log.WithFields(logrus.Fields{
		"worker": "sync",
		"run_id": uuid.NewString(),
	})
	log.Info("Sync: Starting reconciliation...")

	// A missing or non-directory data dir would make every record look
	// missing. That is a configuration problem, not a deletion.
	if fi, err := os.Stat(h.BaseDir); err != nil || !fi.IsDir() {
		log.WithError(err).Errorf("Sync: data dir %q is not available, skipping reconciliation", h.BaseDir)
		return
	}

	// 1. Pre-load all DB records into memory for fast lookup
	dbFiles := make(map[string]MetaResource)
	var allMeta []MetaResource
	if err := h.DB.Find(&allMeta).Error; err != nil {
		log.WithError(err).Error("Sync: failed to load records, skipping reconciliation")
		return
	}
	for _, m := range allMeta {
		dbFiles[m.Path] = m
	}

	foundOnDisk := make(map[string]bool)
	// Subtrees that could not be read. Their records are left untouched: not
	// being able to list a directory says nothing about whether its files exist.
	var unreadable []string

	// 2. Walk the filesystem
	err := filepath.WalkDir(h.BaseDir, func(path string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		// Calculate virtual URL path
		rel, _ := filepath.Rel(h.BaseDir, path)
		urlPath := "/" + filepath.ToSlash(rel)
		if urlPath == "/." {
			urlPath = "/"
		}

		if err != nil {
			if path == h.BaseDir {
				return err
			}
			// Returning the error would abort the whole walk and leave every
			// record not yet visited looking missing. Skip just this subtree.
			log.WithError(err).Warnf("Sync: cannot read %s, leaving its records unchanged", urlPath)
			unreadable = append(unreadable, urlPath)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !d.IsDir() && isTransientFile(d.Name()) {
			return nil
		}

		foundOnDisk[urlPath] = true

		meta, exists := dbFiles[urlPath]

		info, err := d.Info()
		if err != nil {
			// Gone between listing and stat (e.g. an upload's temp file being
			// renamed into place). The next pass sees the outcome.
			return nil
		}

		if d.IsDir() {
			if exists {
				if meta.Size != info.Size() || meta.ModTime.Unix() != info.ModTime().Unix() {
					meta.Size = info.Size()
					meta.ModTime = info.ModTime()
					if err := h.DB.Save(&meta).Error; err != nil {
						log.WithError(err).Errorf("Sync: Failed to update directory stats for %s", urlPath)
					}
				}
			}
			return nil
		}

		// CHANGE DETECTION LOGIC:
		// We re-hash only if:
		// - File is missing from DB
		// - Physical size is different
		// - Physical ModTime is different (we use Unix timestamps for reliable comparison)
		if !exists || meta.Size != info.Size() || meta.ModTime.Unix() != info.ModTime().Unix() {
			log.Infof("Sync: Processing %s (Size/Time mismatch) m.size:%v, f.size:%v, m.modtime:%v, f.modtime:%v", urlPath, meta.Size, info.Size(), meta.ModTime.Unix(), info.ModTime().Unix())
			h.processIncomingFile(ctx, path, urlPath, info)
		}

		return nil
	})

	if err != nil {
		// An incomplete walk cannot tell missing files from unvisited ones.
		log.WithError(err).Error("Sync: Walk failed, skipping missing-file cleanup")
		return
	}

	// 3. CLEANUP: records whose file is gone.
	h.reconcileMissing(log, dbFiles, foundOnDisk, unreadable)
}

// reconcileMissing applies the missing-file grace period to every record the
// walk did not find: mark it, delete it once the grace period has passed, or
// clear the mark if the file is back.
func (h *Handler) reconcileMissing(log *logrus.Entry, dbFiles map[string]MetaResource, foundOnDisk map[string]bool, unreadable []string) {
	ts := now()
	grace := h.Config.Storage.MissingGracePeriodDuration
	var marked, recovered, removed int

	for path, meta := range dbFiles {
		if foundOnDisk[path] || underAnyPath(path, unreadable) {
			if meta.MissingSince != nil {
				if err := h.DB.Model(&MetaResource{}).Where("id = ?", meta.ID).UpdateColumn("missing_since", nil).Error; err != nil {
					log.WithError(err).Errorf("Sync: failed to clear missing mark on %s", path)
					continue
				}
				log.Infof("Sync: %s is back on disk", path)
				h.Audit.Success("SYSTEM_SYNC_CLEANUP", path, "reason", "reappeared_on_disk", "missing_since", meta.MissingSince)
				recovered++
			}
			continue
		}

		// Our own temp files are never artifacts, so there is nothing to keep.
		expired := grace == 0 || isTransientFile(filepath.Base(path)) ||
			(meta.MissingSince != nil && ts.Sub(*meta.MissingSince) >= grace)

		if !expired {
			if meta.MissingSince == nil {
				if err := h.DB.Model(&MetaResource{}).Where("id = ?", meta.ID).UpdateColumn("missing_since", ts).Error; err != nil {
					log.WithError(err).Errorf("Sync: failed to mark %s missing", path)
					continue
				}
				log.Warnf("Sync: %s is missing on disk; its record is kept until %s", path, ts.Add(grace).Format(time.RFC3339))
				h.Audit.Success("SYSTEM_SYNC_CLEANUP", path, "reason", "marked_missing_on_disk", "delete_record_after", ts.Add(grace))
				marked++
			}
			continue
		}

		log.Infof("Sync: Removing ghost record from DB: %s", path)
		if err := h.DB.Delete(&meta).Error; err != nil {
			log.WithError(err).Errorf("Sync: failed to remove record %s", path)
			continue
		}
		h.Audit.Success("SYSTEM_SYNC_CLEANUP", path, "reason", "missing_on_disk", "missing_since", meta.MissingSince)
		removed++

		if meta.GroupID != nil {
			meta.DeleteAfter = nil // gone; don't let its old deadline extend the group's
			if err := meta.syncGroupDeadline(h.DB); err != nil {
				log.WithError(err).WithField("group_id", *meta.GroupID).Warn("Sync: sync group deadline failed")
			}
			if err := h.cleanupGroupIfEmpty(meta.GroupID); err != nil {
				log.WithError(err).WithField("group_id", *meta.GroupID).Warn("Sync: group cleanup failed")
			}
		}
	}

	if marked+recovered+removed > 0 {
		log.WithFields(logrus.Fields{
			"marked_missing": marked,
			"reappeared":     recovered,
			"removed":        removed,
			"grace_period":   grace.String(),
		}).Info("Sync: reconciled missing files")
	}
}

// underAnyPath reports whether urlPath is one of roots or lies beneath one.
func underAnyPath(urlPath string, roots []string) bool {
	for _, r := range roots {
		if urlPath == r || strings.HasPrefix(urlPath, r+"/") {
			return true
		}
	}
	return false
}

// isTransientFile reports whether name is a short-lived file yaar itself
// creates in the data dir: an upload still being written
// (".<name>.upload-*", see HandleUpload) or the health probe (".health-*").
// They are renamed or removed within the request, so tracking them only
// produces records that go missing moments later.
func isTransientFile(name string) bool {
	if !strings.HasPrefix(name, ".") {
		return false
	}
	return strings.HasPrefix(name, ".health-") || strings.Contains(name, ".upload-")
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
	h.Audit.Success("SYSTEM_SYNC_CLEANUP", urlPath, "reason", "new file on disk", "size", meta.Size, "sha256", meta.SHA256)

	h.DB.Save(&meta)
}

// hashThrottleBytesPerSec bounds how fast the background sync worker reads from
// disk, so a large re-hash cannot saturate I/O and starve request handlers.
//
// The previous form — a flat 5ms sleep per 32KB chunk — worked out to roughly
// 6 MB/s, which put a 1GB artifact at ~2.7 minutes of wall time regardless of
// how idle the server was. A pause budgeted against elapsed time keeps the same
// "stay out of the way" property at a usable ceiling.
const hashThrottleBytesPerSec = 64 * 1024 * 1024

func (h *Handler) calculateHashes(ctx context.Context, reader io.Reader) (string, string, string, int64) {
	md5 := md5.New()
	sha1 := sha1.New()
	sha256 := sha256.New()

	// Stream to file and all hashers at once
	multi := io.MultiWriter(md5, sha1, sha256)

	// 1MB balances syscall overhead against the memory a concurrent scan holds.
	buffer := make([]byte, 1024*1024)

	written := int64(0)
	start := time.Now()

	for {
		// Check if app is shutting down
		select {
		case <-ctx.Done():
			return "", "", "", 0
		default:
		}

		n, err := reader.Read(buffer)
		if n > 0 {
			n, werr := multi.Write(buffer[:n])
			written += int64(n)
			if werr != nil {
				return "", "", "", 0
			}

			// THROTTLING: sleep only when we are running ahead of the budget,
			// so an idle server hashes at full speed up to the ceiling.
			budget := time.Duration(float64(written) / hashThrottleBytesPerSec * float64(time.Second))
			if ahead := budget - time.Since(start); ahead > 0 {
				timer := time.NewTimer(ahead)
				select {
				case <-ctx.Done():
					timer.Stop()
					return "", "", "", 0
				case <-timer.C:
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", "", 0
		}
	}

	return hex.EncodeToString(md5.Sum(nil)), hex.EncodeToString(sha1.Sum(nil)), hex.EncodeToString(sha256.Sum(nil)), written
}
