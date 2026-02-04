package main

import (
	"context"
	"flag"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/middleware"
)

func LogRoutes(r *gin.Engine, logger *logrus.Entry) {
	for _, route := range r.Routes() {
		logger.Infof("route registered: %s %s", route.Method, route.Path)
	}
}

func main() {
	cfg := config.NewConfig()

	log := logrus.WithField("module", "main")
	logrus.SetLevel(logrus.DebugLevel)
	api.InitializeVersionInfo(log)

	configFile := flag.String("config", "config.yml", "config file path")
	portFlag := flag.Int("port", cfg.Server.Port, "HTTP server port")
	dbFlag := flag.String("db", cfg.Database.File, "Path to SQLite database file")
	baseDirFlag := flag.String("data-dir", cfg.Storage.BaseDir, "Base data directory for file storage")
	webDirFlag := flag.String("web-dir", cfg.Server.WebDir, "Base data directory for file storage")
	auditFlag := flag.String("audit-log", cfg.Audit.File, "Path to the audit log file")
	maxSizeFlag := flag.String("max-upload-size", cfg.Storage.MaxUploadSize, "Maximum upload size")
	flag.Parse()

	configArgProvided := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configArgProvided = true
		}
	})

	if err := cfg.LoadYAML(*configFile); err != nil {
		// if arg is not explicitly provided ignore the not-exist error code
		if configArgProvided || !os.IsNotExist(err) {
			log.Fatalf("Error loading config: %v", err)
		}
	} else {
		log.Infof("Loaded config %v", *configFile)
	}

	if err := cfg.LoadEnv(); err != nil {
		// This will print something like:
		// FATAL: environment variable AF_PORT: expected integer, got "abc"
		log.Fatalf("Invalid environment configuration: %v", err)
	}

	flag.Visit(func(f *flag.Flag) {
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
		}
	})

	err := cfg.Finalize()
	if err != nil {
		log.Fatalf("Invalid config: %v", err)
	}

	log.Info("Initializing audit log: ", cfg.Audit.File)
	auditor, err := audit.NewAuditor(cfg.Audit.File)
	if err != nil {
		log.Fatal("Failed to initialize auditor: ", err)
	}

	log.Infof("Opening db: %v", cfg.Database.File)
	db, err := config.ConnectDB(cfg.Database.File)
	if err != nil {
		panic(err)
	}

	log.Info("Auto migrate")
	if err := api.AutoMigrate(db); err != nil {
		panic(err)
	}

	log.Infof("Data dir: %v", cfg.Storage.BaseDir)
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(middleware.LogrusMiddleware(logrus.StandardLogger()))
	r.Use(gin.Recovery())

	m := api.Handler{
		BaseDir: cfg.Storage.BaseDir,
		DB:      db,
		Config:  cfg,
		Log:     log.WithField("module", "api"),
		Audit:   auditor,
	}

	authH := &auth.AuthHandler{
		DB:        db,
		Config:    *cfg,
		Audit:     auditor,
		UserCache: *auth.NewUserCache(),
		Log:       logrus.WithField("module", "auth")}

	log.Infof("Web dir: %v", cfg.Server.WebDir)
	r.Static("/_/static", path.Join(cfg.Server.WebDir, "static"))
	r.Use(auth.Identify(cfg.Server.JwtSecret, db, &authH.UserCache))

	m.RegisterRoutes(r)
	authH.RegisterRoutes(r, db, cfg, auditor)

	ctx, cancel := context.WithCancel(context.Background())
	m.StartJanitor(ctx, 30*time.Second)
	sc := api.NewSyncController(&m)
	sc.Start(ctx, 10*time.Second, 1*time.Hour)
	r.POST("/_/api/v1/system/sync", func(c *gin.Context) {
		sc.Trigger()
		c.JSON(200, gin.H{"status": "sync triggered"})
	})

	// --- log out all registered routes
	LogRoutes(r, log)

	// --- start
	log.Info("Listening on :", cfg.Server.Port)
	r.Run(":" + strconv.Itoa(cfg.Server.Port))

	cancel()
}
