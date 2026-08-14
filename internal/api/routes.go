package api

import (
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/auth"
)

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Match routes against the raw (still-escaped) URL path so that an escaped
	// slash (%2F) inside a single path segment — e.g. a stream/group name
	// containing "/" captured by :name — survives routing instead of being
	// split into multiple segments. UnescapePathValues keeps c.Param() values
	// decoded, so handlers reading *path params are unaffected.
	r.UseRawPath = true
	r.UnescapePathValues = true

	h.cleanupMutex.Lock()
	if h.failedCleanups == nil {
		h.failedCleanups = make(map[string]*CleanupFailure)
	}
	h.cleanupMutex.Unlock()

	api := r.Group("/_/api/v1")
	files := api.Group("/fs")
	{
		files.GET("/*path", h.GetMeta)
		files.HEAD("/*path", h.GetMeta)
		files.PATCH("/*path", auth.Protect(), h.PatchMeta)
		files.POST("/*path", auth.Protect(), h.PostMeta)
	}
	api.GET("/search", h.Search)
	// Authenticated, and tiered inside the handler: the config, SBOM and commit
	// are admin-only. It used to be fully anonymous.
	api.GET("/settings", auth.Protect(), h.GetSettings)
	api.GET("/batch", h.HandleBatchDownload)
	api.DELETE("/batch", auth.Protect(), h.BatchDelete)

	// --- stream routes ---
	// Writes are gated like the `files` group above: PUT/PATCH set
	// AutoExpirePrevious, which drives janitor deletion, so leaving them open
	// would be an unauthenticated path to destroying artifacts. Reads stay
	// anonymous, consistent with the rest of the read surface.
	stream := api.Group("/streams")
	{
		stream.GET("", h.ListStreams)
		stream.PUT("/:name", auth.Protect(), h.PutStream)
		stream.GET("/:name", h.GetStreamDetails)
		stream.PATCH("/:name", auth.Protect(), h.PatchStream)
	}

	// Health is deliberately outside the authenticated surface: a load balancer
	// or uptime monitor polls it without credentials. The handler reports which
	// check failed but no paths or driver errors.
	r.GET("/_/hp", h.HandleHealth)
	r.HEAD("/_/hp", h.HandleHealth)

	system := r.Group("/_/api/system", auth.AdminRequired())
	system.POST("vacuum", h.HandleVacuum)
	system.GET("janitor/errors", h.GetJanitorErrors)
	system.DELETE("janitor/errors/:key", h.ClearJanitorError)
	if h.TriggerSync != nil {
		system.POST("sync", func(c *gin.Context) {
			h.TriggerSync()
			c.JSON(http.StatusOK, gin.H{"status": "sync triggered"})
		})
	}

	adminAPI := api.Group("/admin", auth.AdminRequired())
	adminAPI.GET("/audit-log", h.GetAuditLog)

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
		p := h.fsPath(dbPath)
		isHtmlRequested := getScore(c.GetHeader("Accept"), "text/html") > 0
		stat, err := os.Stat(p)
		h.log(c).Infof("defaultHandler p=%q err=%v", p, err)
		if err != nil {
			// Path doesn't exist.
			// Serve UI so the SPA can show a 404, but use a 404 status code.
			h.serveIndexWithStatus(c, http.StatusNotFound)
			return
		}
		if stat.IsDir() {
			if isHtmlRequested {
				// Serve directory SPA view
				c.File(path.Join(h.Config.Server.WebDir, "/index.html"))
				return
			}
			// A directory is not a downloadable artifact. Falling through to
			// ServeFile would let http.ServeFile answer with a 301 to the
			// trailing-slash URL, which reads as success to tooling that does
			// not follow redirects. Report it as absent instead, matching the
			// "directories are not files" contract above.
			h.serveIndexWithStatus(c, http.StatusNotFound)
			return
		}

		h.ServeFile(c, dbPath)
		return
	}

	c.Status(http.StatusNotFound)
}

// serveIndexWithStatus writes the SPA entrypoint with an explicit status code.
//
// c.File (http.ServeFile) always calls WriteHeader(200) itself, which would
// overwrite a status previously recorded by c.Status — so a "404 but still show
// the UI" response would go out as 200 and mislead non-browser clients such as
// CI tooling. Writing the file contents ourselves keeps the status intact.
func (h *Handler) serveIndexWithStatus(c *gin.Context, status int) {
	index := path.Join(h.Config.Server.WebDir, "index.html")
	body, err := os.ReadFile(index)
	if err != nil {
		h.log(c).Warnf("cannot read SPA index %q: %v", index, err)
		c.Status(status)
		return
	}

	c.Header("Cache-Control", "no-store")
	// For HEAD, net/http drops the body but keeps the headers, so the same call
	// serves both methods.
	c.Data(status, "text/html; charset=utf-8", body)
}

// getScore returns the client's preference for target (e.g. "text/html") from an
// Accept header, as a q-value in [0,1]; 0 means "not acceptable".
//
// Per RFC 9110 media types are case-insensitive and optional whitespace is
// allowed around ";" and "=", so both are normalized before matching. A
// "type/*" range matches target's type, but a bare "*/*" deliberately does NOT:
// it is what curl and most CI clients send by default, and treating it as a
// request for HTML would serve them the SPA instead of the artifact.
func getScore(header, target string) float64 {
	target = strings.ToLower(strings.TrimSpace(target))
	targetType, _, _ := strings.Cut(target, "/")

	best := 0.0
	for _, part := range strings.Split(header, ",") {
		mediaRange, params, hasParams := strings.Cut(part, ";")
		mediaRange = strings.ToLower(strings.TrimSpace(mediaRange))

		// Match the exact type ("text/html") or a subtype wildcard ("text/*").
		// "*/*" is intentionally excluded — see the doc comment.
		if mediaRange != target && mediaRange != targetType+"/*" {
			continue
		}

		score := 1.0 // absent q means 1.0 (max)
		if hasParams {
			for _, param := range strings.Split(params, ";") {
				key, value, ok := strings.Cut(param, "=")
				if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
					continue
				}
				// A malformed q leaves the entry at its default weight rather
				// than silently scoring it 0.
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					score = parsed
				}
				break
			}
		}

		// An exact match outranks a wildcard, so keep the strongest signal.
		if score > best {
			best = score
		}
	}
	return best
}
