package api

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (h *Handler) StartJanitor(ctx context.Context, period time.Duration) {
	ticker := time.NewTicker(period)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.RunCleanup()
			case <-ctx.Done():
				h.Log.Info("Janitor: shutting down")
				return
			}
		}
	}()
}

func (h *Handler) RunCleanup() {
	var expired []MetaResource
	now := time.Now().UTC()

	// Find resources where expiry is set and is in the past
	err := h.DB.Where("expires_at IS NOT NULL AND expires_at <= ?", now).
		Where("immutable = ? OR immutable IS NULL", false).
		Find(&expired).Error
	if err != nil {
		h.Log.WithError(err).Error("Janitor: failed to query expired records")
		return
	}

	for _, res := range expired {
		if h.Config.IsProtected(res.Path) {
			h.Log.Debugf("Janitor: skipping deletion of expired resource %s because the path is PROTECTED in config", res.Path)

			// Optional: Clear the expiry in the DB so we stop checking this file
			// every minute, or leave it so it deletes if the config changes later.
			continue
		}

		fullPath := filepath.Join(h.BaseDir, filepath.Clean(res.Path))

		info, err := os.Stat(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File already gone? Just clean the DB
				h.DB.Delete(&res)
			}
			continue
		}

		if info.IsDir() {
			// 2. Only delete directories if they are empty
			isEmpty, err := isDirEmpty(fullPath)
			if err != nil {
				h.Log.Errorf("Janitor: error checking dir %s: %v", res.Path, err)
				continue
			}
			if !isEmpty {
				// Skip for now. It will be deleted in a later run
				// once the files inside it expire.
				continue
			}
		}

		// Safe to remove (File or Empty Dir)
		if err := os.Remove(fullPath); err != nil {
			h.Log.Errorf("Janitor: failed to remove %s: %v", res.Path, err)
			continue
		}

		// 4. Cleanup DB
		h.DB.Delete(&res)
		h.Audit.Success("SYSTEM_CLEANUP", res.Path, "reason", "expired")
	}
}

// isDirEmpty returns true if the directory contains no files/folders
func isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Read only one entry
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}
