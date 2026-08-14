package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
)

// streamAuditFields flattens a stream's retention settings for the audit trail.
// The values are what the janitor later acts on, so recording them makes a mass
// expiry traceable back to the policy edit that caused it.
func streamAuditFields(s Stream, op string) []any {
	kv := []any{"op", op}
	if s.RetainLatest != nil {
		kv = append(kv, "retain_latest", *s.RetainLatest)
	}
	if s.RetainLatestMaxExpiry != nil {
		kv = append(kv, "retain_latest_max_expiry", *s.RetainLatestMaxExpiry)
	}
	if s.AutoExpirePrevious != nil {
		kv = append(kv, "auto_expire_previous", *s.AutoExpirePrevious)
	}
	return kv
}

type GroupInfo struct {
	Name  string             `json:"name"`
	Files []ResourceResponse `json:"files"`
}
type StreamRequest struct {
	RetainLatest          *bool   `json:"retain_latest"`
	RetainLatestMaxExpiry *string `json:"retain_latest_max_expiry"`
	AutoExpirePrevious    *bool   `json:"auto_expire_previous"`
}

type StreamResponse struct {
	ID                    uint      `json:"id"`
	Name                  string    `json:"name"`
	RetainLatest          *bool     `json:"retain_latest"`
	RetainLatestMaxExpiry *string   `json:"retain_latest_max_expiry"`
	AutoExpirePrevious    *bool     `json:"auto_expire_previous"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type StreamDetailsResponse struct {
	ID                    uint        `json:"id"`
	Name                  string      `json:"name"`
	RetainLatest          *bool       `json:"retain_latest"`
	RetainLatestMaxExpiry *string     `json:"retain_latest_max_expiry"`
	AutoExpirePrevious    *bool       `json:"auto_expire_previous"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
	Groups                []GroupInfo `json:"groups"`
}

func streamToResponse(s Stream) StreamResponse {
	return StreamResponse{
		ID:                    s.ID,
		Name:                  s.Name,
		RetainLatest:          s.RetainLatest,
		RetainLatestMaxExpiry: s.RetainLatestMaxExpiry,
		AutoExpirePrevious:    s.AutoExpirePrevious,
		CreatedAt:             s.CreatedAt,
		UpdatedAt:             s.UpdatedAt,
	}
}
func (h *Handler) PutStream(c *gin.Context) {
	name := c.Param("name")
	var req StreamRequest
	if err := BindJSONStrict(c, &req); err != nil {
		respondErr(c, ErrBadRequest, err)
		return
	}
	if err := validateStreamRequest(&req); err != nil {
		respondErr(c, ErrBadRequest, err)
		return
	}

	var stream Stream
	h.DB.Where("name = ?", name).Limit(1).Find(&stream)
	op := "update"
	if stream.ID == 0 {
		stream = Stream{Name: name}
		op = "create"
	}

	stream.RetainLatest = req.RetainLatest
	stream.RetainLatestMaxExpiry = req.RetainLatestMaxExpiry
	stream.AutoExpirePrevious = req.AutoExpirePrevious

	if err := h.DB.Save(&stream).Error; err != nil {
		h.Audit.WithContext(c).Failure(audit.ActionStreamUpdate, name, err, "op", op)
		respondErr(c, err)
		return
	}

	h.Audit.WithContext(c).Success(audit.ActionStreamUpdate, name, streamAuditFields(stream, op)...)

	c.JSON(http.StatusOK, streamToResponse(stream))
}

func (h *Handler) PatchStream(c *gin.Context) {
	name := c.Param("name")
	var req StreamRequest
	if err := BindJSONStrict(c, &req); err != nil {
		respondErr(c, ErrBadRequest, err)
		return
	}
	if err := validateStreamRequest(&req); err != nil {
		respondErr(c, ErrBadRequest, err)
		return
	}

	var stream Stream
	res := h.DB.Where("name = ?", name).Limit(1).Find(&stream)
	if res.Error != nil {
		respondErr(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		respondErr(c, ErrNotFound)
		return
	}

	if req.RetainLatest != nil {
		stream.RetainLatest = req.RetainLatest
	}
	if req.RetainLatestMaxExpiry != nil {
		stream.RetainLatestMaxExpiry = req.RetainLatestMaxExpiry
	}
	if req.AutoExpirePrevious != nil {
		stream.AutoExpirePrevious = req.AutoExpirePrevious
	}

	if err := h.DB.Save(&stream).Error; err != nil {
		h.Audit.WithContext(c).Failure(audit.ActionStreamUpdate, name, err, "op", "patch")
		respondErr(c, err)
		return
	}

	h.Audit.WithContext(c).Success(audit.ActionStreamUpdate, name, streamAuditFields(stream, "patch")...)

	c.JSON(http.StatusOK, streamToResponse(stream))
}

func validateStreamRequest(req *StreamRequest) error {
	if req.RetainLatestMaxExpiry != nil {
		if _, err := parseDuration(*req.RetainLatestMaxExpiry); err != nil {
			return fmt.Errorf("invalid retain_latest_max_expiry: %w", err)
		}
	}
	return nil
}

func (h *Handler) ListStreams(c *gin.Context) {
	var streams []string
	if err := h.DB.Model(&Stream{}).Pluck("name", &streams).Error; err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, streams)
}

func (h *Handler) GetStreamDetails(c *gin.Context) {
	scopes := c.GetStringSlice("allowed_paths")
	streamName := c.Param("name")

	var stream Stream
	res := h.DB.Where("name = ?", streamName).Limit(1).Find(&stream)
	if res.Error != nil {
		respondErr(c, res.Error)
		return
	}
	if res.RowsAffected != 1 {
		respondErr(c, ErrNotFound)
		return
	}

	var groups []Group
	if err := h.DB.Where("stream_id = ?", stream.ID).
		Order("created_at DESC").
		Preload("Members.Tags").
		Find(&groups).Error; err != nil {
		respondErr(c, err)
		return
	}

	var result []GroupInfo
	for _, group := range groups {
		var files []ResourceResponse
		for _, res := range group.Members {
			files = append(files, res.ToResourceResponse(scopes, h.Config))
		}
		result = append(result, GroupInfo{
			Name:  group.Name,
			Files: files,
		})
	}

	c.JSON(200, StreamDetailsResponse{
		ID:                    stream.ID,
		Name:                  stream.Name,
		RetainLatest:          stream.RetainLatest,
		RetainLatestMaxExpiry: stream.RetainLatestMaxExpiry,
		AutoExpirePrevious:    stream.AutoExpirePrevious,
		CreatedAt:             stream.CreatedAt,
		UpdatedAt:             stream.UpdatedAt,
		Groups:                result,
	})
}
