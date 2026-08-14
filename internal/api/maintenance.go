package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// runVacuum executes the SQLite VACUUM and reports how long it took.
//
// Shared by the admin endpoint and the scheduled worker so both go through the
// same statement and logging. VACUUM cannot run inside a transaction, so this
// calls Exec directly rather than going through a GORM transaction.
//
// Note the cost: the pool is capped at MaxOpenConns(1), so VACUUM holds the only
// connection and every concurrent request waits on it. That is why the scheduled
// run is infrequent.
func (h *Handler) runVacuum(log *logrus.Entry) (time.Duration, error) {
	log.Info("Maintenance: Starting database VACUUM...")
	start := time.Now()

	if err := h.DB.Exec("VACUUM").Error; err != nil {
		log.WithError(err).Error("Maintenance: VACUUM failed")
		return 0, err
	}

	duration := time.Since(start)
	log.Infof("Maintenance: VACUUM completed in %v", duration)
	return duration, nil
}

// HandleVacuum executes the SQLite VACUUM command to shrink the database file.
//
// Retained alongside the scheduled run so an admin can reclaim space immediately
// after a large cleanup rather than waiting for the next interval.
func (h *Handler) HandleVacuum(c *gin.Context) {
	duration, err := h.runVacuum(h.log(c))
	if err != nil {
		h.Audit.WithContext(c).Failure("SYSTEM_MAINTENANCE", "database", err, "operation", "vacuum")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Vacuum failed: " + err.Error()})
		return
	}

	// Audit the action
	h.Audit.WithContext(c).Success("SYSTEM_MAINTENANCE", "database", "operation", "vacuum",
		"trigger", "manual", "duration_ms", duration.Milliseconds())

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"message":  "Database optimized and file size reduced.",
		"duration": duration.String(),
	})
}

// StartVacuumScheduler runs VACUUM periodically until ctx is cancelled.
//
// The first run is deferred by one full interval rather than firing at startup:
// a restart loop would otherwise vacuum on every boot, and VACUUM monopolises
// the single SQLite connection.
func (h *Handler) StartVacuumScheduler(ctx context.Context, period time.Duration) {
	if period <= 0 {
		h.Log.Info("Maintenance: scheduled VACUUM disabled")
		return
	}

	ticker := time.NewTicker(period)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				log := h.Log.WithFields(logrus.Fields{
					"worker": "vacuum",
					"run_id": uuid.NewString(),
				})

				duration, err := h.runVacuum(log)
				if err != nil {
					// Audited as a background action (no gin context), matching
					// how the janitor and sync workers record their outcomes.
					h.Audit.Failure("SYSTEM_MAINTENANCE", "database", err, "operation", "vacuum",
						"trigger", "scheduled")
					continue
				}

				h.Audit.Success("SYSTEM_MAINTENANCE", "database", "operation", "vacuum",
					"trigger", "scheduled", "duration_ms", duration.Milliseconds())

			case <-ctx.Done():
				h.Log.Info("Maintenance: vacuum scheduler shutting down")
				return
			}
		}
	}()
}

// GetJanitorErrors returns the list of active janitor cleanup failures
func (h *Handler) GetJanitorErrors(c *gin.Context) {
	h.cleanupMutex.RLock()
	defer h.cleanupMutex.RUnlock()

	failures := make([]*CleanupFailure, 0, len(h.failedCleanups))
	for _, f := range h.failedCleanups {
		failures = append(failures, f)
	}

	// Sort failures by key to ensure a stable, deterministic response
	sort.Slice(failures, func(i, j int) bool {
		return failures[i].Key < failures[j].Key
	})

	c.JSON(http.StatusOK, failures)
}

// ClearJanitorError clears a specific janitor failure from the active list
func (h *Handler) ClearJanitorError(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	h.clearCleanupFailure(key)

	h.log(c).WithField("key", key).Info("Admin cleared janitor failure")
	h.Audit.WithContext(c).Success("SYSTEM_MAINTENANCE", key, "operation", "clear_janitor_error")

	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Error cleared"})
}
