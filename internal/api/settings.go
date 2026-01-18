package api

import (
	"fmt"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
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
	}).Info("Artifactory system initialized")

	return nil
}

func (h *Handler) GetSettings(c *gin.Context) {
	// Prepare the dependency list (Software Bill of Materials)
	dependencies := make(map[string]string)
	if cachedBuildInfo != nil {
		for _, dep := range cachedBuildInfo.Deps {
			dependencies[dep.Path] = dep.Version
		}
	}

	c.JSON(200, gin.H{
		"version":    Version,
		"commit":     Commit,
		"build_date": BuildDate,
		"go_version": cachedBuildInfo.GoVersion,
		"is_dirty":   IsDirty,
		// Infrastructure info
		"runtime": gin.H{
			"os":       buildSettings["GOOS"],
			"arch":     buildSettings["GOARCH"],
			"cgo":      buildSettings["CGO_ENABLED"] == "1",
			"compiler": buildSettings["-compiler"],
		},
		// The full dependency tree
		"dependencies": dependencies,
		// Your application YAML configuration
		"config": h.Config,
	})
}
