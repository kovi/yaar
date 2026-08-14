package api

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/config"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type CleanupFailure struct {
	Key        string    `json:"key"`  // File path or Group ID/Name
	Type       string    `json:"type"` // "file" or "group"
	Attempts   int       `json:"attempts"`
	RetryAfter time.Time `json:"retry_after"`
	LastError  string    `json:"last_error"`
}

type Handler struct {
	BaseDir   string
	DB        *gorm.DB
	Config    *config.Config
	Log       *logrus.Entry
	Audit     *audit.Auditor
	StartTime time.Time
	// TriggerSync, when set, exposes POST /_/api/system/sync inside the
	// admin-gated `system` group. It is optional so tests and any embedding
	// that runs without a SyncController simply do not register the route.
	TriggerSync    func()
	failedCleanups map[string]*CleanupFailure
	cleanupMutex   sync.RWMutex
}

// log returns the request-scoped logger when one is available, falling back to
// the handler's own logger.
//
// The request-scoped entry carries request_id (and, once Identify has run, the
// caller's identity), so handler lines can be correlated with the audit entry
// for the same request. c may be nil, and the lookup tolerates a missing or
// wrong-typed value, so background workers and helpers shared between the
// request and janitor paths call this exactly like handlers do — that shared
// use is why most call sites reached for h.Log directly in the first place.
func (h *Handler) log(c *gin.Context) *logrus.Entry {
	if c != nil {
		if v, ok := c.Get("logger"); ok {
			if e, ok := v.(*logrus.Entry); ok {
				return e
			}
		}
	}
	return h.Log
}

// GetFileMetaBatch loads metadata for many paths in a single query, keyed by
// path. Directory listings used to call GetFileMeta once per entry, so a
// directory holding 50k artifacts issued 50k queries against a MaxOpenConns(1)
// SQLite handle. Paths absent from the DB are simply absent from the map.
//
// The IN list is chunked because SQLite rejects statements over its variable
// limit (SQLITE_MAX_VARIABLE_NUMBER, commonly 32766 on modern builds, 999 on
// older ones); the chunk size stays well under both.
func (h *Handler) GetFileMetaBatch(paths []string) (map[string]*MetaResource, error) {
	out := make(map[string]*MetaResource, len(paths))
	if len(paths) == 0 {
		return out, nil
	}

	const chunkSize = 500
	for start := 0; start < len(paths); start += chunkSize {
		end := min(start+chunkSize, len(paths))

		var batch []MetaResource
		err := h.DB.Preload("Tags").
			Joins("Group").
			Joins("Group.Stream").
			Where("meta_resources.path IN ?", paths[start:end]).
			Find(&batch).Error
		if err != nil {
			return nil, err
		}

		for i := range batch {
			out[batch[i].Path] = &batch[i]
		}
	}

	return out, nil
}

func (h *Handler) GetFileMeta(path string) (*MetaResource, error) {
	var res MetaResource
	r := h.DB.Preload("Tags").
		Joins("Group").
		Joins("Group.Stream").
		Where("meta_resources.path = ?", path).
		Limit(1).Find(&res)

	if r.Error != nil {
		return nil, r.Error
	}

	if r.RowsAffected != 1 {
		return nil, nil
	}

	return &res, nil
}
