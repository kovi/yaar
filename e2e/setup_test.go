package e2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/middleware"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var (
	prevConfigs []*config.Config
)

func PushNewConfig(c *config.Config) {
	prevConfigs = append(prevConfigs, Meta.Config)
	Meta.Config = c
	err := Meta.Config.Finalize()
	if err != nil {
		panic(err)
	}
}

func PopConfig() {
	Meta.Config = prevConfigs[len(prevConfigs)-1]
	prevConfigs = prevConfigs[:len(prevConfigs)-1]
}

func WithConfig(t *testing.T, fn func(*config.Config)) {
	cfg := config.NewConfig()
	fn(cfg)
	if cfg.Server.JwtSecret == "" {
		cfg.Server.JwtSecret = "testsecret123456789ö123456789123456789"
	}
	PushNewConfig(cfg)
	t.Cleanup(PopConfig)
}

func setupServer(db *gorm.DB, rootDir, baseDir string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	// setup db
	if err := api.AutoMigrate(db); err != nil {
		panic(err)
	}

	auditor, err := audit.NewAuditor(rootDir + "/audit.log")
	if err != nil {
		panic(err)
	}

	cfg := config.NewConfig()
	cfg.Server.JwtSecret = "your_project/internal/models/laptop"
	err = cfg.Finalize()
	if err != nil {
		panic(err)
	}

	AuthH = &auth.AuthHandler{DB: db, Config: *cfg, Audit: auditor, UserCache: *auth.NewUserCache(), Log: logrus.WithField("module", "auth")}

	// setup router
	router := gin.New()
	router.Use(middleware.LogrusMiddleware(logrus.StandardLogger()))
	router.Use(auth.Identify(cfg.Server.JwtSecret, db, &AuthH.UserCache))
	Meta = &api.Handler{
		BaseDir: baseDir,
		DB:      db,
		Log:     logrus.WithField("module", "meta"),
		Config:  cfg,
		Audit:   auditor,
	}
	Meta.RegisterRoutes(router)
	AuthH.RegisterRoutes(router, db, cfg, auditor)
	api.InitializeVersionInfo(Meta.Log)

	return router
}

func removeOldSuites(parent string) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "suite-") {
			if err := os.RemoveAll(filepath.Join(parent, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

var (
	router  *gin.Engine
	server  *httptest.Server
	baseDir string
	db      *gorm.DB
	Meta    *api.Handler
	AuthH   *auth.AuthHandler
)

func TestMain(m *testing.M) {

	if err := removeOldSuites("."); err != nil {
		panic(err)
	}

	root, err := os.MkdirTemp(".", "suite-*")
	if err != nil {
		panic(err)
	}

	baseDir = filepath.Join(root, "base")
	os.MkdirAll(baseDir, 0o755)

	db, err = config.ConnectDB(filepath.Join(root, "db.sqlite"))
	if err != nil {
		panic(err)
	}

	router = setupServer(db, root, baseDir)
	server = httptest.NewServer(router)
	defer server.Close()

	os.Exit(m.Run())
}
