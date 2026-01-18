package e2e

import (
	"testing"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestCanModify_Logic(t *testing.T) {
	WithConfig(t, func(c *config.Config) { c.Storage.ProtectedPaths = []string{"/stable"} })

	// Pre-seed an immutable folder
	isTrue := true
	db.Create(&api.MetaResource{Path: "/locked-folder", Immutable: &isTrue})

	t.Run("Block delete in protected path", func(t *testing.T) {
		ok, msg := Meta.CanModify("/stable/app.exe", []string{"/"}, api.ModifyOptions{})
		assert.False(t, ok)
		assert.Contains(t, msg, "protected")
	})

	t.Run("Allow new upload in protected path", func(t *testing.T) {
		// IgnoreProtected is true because file doesn't exist yet
		ok, _ := Meta.CanModify("/stable/new-file.exe", []string{"/"}, api.ModifyOptions{IgnoreProtected: true})
		assert.True(t, ok)
	})

	t.Run("Block changes inside immutable folder", func(t *testing.T) {
		// Even if IgnoreProtected is true, Immutable is a HARD lock.
		ok, msg := Meta.CanModify("/locked-folder/any.txt", []string{"/"}, api.ModifyOptions{IgnoreProtected: true})
		assert.False(t, ok)
		assert.Contains(t, msg, "immutable")
	})

	t.Run("Enforce multiple allowed scopes", func(t *testing.T) {
		allowed := []string{"/projects/A", "/public"}

		ok, _ := Meta.CanModify("/projects/A/file.txt", allowed, api.ModifyOptions{})
		assert.True(t, ok)

		ok, msg := Meta.CanModify("/projects/B/file.txt", allowed, api.ModifyOptions{})
		assert.False(t, ok)
		assert.Contains(t, msg, "authorized scope")
	})
}
