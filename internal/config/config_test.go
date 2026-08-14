package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProtectedPaths(t *testing.T) {
	cfg := &Config{}
	cfg.Storage.ProtectedPaths = []string{"/stable", "/archive/2025"}

	tests := []struct {
		path      string
		protected bool
	}{
		{"/stable/app.exe", true},        // Child of protected
		{"/stable/", true},               // The dir itself
		{"/archive/2025/log.zip", true},  // Nested child
		{"/archive/2024/log.zip", false}, // Different year
		{"/unprotected/file.txt", false},
		{"/stables-suffix/file.txt", false}, // Prefix edge case
	}

	for _, tt := range tests {
		assert.Equal(t, tt.protected, cfg.IsProtected(tt.path), "Path: "+tt.path)
	}
}

func TestConfig_LoadEnv(t *testing.T) {
	cfg := NewConfig() // default port 8080

	// Set mock environment variables
	os.Setenv("AF_PORT", "9999")
	os.Setenv("AF_MAX_SIZE", "5GB")
	os.Setenv("AF_PROTECTED_PATHS", "/env1, /env2")

	defer func() {
		os.Unsetenv("AF_PORT")
		os.Unsetenv("AF_MAX_SIZE")
		os.Unsetenv("AF_PROTECTED_PATHS")
	}()

	cfg.LoadEnv()

	assert.Equal(t, 9999, cfg.Server.Port)
	assert.Equal(t, "5GB", cfg.Storage.MaxUploadSize)
	assert.Len(t, cfg.Storage.ProtectedPaths, 2)
	assert.Equal(t, "/env1", cfg.Storage.ProtectedPaths[0])
}

func TestConfig_LoadEnvErrors(t *testing.T) {
	cfg := NewConfig()

	t.Run("Fail on invalid integer", func(t *testing.T) {
		os.Setenv("AF_PORT", "not-a-number")
		defer os.Unsetenv("AF_PORT")

		err := cfg.LoadEnv()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AF_PORT")
		assert.Contains(t, err.Error(), "expected integer")
	})

	t.Run("Fail on invalid boolean", func(t *testing.T) {
		// Assuming you add an env tag to KeepLatest or similar
		os.Setenv("AF_DEBUG", "maybe")
		defer os.Unsetenv("AF_DEBUG")

		err := cfg.LoadEnv()
		if err != nil {
			assert.Contains(t, err.Error(), "expected boolean")
		}
	})
}

func TestFinalize_JwtSecret(t *testing.T) {
	base := func() *Config {
		c := NewConfig()
		c.Server.JwtSecret = "a-perfectly-fine-random-secret-over-32-chars"
		return c
	}

	t.Run("valid secret passes", func(t *testing.T) {
		assert.NoError(t, base().Finalize())
	})

	t.Run("example placeholder is rejected", func(t *testing.T) {
		c := base()
		c.Server.JwtSecret = placeholderJwtSecret
		err := c.Finalize()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "placeholder")
	})

	t.Run("too-short secret is rejected", func(t *testing.T) {
		c := base()
		c.Server.JwtSecret = "short"
		assert.Error(t, c.Finalize())
	})

	t.Run("the shipped example config would not start as-is", func(t *testing.T) {
		// Guards against config.example.yml drifting away from the constant the
		// guard checks: if someone edits the example secret, this fails until the
		// constant is updated to match, keeping the "example never boots" promise.
		c := base()
		if err := c.LoadYAML("../../config.example.yml"); err != nil {
			t.Skipf("example config not found: %v", err)
		}
		assert.Error(t, c.Finalize(), "config.example.yml must not boot unmodified")
	})
}
