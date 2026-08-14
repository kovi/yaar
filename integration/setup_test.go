package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/internal/testconfig"
	"github.com/kovi/yaar/middleware"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type strictLogger struct {
	logger.Interface
}

func (l *strictLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if err != nil {
		// Note: FirstOrCreate() triggers ErrRecordNotFound internally. If panicking here
		// breaks your tests, you can bypass it by uncommenting the following lines:
		if errors.Is(err, gorm.ErrRecordNotFound) {
			l.Interface.Trace(ctx, begin, fc, err)
			return
		}
		sql, _ := fc()
		panic(fmt.Sprintf("GORM Query Error: %v\nSQL: %s", err, sql))
	}

	var caller string
	for i := 2; i < 15; i++ {
		pc, file, line, ok := runtime.Caller(i)
		// Skip GORM internals and the logger wrapper itself
		if ok && !strings.Contains(file, "gorm.io") && !strings.HasSuffix(file, "setup_test.go") {
			funcName := runtime.FuncForPC(pc).Name()
			idx := strings.LastIndex(funcName, "/")
			if idx >= 0 {
				funcName = funcName[idx+1:]
			}
			caller = fmt.Sprintf("%s:%d %s", filepath.Base(file), line, funcName)
			break
		}
	}

	wrappedFc := func() (string, int64) {
		sql, rows := fc()
		if caller != "" {
			return fmt.Sprintf("[%s] %s", caller, sql), rows
		}
		return sql, rows
	}

	l.Interface.Trace(ctx, begin, wrappedFc, err)
}

func (l *strictLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	panic(fmt.Sprintf("GORM Error: "+msg, data...))
}

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
		cfg.Server.JwtSecret = testconfig.JWTSecret()
	}
	PushNewConfig(cfg)
	t.Cleanup(PopConfig)
}

func setupServer(db *gorm.DB, rootDir, baseDir string) *gin.Engine {
	os.Setenv("TZ", "UTC")
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
	cfg.Server.JwtSecret = testconfig.JWTSecret()
	// Tests run with the integration package dir as CWD, so point at the repo's real web
	// assets — the SPA index is served for directory views and 404 pages.
	cfg.Server.WebDir = "../web"
	err = cfg.Finalize()
	if err != nil {
		panic(err)
	}

	logrus.SetLevel(logrus.DebugLevel)

	AuthH = &auth.AuthHandler{DB: db, Config: *cfg, Audit: auditor, UserCache: *auth.NewUserCache(), Log: logrus.WithField("module", "auth")}

	// setup router
	router := gin.New()
	router.Use(DebugMiddleware())
	router.Use(middleware.LogrusMiddleware(logrus.StandardLogger()))
	// Mirrors BuildApp: the authorization gates emit AUTH_DENIED through the
	// auditor they find on the context, so it must be set before Identify.
	router.Use(auth.SetAuditor(auditor))
	router.Use(auth.Identify(cfg.Server.JwtSecret, db, &AuthH.UserCache))
	Meta = &api.Handler{
		BaseDir: baseDir,
		DB:      db,
		Log:     logrus.WithField("module", "meta"),
		Config:  cfg,
		Audit:   auditor,
		// Record calls instead of running a real sync: the tests only assert on
		// who is allowed to reach the route.
		TriggerSync: func() { syncTriggers.Add(1) },
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

	// syncTriggers counts how many times the sync route actually reached its
	// handler, so tests can prove a rejected request did not run the sync.
	syncTriggers atomic.Int64
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
	db = db.Debug()
	db.Logger = &strictLogger{Interface: db.Logger}

	router = setupServer(db, root, baseDir)
	server = httptest.NewServer(router)
	defer server.Close()

	os.Exit(m.Run())
}
