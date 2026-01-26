package api

import (
	"path/filepath"
	"strings"

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
	rtags := h.DB.Debug().
		Joins("Resource").
		Preload("Resource.Tags").
		Where("key LIKE ? OR value LIKE ?", searchPattern, searchPattern).
		Limit(10).Find(&matchedTags)

	if rtags.Error != nil {
		log := logger(c)
		log.WithError(rtags.Error).Info("tag search failed")
		c.JSON(500, map[string]any{"error": "query failed"})
		return
	}

	// Helper to merge and convert to SearchResult
	seenPaths := make(map[string]bool)

	addResult := func(r MetaResource, reason string) {
		if seenPaths[r.Path] {
			return
		}
		seenPaths[r.Path] = true

		res := SearchResult{
			Type:   string(r.Type),
			Name:   filepath.Base(r.Path),
			Path:   r.Path,
			Reason: reason,
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
		// check segmens for matching and stop at first
		segments := strings.Split(strings.Trim(r.Path, "/"), "/")
		cumulativePath := ""
		for _, segment := range segments {
			cumulativePath += "/" + segment

			if strings.Contains(strings.ToLower(segment), strings.ToLower(query)) {
				t := ResourceTypeDir
				m := "path"
				if cumulativePath == r.Path && r.Type == ResourceTypeFile {
					t = r.Type
					m = "entry"
				}

				e := MetaResource{
					Path: cumulativePath,
					Type: t,
				}
				addResult(e, m)
			}
		}
	}

	// Process matches via tags
	for _, t := range matchedTags {
		if t.Resource == nil {
			continue
		}
		addResult(*t.Resource, "tag")
	}

	c.JSON(200, results)
}
