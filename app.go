package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/kovi/yaar/internal/api"
	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/auth"
	"github.com/kovi/yaar/internal/config"
	"github.com/kovi/yaar/middleware"
)

// App is a fully wired instance: routes registered, dependencies constructed,
// background workers not yet started.
//
// main() used to build all of this inline, which meant the wiring — route
// registration, the sync-trigger hookup, janitor startup, graceful shutdown —
// could only be exercised by running the real binary and poking it over HTTP.
// Splitting it out lets tests assert that everything is connected without
// duplicating the setup (and drifting from it, as integration/setup_test.go does).
type App struct {
	Config  *config.Config
	DB      *gorm.DB
	Handler *api.Handler
	Auth    *auth.AuthHandler
	Auditor *audit.Auditor
	Engine  *gin.Engine
	Log     *logrus.Entry

	sync *api.SyncController
}

// BuildApp constructs every dependency and registers all routes.
//
// It performs no I/O beyond opening the database and audit log, starts no
// goroutines, and binds no port — so a test can build one, make assertions
// about it, and throw it away.
func BuildApp(cfg *config.Config, log *logrus.Entry) (*App, error) {
	log.Info("Initializing audit log: ", cfg.Audit.File)
	auditOpts := []audit.Option{
		audit.WithRotation(cfg.Audit.MaxSizeBytes, cfg.Audit.MaxBackups),
	}
	if cfg.Audit.MirrorToStdout {
		auditOpts = append(auditOpts, audit.WithStdoutMirror())
	}
	auditor, err := audit.NewAuditor(cfg.Audit.File, auditOpts...)
	if err != nil {
		return nil, fmt.Errorf("initialize auditor: %w", err)
	}

	log.Infof("Opening db: %v", cfg.Database.File)
	db, err := config.ConnectDB(cfg.Database.File)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	log.Info("Auto migrate")
	if err := api.AutoMigrate(db); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	log.Infof("Data dir: %v", cfg.Storage.BaseDir)
	r := gin.New()
	r.Use(middleware.LogrusMiddleware(logrus.StandardLogger()))
	r.Use(gin.Recovery())

	m := &api.Handler{
		BaseDir:   cfg.Storage.BaseDir,
		DB:        db,
		Config:    cfg,
		Log:       log.WithField("module", "api"),
		Audit:     auditor,
		StartTime: time.Now(),
	}

	authH := &auth.AuthHandler{
		DB:        db,
		Config:    *cfg,
		Audit:     auditor,
		UserCache: *auth.NewUserCache(),
		Log:       logrus.WithField("module", "auth"),
	}

	log.Infof("Web dir: %v", cfg.Server.WebDir)
	r.Static("/_/static", path.Join(cfg.Server.WebDir, "static"))
	// SetAuditor must precede Identify: the authorization gates record denials
	// through the context, and Identify itself rejects bad API tokens.
	r.Use(auth.SetAuditor(auditor))
	r.Use(auth.Identify(cfg.Server.JwtSecret, db, &authH.UserCache))

	// The sync controller must exist before RegisterRoutes so the sync trigger
	// can be registered inside the admin-gated `system` group rather than as a
	// bare, unauthenticated route.
	sc := api.NewSyncController(m)
	m.TriggerSync = sc.Trigger

	m.RegisterRoutes(r)
	authH.RegisterRoutes(r, db, cfg, auditor)

	return &App{
		Config:  cfg,
		DB:      db,
		Handler: m,
		Auth:    authH,
		Auditor: auditor,
		Engine:  r,
		Log:     log,
		sync:    sc,
	}, nil
}

// StartWorkers launches the janitor, filesystem-sync and vacuum goroutines.
// They stop when ctx is cancelled.
func (a *App) StartWorkers(ctx context.Context) {
	a.Handler.StartJanitor(ctx, 30*time.Second)
	a.sync.Start(ctx, 10*time.Second, 1*time.Hour)
	a.Handler.StartVacuumScheduler(ctx, a.Config.Maintenance.VacuumIntervalDuration)
}

// LogRoutes writes every registered route to the log.
func (a *App) LogRoutes() {
	for _, route := range a.Engine.Routes() {
		a.Log.Infof("route registered: %s %s", route.Method, route.Path)
	}
}

// ShutdownTimeout bounds how long in-flight requests may take to drain before
// the server is forced closed.
const ShutdownTimeout = 30 * time.Second

// Serve runs the HTTP server until ctx is cancelled, then drains gracefully.
//
// ready, when non-nil, is closed once the listener is accepting connections —
// tests use it instead of polling the port. The listener is created before
// returning so a caller that gets a nil error knows the port is bound.
func (a *App) Serve(ctx context.Context, ready chan<- struct{}) error {
	addr := fmt.Sprintf(":%d", a.Config.Server.Port)

	// Bind before serving so a port conflict surfaces here as an error rather
	// than asynchronously inside the goroutine below.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	srv := &http.Server{Handler: a.Engine}

	serveErr := make(chan error, 1)
	go func() {
		a.Log.Info("Listening on ", addr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	if ready != nil {
		close(ready)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("server failed: %w", err)
		}
	case <-ctx.Done():
		a.Log.Info("Shutdown signal received, draining connections")
	}

	// Stop listening and let in-flight requests finish. The timeout bounds a
	// stuck request so a hung upload cannot block the shutdown indefinitely.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		a.Log.WithError(err).Error("Graceful shutdown failed; forcing close")
		if closeErr := srv.Close(); closeErr != nil {
			a.Log.WithError(closeErr).Error("Forced close failed")
		}
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	a.Log.Info("Server stopped")
	return nil
}
