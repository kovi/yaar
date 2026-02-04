package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/kovi/yaar/internal/models"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port      int    `yaml:"port" env:"AF_PORT"`
		JwtSecret string `yaml:"jwt_secret" env:"JWT_SECRET" json:"-"`
		WebDir    string `yaml:"web_dir" env:"AF_WEB_DIR" `
	} `yaml:"server"`

	Database struct {
		File string `yaml:"file" env:"AF_DB_FILE"`
	} `yaml:"database"`

	Storage struct {
		BaseDir            string                   `yaml:"base_dir" env:"AF_BASE_DIR"`
		MaxUploadSize      string                   `yaml:"max_upload_size" env:"AF_MAX_SIZE"`
		MaxUploadSizeBytes int64                    `yaml:"-"`
		ProtectedPaths     []string                 `yaml:"protected_paths" env:"AF_PROTECTED_PATHS"`
		DefaultBatchMode   models.BatchDownloadMode `yaml:"default_batch_mode" env:"AF_DEFAULT_BATCH_MODE"`
	} `yaml:"storage"`

	Audit struct {
		File string `yaml:"file" env:"AF_AUDIT_LOG"`
	} `yaml:"audit"`
}

// NewConfig sets the hardcoded "Factory Defaults"
func NewConfig() *Config {
	cfg := &Config{}

	cfg.Server.Port = 8080
	cfg.Database.File = "artifactory.db"
	cfg.Storage.BaseDir = "storage"
	cfg.Storage.MaxUploadSize = "100MB"
	cfg.Audit.File = "audit.log"
	cfg.Server.WebDir = "web"

	return cfg
}

func (c *Config) LoadYAML(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Unmarshal will only overwrite fields present in the YAML
	return yaml.Unmarshal(data, c)
}

func (c *Config) Finalize() error {
	if len(c.Server.JwtSecret) < 32 {
		return errors.New("Server.JwtSecret should be at least 32 characters")
	}

	bytes, err := ParseBytes(c.Storage.MaxUploadSize)
	if err != nil {
		return err
	}
	c.Storage.MaxUploadSizeBytes = bytes

	// Validate Batch Mode (if set in YAML/Env)
	// If it's empty, we apply the default. If it's set, it MUST be valid.
	if c.Storage.DefaultBatchMode == "" {
		c.Storage.DefaultBatchMode = models.BatchModeLiteral
	} else {
		if !c.Storage.DefaultBatchMode.IsValid() {
			return fmt.Errorf("storage.default_batch_mode: %w",
				fmt.Errorf("invalid value %q", c.Storage.DefaultBatchMode))
		}
	}

	// Normalize paths to ensure they start with / and don't end with /
	for i, p := range c.Storage.ProtectedPaths {
		cleaned := "/" + strings.Trim(filepath.ToSlash(p), "/")
		c.Storage.ProtectedPaths[i] = cleaned
	}
	return nil
}

// IsProtected checks if the given URL path is within a protected directory
func (c *Config) IsProtected(urlPath string) bool {
	cleanPath := "/" + strings.Trim(filepath.ToSlash(urlPath), "/")
	for _, p := range c.Storage.ProtectedPaths {
		// Check if path is exactly the protected dir or a child of it
		if cleanPath == p || strings.HasPrefix(cleanPath, p+"/") {
			return true
		}
	}
	return false
}

// ParseBytes converts strings like "10MB", "1GB" to int64 bytes
func ParseBytes(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	re := regexp.MustCompile(`^(\d+)\s*([KMGT]B|[B])$`)
	matches := re.FindStringSubmatch(s)
	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid size format: %s", s)
	}

	value, _ := strconv.ParseInt(matches[1], 10, 64)
	unit := matches[2]

	switch unit {
	case "B":
		return value, nil
	case "KB":
		return value * 1024, nil
	case "MB":
		return value * 1024 * 1024, nil
	case "GB":
		return value * 1024 * 1024 * 1024, nil
	case "TB":
		return value * 1024 * 1024 * 1024 * 1024, nil
	default:
		return value, nil
	}
}

// LoadEnv attempts to fill the struct from environment variables.
// It returns an error if a value exists but cannot be converted to the target type.
func (c *Config) LoadEnv() error {
	return loadRecursive(reflect.ValueOf(c).Elem())
}

func loadRecursive(v reflect.Value) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		fieldV := v.Field(i)
		fieldT := t.Field(i)

		// 1. Recurse into nested structs
		if fieldV.Kind() == reflect.Struct {
			if err := loadRecursive(fieldV); err != nil {
				return err
			}
			continue
		}

		// 2. Check for the "env" tag
		tag := fieldT.Tag.Get("env")
		if tag == "" {
			continue
		}

		// 3. If env var exists and is not empty, set it
		if val := os.Getenv(tag); val != "" {
			if err := setField(fieldV, tag, val); err != nil {
				return err
			}
		}
	}
	return nil
}

func setField(field reflect.Value, tagName, val string) error {
	// Ensure we can actually set the field
	if !field.CanSet() {
		return fmt.Errorf("field for %s is not settable (check exported fields)", tagName)
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(val)

	case reflect.Int:
		i, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("environment variable %s: expected integer, got %q", tagName, val)
		}
		field.SetInt(int64(i))

	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("environment variable %s: expected boolean (true/false/1/0), got %q", tagName, val)
		}
		field.SetBool(b)

	case reflect.Slice:
		// Handle []string (CSV)
		if field.Type().Elem().Kind() == reflect.String {
			parts := strings.Split(val, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			field.Set(reflect.ValueOf(parts))
		} else {
			return fmt.Errorf("unsupported slice type for %s", tagName)
		}

	default:
		return fmt.Errorf("unsupported type %s for environment variable %s", field.Kind(), tagName)
	}

	return nil
}
