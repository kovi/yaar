package audit

import (
	"fmt"
	"os"

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
)

type Auditor struct {
	log *logrus.Logger
}

// AuditEntry holds temporary state like the context
type AuditEntry struct {
	auditor *Auditor
	ctx     *gin.Context
}

func NewAuditor(filePath string) (*Auditor, error) {
	l := logrus.New()
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	l.SetOutput(file)
	l.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02T15:04:05Z07:00"})
	return &Auditor{log: l}, nil
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
	} else {
		fields["ip"] = "internal"
		fields["ua"] = "system-worker"
		fields["user"] = "system"
	}

	if err != nil {
		fields["error"] = err.Error()
	}

	for i := 0; i < len(kv); i += 2 {
		if i+1 < len(kv) {
			key := fmt.Sprintf("%v", kv[i])
			fields[key] = kv[i+1]
		}
	}

	a.log.WithFields(fields).Info("audit")
}
