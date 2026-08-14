package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/config"
)

// ParseConfig resolves configuration from, in increasing order of precedence:
// the YAML file, environment variables, then command-line flags.
//
// args is the argument list *without* the program name, so tests can drive it
// directly instead of mutating os.Args.
func ParseConfig(args []string, log *logrus.Entry) (*config.Config, error) {
	cfg := config.NewConfig()

	fs := flag.NewFlagSet("yaar", flag.ContinueOnError)
	configFile := fs.String("config", "config.yml", "config file path")
	portFlag := fs.Int("port", cfg.Server.Port, "HTTP server port")
	dbFlag := fs.String("db", cfg.Database.File, "Path to SQLite database file")
	baseDirFlag := fs.String("data-dir", cfg.Storage.BaseDir, "Base data directory for file storage")
	webDirFlag := fs.String("web-dir", cfg.Server.WebDir, "Directory containing the web assets")
	auditFlag := fs.String("audit-log", cfg.Audit.File, "Path to the audit log file")
	maxSizeFlag := fs.String("max-upload-size", cfg.Storage.MaxUploadSize, "Maximum upload size")
	logLevelFlag := fs.String("log-level", cfg.Logging.Level, "Log level (panic, fatal, error, warn, info, debug, trace)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	configArgProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configArgProvided = true
		}
	})

	if err := cfg.LoadYAML(*configFile); err != nil {
		// if arg is not explicitly provided ignore the not-exist error code
		if configArgProvided || !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		log.Infof("Loaded config %v", *configFile)
	}

	if err := cfg.LoadEnv(); err != nil {
		return nil, err
	}

	// Flags win over both the file and the environment.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			cfg.Server.Port = *portFlag
		case "db":
			cfg.Database.File = *dbFlag
		case "data-dir":
			cfg.Storage.BaseDir = *baseDirFlag
		case "audit-log":
			cfg.Audit.File = *auditFlag
		case "max-upload-size":
			cfg.Storage.MaxUploadSize = *maxSizeFlag
		case "web-dir":
			cfg.Server.WebDir = *webDirFlag
		case "log-level":
			cfg.Logging.Level = *logLevelFlag
		}
	})

	if err := cfg.Finalize(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func main() {
	os.Setenv("TZ", "UTC")

	log := logrus.WithField("module", "main")
	api.InitializeVersionInfo(log)

	cfg, err := ParseConfig(os.Args[1:], log)
	if err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Applied after parsing, since the level comes from config. It used to be
	// hardcoded to debug, so every deployment ran at debug verbosity.
	logrus.SetLevel(cfg.LogLevel())

	gin.SetMode(gin.ReleaseMode)

	app, err := BuildApp(cfg, log)
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// SIGINT/SIGTERM cancel this context, which both stops the background
	// workers and starts the HTTP drain. `docker stop` sends SIGTERM, so
	// without this an in-flight upload is killed mid-write and leaves a
	// truncated file on disk with no DB record.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	app.StartWorkers(ctx)
	app.LogRoutes()

	if err := app.Serve(ctx, nil); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
