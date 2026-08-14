package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// healthCheckTimeout bounds the DB probe. A load balancer polling this endpoint
// needs a fast verdict: hanging until the SQLite busy_timeout expires would make
// an overloaded server look down to the balancer while it is merely slow.
const healthCheckTimeout = 2 * time.Second

// HandleHealth reports whether the service can actually do its job: reach the
// database and write to the storage directory.
//
// It is deliberately unauthenticated — a load balancer or uptime monitor has no
// credentials — so the response body names which check failed but never leaks
// paths, driver errors, or configuration. The detail goes to the log instead.
func (h *Handler) HandleHealth(c *gin.Context) {
	checks := gin.H{}
	healthy := true

	if err := h.checkDatabase(); err != nil {
		h.log(c).WithError(err).Error("health: database check failed")
		checks["database"] = "fail"
		healthy = false
	} else {
		checks["database"] = "ok"
	}

	if err := h.checkStorage(); err != nil {
		h.log(c).WithError(err).Error("health: storage check failed")
		checks["storage"] = "fail"
		healthy = false
	} else {
		checks["storage"] = "ok"
	}

	status := http.StatusOK
	body := gin.H{"status": "ok", "checks": checks}
	if !healthy {
		// 503 rather than 500: the service is unavailable but may recover, which
		// is what a balancer needs to know to stop routing without alerting on a
		// crash.
		status = http.StatusServiceUnavailable
		body["status"] = "unhealthy"
	}

	// Health must reflect the live state, never a cached one.
	c.Header("Cache-Control", "no-store")
	c.JSON(status, body)
}

// checkDatabase verifies the connection is usable, not merely open. SQLite hands
// back a handle that only fails on first use, so this issues a real query.
func (h *Handler) checkDatabase() error {
	sqlDB, err := h.DB.DB()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()

	return sqlDB.PingContext(ctx)
}

// checkStorage verifies the base directory is present and actually writable.
//
// A stat alone would pass on a full disk or a read-only remount — the two
// failure modes that matter for an artifact server — so this writes and removes
// a probe file.
func (h *Handler) checkStorage() error {
	info, err := os.Stat(h.BaseDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errNotDirectory
	}

	f, err := os.CreateTemp(h.BaseDir, ".health-*")
	if err != nil {
		return err
	}
	name := f.Name()

	// Close before removing, and remove even if the close failed, so a probe
	// never leaks a file into the storage tree the sync worker would then pick
	// up as an artifact.
	closeErr := f.Close()
	rmErr := os.Remove(name)

	if closeErr != nil {
		return closeErr
	}
	return rmErr
}

// errNotDirectory reports a BaseDir that exists but is not a directory.
var errNotDirectory = errors.New("storage base path is not a directory")
