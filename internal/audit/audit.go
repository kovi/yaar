package audit

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Constants for common actions to ensure consistency in logs
const (
	ActionUpload    = "FILE_UPLOAD"
	ActionDelete    = "FILE_DELETE"
	ActionRename    = "FILE_RENAME"
	ActionMkdir     = "DIR_CREATE"
	ActionPatchMeta = "META_PATCH"
	// ActionLogin records both successful and failed authentication attempts,
	// so a brute-force run is visible in the audit trail rather than silent.
	ActionLogin = "USER_LOGIN"
	// ActionUserCreate completes the account lifecycle in the trail. USER_UPDATE
	// and USER_DELETE were already recorded, so an admin minting an account —
	// including an admin one — used to be the only unlogged step.
	ActionUserCreate = "USER_CREATE"
	// ActionTokenCreate / ActionTokenDelete pair up so a credential's revocation
	// is as visible as its issuance.
	ActionTokenCreate = "TOKEN_CREATED"
	ActionTokenDelete = "TOKEN_DELETED"
	// ActionStreamUpdate records retention-policy edits. These decide what the
	// janitor later deletes, so an unlogged edit is an untraceable cause of a
	// mass deletion.
	ActionStreamUpdate = "STREAM_UPDATE"
	// ActionAuthDenied records authorization failures against an established
	// identity — a valid session probing admin routes, or a bad API token.
	ActionAuthDenied = "AUTH_DENIED"
)

type Auditor struct {
	log      *logrus.Logger
	filePath string
	// mirror duplicates every entry to the standard logger. Off by default: it
	// doubles audit write volume and the mirrored copy is subject to no
	// rotation at all, so it is opt-in for deployments that collect stdout.
	mirror bool

	writer *rotatingWriter
	// maxBackups mirrors the writer's retention count so the reader knows how
	// many rotated generations to page into.
	maxBackups int
}

// Option configures an Auditor at construction.
type Option func(*auditorConfig)

type auditorConfig struct {
	mirror     bool
	maxSize    int64
	maxBackups int
}

// WithStdoutMirror also writes every audit entry to the standard logger, for
// deployments that ship container stdout to a log collector.
func WithStdoutMirror() Option {
	return func(c *auditorConfig) { c.mirror = true }
}

// WithRotation caps the audit log at maxSize bytes, retaining at most maxBackups
// rotated generations (audit.log.1 … audit.log.N).
//
// A maxSize of 0 or less falls back to DefaultMaxSizeBytes. maxBackups is taken
// literally, including 0 — "rotate and keep nothing" is a real choice — so a
// negative value is what selects DefaultMaxBackups.
func WithRotation(maxSize int64, maxBackups int) Option {
	return func(c *auditorConfig) {
		if maxSize > 0 {
			c.maxSize = maxSize
		}
		if maxBackups >= 0 {
			c.maxBackups = maxBackups
		}
	}
}

// reservedFields are set by the auditor itself and identify the event. A
// caller's key-value detail that collides with one of these is prefixed rather
// than allowed to overwrite it.
var reservedFields = map[string]bool{
	"action":     true,
	"resource":   true,
	"status":     true,
	"error":      true,
	"request_id": true,
	"ip":         true,
	"ua":         true,
	"user":       true,
	"token_name": true,
	"time":       true,
	"level":      true,
	"msg":        true,
}

// AuditEntry holds temporary state like the context
type AuditEntry struct {
	auditor *Auditor
	ctx     *gin.Context
}

func NewAuditor(filePath string, opts ...Option) (*Auditor, error) {
	cfg := auditorConfig{
		maxSize:    DefaultMaxSizeBytes,
		maxBackups: DefaultMaxBackups,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Writes go through the rotating writer rather than a bare *os.File so the
	// log cannot grow without bound. The writer reopens the path after each
	// rename, which is what keeps the reader — which resolves the log by path —
	// seeing live entries.
	w, err := newRotatingWriter(filePath, cfg.maxSize, cfg.maxBackups)
	if err != nil {
		return nil, err
	}

	l := logrus.New()
	l.SetOutput(w)
	l.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02T15:04:05Z07:00"})

	return &Auditor{
		log:        l,
		filePath:   filePath,
		mirror:     cfg.mirror,
		writer:     w,
		maxBackups: cfg.maxBackups,
	}, nil
}

// Close releases the audit log file. Safe on a reader-only Auditor.
func (a *Auditor) Close() error {
	if a.writer == nil {
		return nil
	}
	return a.writer.Close()
}

// --- Entry Point Methods ---

// WithContext wraps the auditor with Gin context info
func (a *Auditor) WithContext(c *gin.Context) *AuditEntry {
	return &AuditEntry{auditor: a, ctx: c}
}

// Success called directly (for background tasks)
func (a *Auditor) Success(action, resource string, kv ...any) {
	a.record(nil, action, resource, "SUCCESS", nil, kv...)
}

// Failure called directly (for background tasks)
func (a *Auditor) Failure(action, resource string, err error, kv ...any) {
	a.record(nil, action, resource, "FAILURE", err, kv...)
}

// --- Chained Methods (for AuditEntry) ---

func (e *AuditEntry) Success(action, resource string, kv ...any) {
	e.auditor.record(e.ctx, action, resource, "SUCCESS", nil, kv...)
}

func (e *AuditEntry) Failure(action, resource string, err error, kv ...any) {
	e.auditor.record(e.ctx, action, resource, "FAILURE", err, kv...)
}

// --- The Core Logic ---

func (a *Auditor) record(c *gin.Context, action, resource, status string, err error, kv ...any) {
	fields := logrus.Fields{
		"action":   action,
		"resource": resource,
		"status":   status,
	}

	if c != nil {
		fields["request_id"] = c.GetString("request_id")
		fields["ip"] = c.ClientIP()
		fields["ua"] = c.GetHeader("User-Agent")
		if user, exists := c.Get("username"); exists {
			fields["user"] = user
		}
		if tokenName, exists := c.Get("token_name"); exists {
			fields["token_name"] = tokenName
		}
	} else {
		fields["ip"] = "internal"
		fields["ua"] = "system-worker"
		fields["user"] = "system"
	}

	if err != nil {
		fields["error"] = err.Error()
	}

	// Caller-supplied pairs must not overwrite the fields that identify the
	// event. Passing "action" as a detail key used to silently replace the action
	// itself — every SYSTEM_MAINTENANCE entry was written as action=vacuum, so
	// the category never appeared in the log at all. Such keys are prefixed
	// rather than dropped, so the detail survives without corrupting the record.
	for i := 0; i < len(kv); i += 2 {
		if i+1 < len(kv) {
			key := fmt.Sprintf("%v", kv[i])
			if reservedFields[key] {
				key = "detail_" + key
			}
			fields[key] = kv[i+1]
		}
	}

	a.log.WithFields(fields).Info("audit")
	if a.mirror {
		logrus.WithFields(fields).Info("audit")
	}
}
