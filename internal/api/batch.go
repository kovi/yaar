package api

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/models"
)

func (h *Handler) HandleBatchDownload(c *gin.Context) {
	paths := c.QueryArray("p") // Expects ?p=/file1.txt&p=/folderA
	if len(paths) == 0 {
		c.JSON(400, gin.H{"error": "No files selected"})
		return
	}

	// Set headers for file download
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename=artifactory_export.zip")
	c.Header("Content-Transfer-Encoding", "binary")

	mode := h.Config.Storage.DefaultBatchMode
	if mParam := c.Query("mode"); mParam != "" {
		var err error
		mode, err = models.ParseBatchMode(mParam)
		if err != nil {
			c.JSON(400, gin.H{
				"error": err.Error(),
			})
			return
		}
	}

	// Initialize ZIP writer directly to the HTTP response stream
	zipWriter := zip.NewWriter(c.Writer)
	defer zipWriter.Close()

	for _, relPath := range paths {
		fullPath := filepath.Join(h.BaseDir, filepath.Clean(relPath))

		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}

			// Determine the name inside the ZIP
			var zipEntryName string
			if mode == "merge" && info.Name() != filepath.Base(relPath) {
				// If merging, we take the path relative to the selected root
				rel, _ := filepath.Rel(fullPath, path)
				if info.IsDir() {
					return nil
				}
				zipEntryName = rel
			} else {
				// Literal: keep the name of the selected file/folder
				parent := filepath.Dir(relPath)
				zipEntryName, _ = filepath.Rel(parent, relPath)
				// If it's a sub-file in a folder, append that subpath
				sub, _ := filepath.Rel(fullPath, path)
				zipEntryName = filepath.Join(zipEntryName, sub)
			}

			// Create the entry in ZIP
			f, _ := os.Open(path)
			defer f.Close()

			w, err := zipWriter.Create(zipEntryName)
			if err != nil {
				return err
			}

			_, err = io.Copy(w, f)
			return err
		})
		if err != nil {
			h.Log.Errorf("Error adding %s to zip: %v", relPath, err)
		}
	}
}
