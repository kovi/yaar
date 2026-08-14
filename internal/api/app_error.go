package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type AppError struct {
	Code    ErrorCode
	Message string // overrides errorDefs message if set
	Details map[string]any
}

func (e *AppError) Error() string { return string(e.Code) }

type ErrorCode string

const (
	ErrNotFound                ErrorCode = "NOT_FOUND"
	ErrBadRequest              ErrorCode = "BAD_REQUEST"
	ErrResourceAlreadyInGroup  ErrorCode = "RESOURCE_ALREADY_IN_GROUP"
	ErrStreamGroupBothRequired ErrorCode = "STREAM_GROUP_BOTH_REQUIRED"
	ErrResourceLocked          ErrorCode = "RESOURCE_LOCKED"
)

type errorDef struct {
	status  int
	message string
}

var errorDefs = map[ErrorCode]errorDef{
	ErrBadRequest:              {http.StatusBadRequest, "Bad request"},
	ErrNotFound:                {http.StatusNotFound, "Not found"},
	ErrResourceAlreadyInGroup:  {http.StatusConflict, "Resource already belongs to a group and cannot be moved."},
	ErrStreamGroupBothRequired: {http.StatusBadRequest, "Stream and group must be provided together."},
	ErrResourceLocked:          {http.StatusForbidden, "This resource is locked and cannot be modified."},
}

func (e *AppError) Respond(c *gin.Context) {
	def, ok := errorDefs[e.Code]
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": "unexpected error"})
		return
	}
	msg := def.message
	if e.Message != "" {
		msg = e.Message
	}
	body := gin.H{"error": string(e.Code), "message": msg}
	for k, v := range e.Details {
		body[k] = v
	}
	c.JSON(def.status, body)
}

func respondErr(c *gin.Context, err any, cause ...error) {
	var appErr *AppError
	switch e := err.(type) {
	case ErrorCode:
		appErr = &AppError{Code: e}
	case *AppError:
		appErr = e
	case error:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "INTERNAL_ERROR", "message": e.Error()})
		return
	}

	if len(cause) > 0 && cause[0] != nil {
		if appErr.Message == "" {
			appErr.Message = cause[0].Error()
		}
		// if errorDefs[appErr.Code].status >= 500 {
		// 	log.WithError(cause[0]).WithField("error_code", appErr.Code).Error(appErr.Message)
		// }
	}

	appErr.Respond(c)
}
