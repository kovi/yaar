package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kovi/yaar/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestJanitor_NonEmptyDirectory(t *testing.T) {

	t.Run("Do not remove expired directory if it contains an active file", func(t *testing.T) {
		// Create a directory: /old-folder
		// Create a file inside: /old-folder/important.txt
		dirName := "old-folder"
		fileName := "important.txt"
		diskDirPath := filepath.Join(baseDir, dirName)
		diskFilePath := filepath.Join(diskDirPath, fileName)

		err := os.MkdirAll(diskDirPath, 0755)
		assert.NoError(t, err)
		err = os.WriteFile(diskFilePath, []byte("don't delete me"), 0644)
		assert.NoError(t, err)

		// Mark the DIRECTORY as expired, but NOT the file.
		past := time.Now().Add(-1 * time.Hour).UTC()
		db.Create(&api.MetaResource{
			Path:      "/" + dirName,
			Type:      api.ResourceTypeDir,
			ExpiresAt: &past,
		})

		// 4. Run Janitor Cleanup
		Meta.RunCleanup()

		// 5. Assertions
		// The directory should still exist on disk because it's not empty
		assert.DirExists(t, diskDirPath, "Directory should not be physically removed if not empty")
		assert.FileExists(t, diskFilePath, "File inside should still exist")

		// The metadata record for the directory should also still exist
		// because the physical removal failed/was skipped.
		var meta api.MetaResource
		res := db.Where("path = ?", "/"+dirName).First(&meta)
		assert.NoError(t, res.Error, "Database record should remain until physical dir is empty and removed")
	})

	t.Run("Remove expired directory once it is empty", func(t *testing.T) {
		dirName := "empty-folder"
		diskDirPath := filepath.Join(baseDir, dirName)
		os.MkdirAll(diskDirPath, 0755)

		past := time.Now().Add(-1 * time.Hour).UTC()
		db.Create(&api.MetaResource{
			Path:      "/" + dirName,
			Type:      api.ResourceTypeDir,
			ExpiresAt: &past,
		})

		Meta.RunCleanup()

		// Should be gone now
		assert.NoDirExists(t, diskDirPath, "Empty expired directory should be removed")

		var count int64
		db.Model(&api.MetaResource{}).Where("path = ?", "/"+dirName).Count(&count)
		assert.Equal(t, int64(0), count, "Database record should be deleted")
	})
}
