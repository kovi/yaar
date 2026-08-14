package api

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/models"
	"github.com/sirupsen/logrus"
)

var (
	// Primary identifiers
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
	IsDirty   = false

	// Cached build metadata
	cachedBuildInfo *debug.BuildInfo
	buildSettings   map[string]string
)

func InitializeVersionInfo(log *logrus.Entry) error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("failed to read build info: binary was built without debug info")
	}
	cachedBuildInfo = info

	// Convert slice of settings to a map for easy lookup
	buildSettings = make(map[string]string)
	for _, s := range info.Settings {
		buildSettings[s.Key] = s.Value
	}

	// 1. Mandatory Key Checks & Safe Extraction
	// We check for existence explicitly to avoid empty strings in critical logs
	if val, ok := buildSettings["vcs.revision"]; ok {
		Commit = val
		if Version == "dev" {
			Version = val[:7]
		}
	} else {
		log.Warn("Build missing 'vcs.revision' - check if -buildvcs=true was used")
	}

	if val, ok := buildSettings["vcs.time"]; ok {
		BuildDate = val
	} else {
		// Fallback to now if not built via 'go build'
		BuildDate = time.Now().Format(time.RFC3339)
	}

	if val, ok := buildSettings["vcs.modified"]; ok {
		IsDirty = (val == "true")
	}

	// 2. Startup Logging
	log.WithFields(logrus.Fields{
		"version": Version,
		"commit":  Commit,
		"build":   BuildDate,
		"dirty":   IsDirty,
		"go":      info.GoVersion,
		"arch":    buildSettings["GOARCH"],
		"os":      buildSettings["GOOS"],
	}).Info("started")

	return nil
}

// GetSettings serves the system/diagnostics payload.
//
// The response is tiered. Every authenticated caller gets the operational view
// the UI's System tab renders (version, uptime, resource usage). The detailed
// view — the full config with its storage paths and DB filename, the exact
// commit, and the dependency SBOM with versions — is admin-only: a complete
// dependency list with versions is a ready-made vulnerability-matching list, and
// there is no reason for a non-admin to hold one.
func (h *Handler) GetSettings(c *gin.Context) {
	isAdmin, _ := c.Get("is_admin")

	// Get DB Size
	dbSize := 0
	dbStat, err := os.Stat(h.Config.Database.File)
	if err == nil {
		dbSize = int(dbStat.Size())
	}

	// Get Storage Disk Usage
	var storageTotal, storageFree uint64
	var stat syscall.Statfs_t
	if err := syscall.Statfs(h.Config.Storage.BaseDir, &stat); err == nil {
		storageTotal = stat.Blocks * uint64(stat.Bsize)
		storageFree = stat.Bavail * uint64(stat.Bsize)
	}

	// Memory Info
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	goVersion := ""
	if cachedBuildInfo != nil {
		goVersion = cachedBuildInfo.GoVersion
	}

	resp := gin.H{
		"version":        Version,
		"build_date":     BuildDate,
		"go_version":     goVersion,
		"uptime_seconds": time.Since(h.StartTime).Seconds(),
		"is_dirty":       IsDirty,
		"runtime": gin.H{
			"goroutines": runtime.NumGoroutine(),
			"mem_alloc":  m.Alloc, // Current bytes allocated
			"sys_total":  m.Sys,   // Total bytes obtained from System
			"os":         buildSettings["GOOS"],
			"arch":       buildSettings["GOARCH"],
			"cgo":        buildSettings["CGO_ENABLED"] == "1",
			"compiler":   buildSettings["-compiler"],
		},
		"db_size": dbSize,
		"storage": gin.H{
			"total": storageTotal,
			"free":  storageFree,
			"used":  storageTotal - storageFree,
		},
	}

	if isAdmin == true {
		// Prepare the dependency list (Software Bill of Materials)
		dependencies := make(map[string]string)
		if cachedBuildInfo != nil {
			for _, dep := range cachedBuildInfo.Deps {
				dependencies[dep.Path] = dep.Version
			}
		}

		// Get all System States
		var states []models.SystemState
		h.DB.Find(&states)

		resp["commit"] = Commit
		resp["dependencies"] = dependencies
		resp["config"] = h.Config
		resp["system_states"] = states
	}

	c.JSON(200, resp)
}
