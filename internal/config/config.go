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
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port      int    `yaml:"port" json:"port" env:"AF_PORT"`
		JwtSecret string `yaml:"jwt_secret" env:"JWT_SECRET" json:"-"`
		WebDir    string `yaml:"web_dir" json:"web_dir" env:"AF_WEB_DIR" `
	} `yaml:"server" json:"server"`

	Database struct {
		File string `yaml:"file" json:"file" env:"AF_DB_FILE"`
	} `yaml:"database" json:"database"`

	Storage struct {
		BaseDir            string   `yaml:"base_dir" json:"base_dir" env:"AF_BASE_DIR"`
		MaxUploadSize      string   `yaml:"max_upload_size" json:"max_upload_size" env:"AF_MAX_SIZE"`
		MaxUploadSizeBytes int64    `yaml:"-" json:"-"`
		ProtectedPaths     []string `yaml:"protected_paths" json:"protected_paths" env:"AF_PROTECTED_PATHS"`
	} `yaml:"storage" json:"storage"`

	Audit struct {
		File string `yaml:"file" json:"file" env:"AF_AUDIT_LOG"`
		// MirrorToStdout also writes every audit entry to the standard logger.
		// Off by default: it doubles audit write volume and the mirrored copy is
		// subject to no rotation, so it is opt-in for deployments that ship
		// container stdout to a log collector.
		MirrorToStdout bool `yaml:"mirror_to_stdout" json:"mirror_to_stdout" env:"AF_AUDIT_MIRROR_STDOUT"`
		// MaxSize is the size at which the audit log rotates, in the same format
		// as max_upload_size ("100MB"). Rotation keeps MaxBackups generations,
		// so worst-case disk use is roughly MaxSize * (MaxBackups + 1).
		MaxSize      string `yaml:"max_size" json:"max_size" env:"AF_AUDIT_MAX_SIZE"`
		MaxSizeBytes int64  `yaml:"-" json:"-"`
		MaxBackups   int    `yaml:"max_backups" json:"max_backups" env:"AF_AUDIT_MAX_BACKUPS"`
	} `yaml:"audit" json:"audit"`

	Maintenance struct {
		// VacuumInterval is how often the scheduled VACUUM runs, as a Go duration
		// ("24h", "168h"). VACUUM holds the single SQLite connection for its
		// duration, so this is deliberately infrequent. Set to "0" to disable it
		// and rely on the admin endpoint alone.
		VacuumInterval string `yaml:"vacuum_interval" json:"vacuum_interval" env:"AF_VACUUM_INTERVAL"`
		// VacuumIntervalDuration is the parsed form, resolved by Finalize.
		VacuumIntervalDuration time.Duration `yaml:"-" json:"-"`
	} `yaml:"maintenance" json:"maintenance"`

	Logging struct {
		// Level is a logrus level name: panic, fatal, error, warn, info, debug or
		// trace. The level used to be hardcoded to debug, so production ran at
		// debug verbosity with no way to turn it down.
		Level string `yaml:"level" json:"level" env:"AF_LOG_LEVEL"`
	} `yaml:"logging" json:"logging"`
}

// NewConfig sets the hardcoded "Factory Defaults"
func NewConfig() *Config {
	cfg := &Config{}

	cfg.Server.Port = 8080
	cfg.Database.File = "artifactory.db"
	cfg.Storage.BaseDir = "storage"
	cfg.Storage.MaxUploadSize = "100MB"
	cfg.Audit.File = "audit.log"
	cfg.Audit.MaxSize = "100MB"
	// -1 rather than 0, because 0 is the meaningful "retain no backups" setting
	// and would be indistinguishable from an unset field. Finalize maps the
	// negative sentinel to the default.
	cfg.Audit.MaxBackups = -1
	cfg.Server.WebDir = "web"
	// Weekly: frequent enough to reclaim space from expired artifacts, rare
	// enough that holding the single SQLite connection is not disruptive.
	cfg.Maintenance.VacuumInterval = "168h"
	cfg.Logging.Level = "info"

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

// placeholderJwtSecret is the value shipped in config.example.yml. Starting with
// it in place would sign real tokens with a public secret, so Finalize rejects
// it — a documented "please change this" is not enough on its own, as a
// previously committed config.yml with a hardcoded secret demonstrated.
const placeholderJwtSecret = "CHANGE_ME_TO_A_RANDOM_32_CHAR_MINIMUM_SECRET"

func (c *Config) Finalize() error {
	if c.Server.JwtSecret == placeholderJwtSecret {
		return errors.New("Server.JwtSecret is still the example placeholder — set a real secret (e.g. `openssl rand -base64 48`) in config.yml or JWT_SECRET")
	}
	if len(c.Server.JwtSecret) < 32 {
		return errors.New("Server.JwtSecret should be at least 32 characters")
	}

	bytes, err := ParseBytes(c.Storage.MaxUploadSize)
	if err != nil {
		return err
	}
	c.Storage.MaxUploadSizeBytes = bytes

	// Normalize paths to ensure they start with / and don't end with /
	for i, p := range c.Storage.ProtectedPaths {
		cleaned := "/" + strings.Trim(filepath.ToSlash(p), "/")
		c.Storage.ProtectedPaths[i] = cleaned
	}

	if c.Audit.MaxSize == "" {
		c.Audit.MaxSize = "100MB"
	}
	auditBytes, err := ParseBytes(c.Audit.MaxSize)
	if err != nil {
		return fmt.Errorf("audit.max_size: %w", err)
	}
	c.Audit.MaxSizeBytes = auditBytes

	if c.Audit.MaxBackups < 0 {
		c.Audit.MaxBackups = 5
	}

	// "0" (or "") disables the scheduled vacuum; anything else must parse, so a
	// typo fails at startup instead of silently disabling maintenance.
	if c.Maintenance.VacuumInterval == "" {
		c.Maintenance.VacuumInterval = "168h"
	}
	vacuumEvery, err := time.ParseDuration(c.Maintenance.VacuumInterval)
	if err != nil {
		return fmt.Errorf("maintenance.vacuum_interval %q: %w", c.Maintenance.VacuumInterval, err)
	}
	if vacuumEvery < 0 {
		return fmt.Errorf("maintenance.vacuum_interval must not be negative, got %q", c.Maintenance.VacuumInterval)
	}
	c.Maintenance.VacuumIntervalDuration = vacuumEvery

	// Validated here so a typo fails at startup rather than silently leaving the
	// level at whatever logrus defaults to.
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if _, err := logrus.ParseLevel(c.Logging.Level); err != nil {
		return fmt.Errorf("invalid logging.level %q: %w", c.Logging.Level, err)
	}

	return nil
}

// LogLevel returns the parsed logging level. Finalize has already validated it,
// so the error case falls back to info rather than propagating.
func (c *Config) LogLevel() logrus.Level {
	lvl, err := logrus.ParseLevel(c.Logging.Level)
	if err != nil {
		return logrus.InfoLevel
	}
	return lvl
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
	re := regexp.MustCompile(`^(\d+)\s*([KMGT]?B|)$`)
	matches := re.FindStringSubmatch(s)
	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid size format: %s", s)
	}

	value, _ := strconv.ParseInt(matches[1], 10, 64)
	unit := matches[2]

	switch unit {
	case "":
		return value, nil
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
