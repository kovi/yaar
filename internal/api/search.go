package api

import (
	"path/filepath"

	"github.com/gin-gonic/gin"
)

type SearchResult struct {
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Reason string   `json:"reason"`
	Stream string   `json:"stream,omitempty"`
	Group  string   `json:"group,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

func (h *Handler) Search(c *gin.Context) {
	query := c.Query("q")
	if len(query) < 2 {
		c.JSON(200, []SearchResult{})
		return
	}

	searchPattern := "%" + query + "%"
	var results []SearchResult

	// 1. Query MetaResources with Tags preloaded
	var resources []MetaResource
	h.DB.Preload("Tags").
		Where("path LIKE ? OR stream LIKE ? OR `group` LIKE ?",
			searchPattern, searchPattern, searchPattern).
		Limit(10).Find(&resources)

	// 2. Query Tags directly to find files matching by tag but not by name
	var matchedTags []MetaTag
	h.DB.Preload("Resource.Tags").
		Where("key LIKE ? OR value LIKE ?", searchPattern, searchPattern).
		Limit(10).Find(&matchedTags)

	// Helper to merge and convert to SearchResult
	seenPaths := make(map[string]bool)

	addResult := func(r MetaResource) {
		if seenPaths[r.Path] {
			return
		}
		seenPaths[r.Path] = true

		res := SearchResult{
			Type: string(r.Type),
			Name: filepath.Base(r.Path),
			Path: r.Path,
		}
		if r.Stream != nil {
			res.Stream = *r.Stream
		}
		if r.Group != nil {
			res.Group = *r.Group
		}

		for _, t := range r.Tags {
			tagDisplay := t.Key
			if t.Value != "" {
				tagDisplay += "=" + t.Value
			}
			res.Tags = append(res.Tags, tagDisplay)
		}
		results = append(results, res)
	}

	// Process direct resource matches
	for _, r := range resources {
		addResult(r)
	}

	// Process matches via tags
	for _, t := range matchedTags {
		addResult(*t.Resource)
	}

	c.JSON(200, results)
}
