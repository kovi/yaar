package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/auth"
)

func (h *Handler) RegisterRoutes(r *gin.Engine) {

	api := r.Group("/_/api/v1")
	files := api.Group("/fs")
	{
		files.GET("/*path", h.GetMeta)
		files.PATCH("/*path", auth.Protect(), h.PatchMeta)
		files.POST("/*path", auth.Protect(), h.PostMeta)
	}
	api.GET("/search", h.Search)
	api.GET("/settings", h.GetSettings)

	// --- stream routes ---
	stream := api.Group("/streams")
	{
		stream.GET("", h.ListStreams)
		stream.GET("/:name", h.GetStreamDetails)
	}

	r.NoRoute(h.defaultHandler)
}

/*
defaultHandler is the NoRoute handler to handle files requests
Concept:
- The User URL (/*path): Only serves Files. Directories return 404 (unless it's a browser).
- The API URL (/_/api/fs/*path): Serves Metadata. Works for both files and directories.
*/
func (h *Handler) defaultHandler(c *gin.Context) {
	// not handled api paths are 404
	if strings.HasPrefix(c.Request.URL.Path, "/_/api") {
		c.Status(http.StatusNotFound)
		return
	}

	switch c.Request.Method {
	case http.MethodDelete:
		if auth.EnsureAuth(c) {
			h.DeleteEntry(c)
		}
		return
	case http.MethodPost:
		fallthrough
	case http.MethodPut:
		if auth.EnsureAuth(c) {
			h.HandleUpload(c)
		}
		return
	case http.MethodGet:
		fallthrough
	case http.MethodHead:
		dbPath := dbPath(c.Request.URL.Path)
		path := h.fsPath(dbPath)
		isHtmlRequested := getScore(c.GetHeader("Accept"), "text/html") > 0
		stat, err := os.Stat(path)
		if err != nil || (stat.IsDir() && isHtmlRequested) {
			// Path doesn't exist?
			// Serve UI so the SPA can show a 404 or the directory listing
			c.File("./web/index.html")
			return
		}

		h.ServeFile(c, dbPath)
		return
	}

	c.Status(http.StatusNotFound)
}

func getScore(header, target string) float64 {
	for _, part := range strings.Split(header, ",") {
		pair := strings.Split(strings.TrimSpace(part), ";")
		if pair[0] == target {
			if len(pair) > 1 && strings.HasPrefix(pair[1], "q=") {
				score, _ := strconv.ParseFloat(pair[1][2:], 64)
				return score
			}
			return 1.0 // No q means 1.0 (max)
		}
	}
	return 0.0
}
