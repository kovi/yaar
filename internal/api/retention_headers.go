package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderStream                       = "Yaar-Stream"
	HeaderGroup                        = "Yaar-Group"
	HeaderRetentionImmutable           = "Yaar-Retention-Immutable"
	HeaderRetentionAutoPrune           = "Yaar-Retention-Auto-Prune"
	HeaderRetentionPruneChildren       = "Yaar-Retention-Prune-Children"
	HeaderRetentionExpireAfterUpload   = "Yaar-Retention-Expire-After-Upload"
	HeaderRetentionExpireAfterDownload = "Yaar-Retention-Expire-After-Download"
	HeaderRetentionExpireAt            = "Yaar-Retention-Expire-At"
)

// ErrLegacyHeader rejects a request that uses a header from the pre-1.0 API.
const ErrLegacyHeader ErrorCode = "LEGACY_HEADER"

// legacyUploadHeaders are upload headers the pre-streams API accepted, each with
// what replaces it. Unknown headers are otherwise ignored, so without this an
// old CI script would upload successfully while its stream, expiry and
// keep-latest settings were silently dropped — files that never expire.
var legacyUploadHeaders = []struct{ name, replacement string }{
	{"X-Stream", fmt.Sprintf("send %s and %s as separate headers instead of X-Stream: stream/group", HeaderStream, HeaderGroup)},
	{"X-Expires", fmt.Sprintf("use %s for a relative expiry (e.g. 7d) or %s for an absolute date", HeaderRetentionExpireAfterUpload, HeaderRetentionExpireAt)},
	{"X-KeepLatest", "set auto_expire_previous on the stream instead (PATCH /_/api/v1/streams/<name>)"},
}

// rejectLegacyUploadHeaders returns an error naming the first legacy header
// present on r and its replacement, or nil.
func rejectLegacyUploadHeaders(r *http.Request) *AppError {
	for _, h := range legacyUploadHeaders {
		if _, ok := r.Header[http.CanonicalHeaderKey(h.name)]; ok {
			return &AppError{
				Code:    ErrLegacyHeader,
				Message: fmt.Sprintf("Header %s is no longer supported: %s.", h.name, h.replacement),
				Details: map[string]any{"header": h.name},
			}
		}
	}
	return nil
}

func ExtractRetentionHeaders(r *http.Request) (*RetentionPolicyPatch, error) {
	patch := &RetentionPolicyPatch{}
	hasAnyHeader := false

	// Resource-level: Immutable
	if val := r.Header.Get(HeaderRetentionImmutable); val != "" {
		hasAnyHeader = true
		b, err := parseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderRetentionImmutable, err)
		}
		patch.Immutable = &b
	}

	// Resource-level: Auto-prune
	if val := r.Header.Get(HeaderRetentionAutoPrune); val != "" {
		hasAnyHeader = true
		b, err := parseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderRetentionAutoPrune, err)
		}
		patch.AutoPrune = &b
	}

	// Resource-level: Prune children
	if val := r.Header.Get(HeaderRetentionPruneChildren); val != "" {
		hasAnyHeader = true
		b, err := parseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderRetentionPruneChildren, err)
		}
		patch.PruneChildren = &b
	}

	// Expiry policies
	expires, err := extractExpiresHeaders(r)
	if err != nil {
		return nil, err
	}
	if expires != nil {
		hasAnyHeader = true
		patch.Expires = expires
	}

	if !hasAnyHeader {
		return nil, nil
	}

	return patch, nil
}

// extractExpiresHeaders parses expiry policy headers.
// Returns nil if no expires headers are present.
// IMPORTANT: Any expires header replaces the entire expires block.
func extractExpiresHeaders(r *http.Request) (*ExpiresPatch, error) {
	patch := &ExpiresPatch{}
	hasAnyHeader := false

	// After upload
	if val := r.Header.Get(HeaderRetentionExpireAfterUpload); val != "" {
		hasAnyHeader = true
		if err := validateDuration(val); err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderRetentionExpireAfterUpload, err)
		}
		patch.AfterUpload = &val
	}

	// After download
	if val := r.Header.Get(HeaderRetentionExpireAfterDownload); val != "" {
		hasAnyHeader = true
		if err := validateDuration(val); err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderRetentionExpireAfterDownload, err)
		}
		patch.AfterDownload = &val
	}

	// Absolute timestamp
	if val := r.Header.Get(HeaderRetentionExpireAt); val != "" {
		hasAnyHeader = true
		if _, err := parseTimeString(val); err != nil {
			return nil, fmt.Errorf("%s: %w", HeaderRetentionExpireAt, err)
		}
		patch.At = &val
	}

	// Validate: cannot combine after_upload and after_download
	if patch.AfterUpload != nil && patch.AfterDownload != nil {
		return nil, fmt.Errorf("cannot combine %s and %s",
			HeaderRetentionExpireAfterUpload, HeaderRetentionExpireAfterDownload)
	}

	if !hasAnyHeader {
		return nil, nil
	}

	return patch, nil
}

// parseBool converts string to bool, accepting common formats.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value '%s' (use true/false, 1/0, yes/no, on/off)", s)
	}
}

// validateDuration checks if duration string is valid.
func validateDuration(s string) error {
	// Support "30d" format (days)
	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		if _, err := strconv.Atoi(daysStr); err != nil {
			return fmt.Errorf("invalid day duration '%s' (expected format: '30d')", s)
		}
		return nil
	}

	// Support standard Go durations (2h, 30m, etc.)
	if _, err := time.ParseDuration(s); err != nil {
		return fmt.Errorf("invalid duration '%s' (use '30d', '2h', '30m', etc.)", s)
	}

	return nil
}
