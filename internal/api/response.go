package api

import (
	"os"
	"path/filepath"
	"time"

	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/config"
)

func ToResponse(path string, i os.FileInfo, allowedPaths []string, config *config.Config) ResourceResponse {
	m := MetaResource{
		Path:    path,
		Type:    toResourceType(i),
		Size:    i.Size(),
		ModTime: i.ModTime(),
	}
	return m.ToResourceResponse(allowedPaths, config)
}
func (r *MetaResource) ToResourceResponse(allowedPaths []string, config *config.Config) ResourceResponse {
	resp := ResourceResponse{
		// Previously never assigned, so every resource serialized "id": 0.
		// Entries built by ToResponse for a file with no DB row legitimately
		// carry 0, which now distinguishes "not tracked" from a real record.
		ID:          r.ID,
		Path:        r.Path,
		Name:        filepath.Base(r.Path),
		Type:        r.Type,
		Size:        r.Size,
		ModTime:     r.ModTime,
		ContentType: r.ContentType,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		Checksums: ChecksumResponse{
			MD5:    r.MD5,
			SHA1:   r.SHA1,
			SHA256: r.SHA256,
		},
		Policy:    computePolicy(r, allowedPaths, config),
		Retention: toRetentionResponse(r),
		Tags:      buildTagResponses(r.Tags),
	}

	if r.Group != nil {
		resp.Stream = &r.Group.Stream.Name
		resp.Group = &r.Group.Name
	}

	return resp
}

func computePolicy(r *MetaResource, allowedPaths []string, config *config.Config) ResourcePolicy {
	isImmutable := r.Immutable != nil && *r.Immutable
	isProtected := config.IsProtected(r.Path)

	return ResourcePolicy{
		IsImmutable: isImmutable,
		IsProtected: isProtected,
		IsWritable:  !isImmutable && !isProtected && auth.IsInScopes(r.Path, allowedPaths),
		IsAllowed:   auth.IsInScopes(r.Path, allowedPaths),
	}
}

// toRetentionResponse converts DB model to retention response
func toRetentionResponse(r *MetaResource) *RetentionResponse {
	ret := &RetentionResponse{}
	hasAny := false

	// Immutable
	if r.Immutable != nil && *r.Immutable {
		ret.Immutable = true
		hasAny = true
	}

	// Auto-prune
	if r.AutoPrune != nil && *r.AutoPrune {
		ret.AutoPrune = true
		hasAny = true
	}

	// Prune children
	if r.PruneChildren != nil && *r.PruneChildren {
		ret.PruneChildren = true
		hasAny = true
	}

	// Expires
	var effectiveDeadline *time.Time
	if r.Group != nil && r.Group.EffectiveDeleteAfter != nil {
		effectiveDeadline = r.Group.EffectiveDeleteAfter
	} else if r.DeleteAfter != nil {
		effectiveDeadline = r.DeleteAfter
	}

	if r.ExpiryAfterUpload != nil ||
		r.ExpiryAfterDownload != nil ||
		r.ExpiryAt != nil ||
		effectiveDeadline != nil {
		ret.Expires = &ExpiresResponse{}

		if r.ExpiryAfterUpload != nil {
			ret.Expires.AfterUpload = *r.ExpiryAfterUpload
		}

		if r.ExpiryAfterDownload != nil {
			ret.Expires.AfterDownload = *r.ExpiryAfterDownload
		}

		if r.ExpiryAt != nil {
			ts := r.ExpiryAt.Format(time.RFC3339)
			ret.Expires.At = &ts
		}

		if effectiveDeadline != nil {
			ts := effectiveDeadline.Format(time.RFC3339)
			ret.Expires.Effective = &ts
		}

		hasAny = true
	}

	if !hasAny {
		return nil
	}

	return ret
}

func buildTagResponses(tags []MetaTag) []TagResponse {
	if len(tags) == 0 {
		return nil
	}
	out := make([]TagResponse, len(tags))
	for i, t := range tags {
		out[i] = TagResponse{ID: t.ID, Key: t.Key, Value: t.Value}
	}
	return out
}
