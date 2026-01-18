package api

import (
	"os"

	"github.com/gin-gonic/gin"
)

// ListStreams returns a unique list of all stream names currently in the DB
func (h *Handler) ListStreams(c *gin.Context) {
	var streams []string
	// Query unique non-null stream names from meta_resources
	err := h.DB.Model(&MetaResource{}).
		Where("stream IS NOT NULL AND stream != ''").
		Distinct().
		Pluck("stream", &streams).Error

	if err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}
	c.JSON(200, streams)
}

type GroupInfo struct {
	Name  string         `json:"name"`
	Files []FileResponse `json:"files"`
}

// GetStreamDetails returns all groups and their files for a specific stream
func (h *Handler) GetStreamDetails(c *gin.Context) {
	streamName := c.Param("name")
	var resources []MetaResource

	// Fetch all resources belonging to this stream, ordered by group and name
	err := h.DB.Preload("Tags").
		Where("stream = ?", streamName).
		Order("`group` DESC, path ASC").
		Find(&resources).Error

	if err != nil {
		c.JSON(500, gin.H{"error": "Database error"})
		return
	}

	// Group the flat list into a hierarchy in Go logic
	groupsMap := make(map[string][]FileResponse)
	var groupOrder []string

	for _, res := range resources {
		groupName := ""
		if res.Group != nil {
			groupName = *res.Group
		}
		if _, exists := groupsMap[groupName]; !exists {
			groupOrder = append(groupOrder, groupName)
		}

		r := h.toResponseFromMeta(c, res)
		i, err := os.Stat(h.fsPath(res.Path))
		if err == nil {
			r.updateWithFileInfo(i)
		}
		groupsMap[groupName] = append(groupsMap[groupName], r)
	}

	var result []GroupInfo
	for _, name := range groupOrder {
		result = append(result, GroupInfo{
			Name:  name,
			Files: groupsMap[name],
		})
	}

	c.JSON(200, result)
}
