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
	"github.com/sirupsen/logrus"
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
	if mParam := c.Query("mode"); mParam != "" {
		overrideMode, err := models.ParseBatchMode(mParam)
		if err != nil {
			// Fail-fast if the user explicitly provided an invalid override
			c.JSON(400, gin.H{"error": "Invalid mode parameter", "details": err.Error()})
			return
		}
		mode = overrideMode
	}

	// Deduplicate & Clean Paths
	type selectedPath struct {
		logical  string
		diskPath string
	}
	pathMap := make(map[string]bool)
	var paths []selectedPath
	for _, p := range rawPaths {
		cleaned := filepath.Clean("/" + p) // anchor to root first
		if !strings.HasPrefix(cleaned, "/") {
			c.JSON(400, gin.H{"error": "Invalid path"})
			return
		}
		fullDiskPath := filepath.Join(h.BaseDir, cleaned)
		if !strings.HasPrefix(fullDiskPath, h.BaseDir+string(os.PathSeparator)) {
			c.JSON(400, gin.H{"error": "Path escapes base directory"})
			return
		}
		if !pathMap[cleaned] {
			pathMap[cleaned] = true
			paths = append(paths, selectedPath{logical: cleaned, diskPath: fullDiskPath})
		}
	}

	// Determine ZIP Filename
	var zipName string
	if nameParam := c.Query("name"); nameParam != "" {
		zipName = filepath.Base(nameParam) // filepath.Base sanitizes path traversal attempts
	} else if len(paths) == 1 {
		zipName = filepath.Base(paths[0].logical)
	} else {
		logicalPaths := make([]string, len(paths))
		for i, sp := range paths {
			logicalPaths[i] = sp.logical
		}
		zipName = filepath.Base(findCommonParent(logicalPaths))
	}
	if zipName == "." || zipName == "/" || zipName == "" {
		zipName = "artifactory_root"
	}

	// Setup Stream
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, zipName))

	zipWriter := zip.NewWriter(c.Writer)
	defer zipWriter.Close()

	// zipCandidate holds the metadata needed to write one file into the archive.
	type zipCandidate struct {
		diskPath string
		zipName  string
		info     os.FileInfo
	}

	// First pass: collect all candidates without opening file contents.
	// physicalSeen prevents the same disk file appearing twice (e.g. user selected
	// a folder AND a file inside it).
	var candidates []zipCandidate
	physicalSeen := make(map[string]bool)

	for _, sp := range paths {
		err := filepath.WalkDir(sp.diskPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}

			relToSelection, _ := filepath.Rel(sp.diskPath, path)

			var zipEntryName string
			if mode == models.BatchModeMerge {
				if relToSelection == "." {
					// selected path was a single file
					zipEntryName = filepath.Base(path)
				} else {
					zipEntryName = relToSelection
				}
			} else {
				zipEntryName = filepath.Join(filepath.Base(sp.logical), relToSelection)
			}

			zipEntryName = filepath.ToSlash(filepath.Clean("/" + zipEntryName))
			zipEntryName = strings.TrimPrefix(zipEntryName, "/")

			if physicalSeen[path] {
				return nil
			}
			physicalSeen[path] = true

			info, err := d.Info()
			if err != nil {
				return err
			}
			candidates = append(candidates, zipCandidate{diskPath: path, zipName: zipEntryName, info: info})
			return nil
		})

		if err != nil {
			logrus.WithError(err).Errorf("error in walkdir for %s", sp.diskPath)
			zipWriter.SetComment("INCOMPLETE: archive generation failed: " + err.Error())
			return
		}
	}

	// In merge mode, when multiple files resolve to the same zip entry name,
	// keep the last one by request order (later selection wins).
	var mergeWinner map[string]int
	if mode == models.BatchModeMerge {
		mergeWinner = make(map[string]int, len(candidates))
		for i, c := range candidates {
			mergeWinner[c.zipName] = i
		}
	}

	// Second pass: write candidates to the archive.
	for i, cand := range candidates {
		if mode == models.BatchModeMerge && mergeWinner[cand.zipName] != i {
			continue
		}

		f, err := os.Open(cand.diskPath)
		if err != nil {
			logrus.WithError(err).Errorf("failed to open file for zip: %s", cand.diskPath)
			zipWriter.SetComment("INCOMPLETE: archive generation failed: " + err.Error())
			return
		}

		header, err := zip.FileInfoHeader(cand.info)
		if err != nil {
			f.Close()
			zipWriter.SetComment("INCOMPLETE: archive generation failed: " + err.Error())
			return
		}
		header.Name = cand.zipName
		header.Method = zip.Deflate

		w, err := zipWriter.CreateHeader(header)
		if err != nil {
			f.Close()
			zipWriter.SetComment("INCOMPLETE: archive generation failed: " + err.Error())
			return
		}

		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			zipWriter.SetComment("INCOMPLETE: archive generation failed: " + err.Error())
			return
		}
	}
}
