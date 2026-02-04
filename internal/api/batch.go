package api

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/models"
)

// internal/api/utils.go

func findCommonParent(paths []string) string {
	if len(paths) == 0 {
		return "/"
	}
	// If only one item, its parent is the common container
	if len(paths) == 1 {
		return filepath.Dir(filepath.Clean(paths[0]))
	}

	// Helper to split and clean
	split := func(p string) []string {
		return strings.Split(strings.Trim(filepath.ToSlash(filepath.Clean(p)), "/"), "/")
	}

	// Start with the parent of the first path
	common := split(filepath.Dir(paths[0]))

	for i := 1; i < len(paths); i++ {
		current := split(filepath.Dir(paths[i]))

		// Find where they stop matching
		j := 0
		for j < len(common) && j < len(current) && common[j] == current[j] {
			j++
		}
		common = common[:j]
	}

	res := "/" + strings.Join(common, "/")
	return filepath.Clean(res)
}

func (h *Handler) HandleBatchDownload(c *gin.Context) {
	rawPaths := c.QueryArray("p")
	if len(rawPaths) == 0 {
		c.JSON(400, gin.H{"error": "No files selected"})
		return
	}

	mode := models.BatchModeLiteral
	modeSet := false
	if mParam := c.Query("mode"); mParam != "" {
		overrideMode, err := models.ParseBatchMode(mParam)
		if err != nil {
			// Fail-fast if the user explicitly provided an invalid override
			c.JSON(400, gin.H{"error": "Invalid mode parameter", "details": err.Error()})
			return
		}
		mode = overrideMode
		modeSet = true
	}

	// 1. Deduplicate & Clean Paths
	pathMap := make(map[string]bool)
	var paths []string
	for _, p := range rawPaths {
		cleaned := filepath.Clean(p)
		if !pathMap[cleaned] {
			pathMap[cleaned] = true
			paths = append(paths, cleaned)
		}
	}

	// 2. Determine Common Parent and ZIP Filename
	commonParent := findCommonParent(paths)
	zipName := filepath.Base(commonParent)
	if zipName == "." || zipName == "/" {
		zipName = "artifactory_root"
	}

	// 3. Fetch Mode from Common Parent Metadata
	if !modeSet {
		var parentMeta MetaResource
		if err := h.DB.Where("path = ?", commonParent).Limit(1).Find(&parentMeta).Error; err == nil {
			if parentMeta.DownloadMode.IsValid() {
				mode = parentMeta.DownloadMode
			}
		}
	}

	// 4. Setup Stream
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s.zip", zipName))

	zipWriter := zip.NewWriter(c.Writer)
	defer zipWriter.Close()

	// Track entries already added to ZIP (e.g. if user selected a folder AND a file inside it)
	zipEntries := make(map[string]bool)

	for _, selectedPath := range paths {
		fullDiskPath := filepath.Join(h.BaseDir, selectedPath)

		filepath.WalkDir(fullDiskPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}

			// Get path relative to the selection for internal naming
			relToSelection, _ := filepath.Rel(fullDiskPath, path)

			var zipEntryName string
			if mode == models.BatchModeMerge {
				if relToSelection == "." {
					// If the selected path was a file, relToSelection is ".".
					// We want the actual filename in the ZIP root.
					zipEntryName = filepath.Base(path)
				} else {
					// If it was a folder, relToSelection is "sub/file.txt".
					// We keep that structure but flattened relative to the folder.
					zipEntryName = relToSelection
				}
			} else {
				// Literal: Keep the name of the selection + relative path
				// filepath.Join handles the "." automatically (it ignores it)
				zipEntryName = filepath.Join(filepath.Base(selectedPath), relToSelection)
			}

			// DEDUPLICATION: Don't add the same physical file twice to the ZIP
			if zipEntries[path] {
				return nil
			}
			zipEntries[path] = true

			// Write to ZIP
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer f.Close()

			w, err := zipWriter.Create(filepath.ToSlash(zipEntryName))
			if err != nil {
				return err
			}

			io.Copy(w, f)
			return nil
		})
	}
}
