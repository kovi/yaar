package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/audit"
)

func (h *Handler) GetAuditLog(c *gin.Context) {
	opts := audit.ReadOptions{BeforeOffset: -1}

	if v := c.Query("before_offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid before_offset"})
			return
		}
		opts.BeforeOffset = n
	}

	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		opts.Limit = n
	}

	// Which rotated file to continue in; 0 is the live log. Echoed back from the
	// previous page's next_generation.
	if v := c.Query("generation"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid generation"})
			return
		}
		opts.Generation = n
	}

	opts.Filter = c.Query("filter")

	page, err := h.Audit.ReadPage(opts)
	if err != nil {
		h.log(c).WithError(err).Error("failed to read audit log")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read audit log"})
		return
	}

	c.JSON(http.StatusOK, page)
}
