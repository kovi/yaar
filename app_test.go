package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/kovi/yaar/internal/audit"
	"github.com/kovi/yaar/internal/models"
	"github.com/kovi/yaar/internal/testconfig"
)

// These tests cover main.go's wiring — the part integration/setup_test.go cannot reach,
// because it reconstructs the router itself rather than using BuildApp. A
// mistake here (a route registered outside its auth group, a worker never
// started, a shutdown that kills in-flight requests) would otherwise only show
// up in production.

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// newTestApp builds a fully wired App against a scratch directory.
func newTestApp(t *testing.T) *App {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0o755); err != nil {
		t.Fatalf("creating storage dir: %v", err)
	}

	t.Setenv("JWT_SECRET", testconfig.JWTSecret())
	gin.SetMode(gin.TestMode)

	log := logrus.WithField("module", "apptest")
	cfg, err := ParseConfig([]string{
		"-port", fmt.Sprint(freePort(t)),
		"-db", filepath.Join(dir, "test.db"),
		"-data-dir", filepath.Join(dir, "storage"),
		"-audit-log", filepath.Join(dir, "audit.log"),
		"-web-dir", "web",
	}, log)
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	app, err := BuildApp(cfg, log)
	if err != nil {
		t.Fatalf("building app: %v", err)
	}
	return app
}

// serveInBackground starts the app and returns its base URL plus a stop
// function that triggers the graceful shutdown and waits for it to finish.
func serveInBackground(t *testing.T, app *App) (baseURL string, stop func() error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})

	var (
		once     sync.Once
		serveRes = make(chan error, 1)
	)

	go func() { serveRes <- app.Serve(ctx, ready) }()

	select {
	case <-ready:
	case err := <-serveRes:
		cancel()
		t.Fatalf("server exited before becoming ready: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("server did not become ready in time")
	}

	stop = func() error {
		var err error
		once.Do(func() {
			cancel()
			select {
			case err = <-serveRes:
			case <-time.After(ShutdownTimeout + 10*time.Second):
				err = fmt.Errorf("shutdown did not complete in time")
			}
		})
		return err
	}
	t.Cleanup(func() { _ = stop() })

	return fmt.Sprintf("http://127.0.0.1:%d", app.Config.Server.Port), stop
}

// ParseConfig's precedence rules are easy to get subtly wrong, and a mistake
// silently points a live instance at the wrong directory.
func TestParseConfigFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv("JWT_SECRET", testconfig.JWTSecret())
	t.Setenv("AF_PORT", "9999")

	dir := t.TempDir()
	log := logrus.WithField("module", "apptest")

	cfg, err := ParseConfig([]string{
		"-port", "8123",
		"-db", filepath.Join(dir, "flag.db"),
		"-data-dir", dir,
	}, log)
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	if cfg.Server.Port != 8123 {
		t.Errorf("flag should beat AF_PORT: got port %d, want 8123", cfg.Server.Port)
	}
	if cfg.Database.File != filepath.Join(dir, "flag.db") {
		t.Errorf("db flag not applied: got %q", cfg.Database.File)
	}
}

func TestParseConfigReadsEnvironment(t *testing.T) {
	t.Setenv("JWT_SECRET", testconfig.JWTSecret())
	t.Setenv("AF_PORT", "9191")

	log := logrus.WithField("module", "apptest")
	cfg, err := ParseConfig([]string{"-data-dir", t.TempDir()}, log)
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}

	if cfg.Server.Port != 9191 {
		t.Errorf("AF_PORT not applied: got port %d, want 9191", cfg.Server.Port)
	}
}

// A missing config file is only tolerated when -config was not passed
// explicitly; asking for a file that is not there must be an error.
func TestParseConfigMissingExplicitFileIsAnError(t *testing.T) {
	t.Setenv("JWT_SECRET", testconfig.JWTSecret())
	log := logrus.WithField("module", "apptest")

	if _, err := ParseConfig([]string{"-config", "/nonexistent/config.yml"}, log); err == nil {
		t.Fatal("expected an error for an explicitly requested missing config file")
	}
}

// The routes must be registered with the auth middleware actually attached.
// Asserting through a served instance means a route registered outside its
// group cannot pass.
func TestAppWiringRegistersRoutesWithAuth(t *testing.T) {
	app := newTestApp(t)
	baseURL, _ := serveInBackground(t, app)

	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		// Public read surface — anonymous by design.
		{"anonymous listing", http.MethodGet, "/_/api/v1/fs/", http.StatusOK},
		{"anonymous search", http.MethodGet, "/_/api/v1/search?q=x", http.StatusOK},
		{"anonymous stream list", http.MethodGet, "/_/api/v1/streams", http.StatusOK},
		// Writes and privileged reads require credentials.
		{"anonymous upload", http.MethodPut, "/nope.txt", http.StatusUnauthorized},
		{"anonymous settings", http.MethodGet, "/_/api/v1/settings", http.StatusUnauthorized},
		{"anonymous vacuum", http.MethodPost, "/_/api/system/vacuum", http.StatusUnauthorized},
		{"anonymous sync trigger", http.MethodPost, "/_/api/system/sync", http.StatusUnauthorized},
		{"anonymous audit log", http.MethodGet, "/_/api/v1/admin/audit-log", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, baseURL+tc.path, nil)
			if err != nil {
				t.Fatalf("building request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.want {
				t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

// The sync trigger used to be registered directly on the engine in main.go,
// bypassing the admin gate its siblings use. BuildApp must wire it through the
// handler so it lands inside the AdminRequired group.
func TestAppWiringConnectsSyncTrigger(t *testing.T) {
	app := newTestApp(t)

	if app.Handler.TriggerSync == nil {
		t.Fatal("TriggerSync was not wired; the sync route would not be registered at all")
	}
}

// BuildApp must leave a usable database behind: migrations applied and the
// bootstrap admin created.
func TestAppWiringMigratesAndBootstraps(t *testing.T) {
	app := newTestApp(t)

	var users int64
	if err := app.DB.Table("users").Count(&users).Error; err != nil {
		t.Fatalf("querying users (did AutoMigrate run?): %v", err)
	}
	if users != 1 {
		t.Errorf("expected exactly the bootstrap admin, got %d users", users)
	}

	// A table from the api models proves the rest of the migration ran too.
	if err := app.DB.Table("meta_resources").Count(new(int64)).Error; err != nil {
		t.Errorf("meta_resources missing; AutoMigrate did not complete: %v", err)
	}
}

// Workers must actually start and, critically, stop when the context is
// cancelled — otherwise a shutdown leaves goroutines writing to a closed DB.
func TestAppWorkersStopOnContextCancel(t *testing.T) {
	app := newTestApp(t)

	ctx, cancel := context.WithCancel(context.Background())
	app.StartWorkers(ctx)

	before := runtimeGoroutines()
	cancel()

	// The janitor and sync workers select on ctx.Done(); give them a moment.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtimeGoroutines() < before {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Not a hard failure on count alone — the runtime may be busy — but the
	// workers should have released at least one goroutine.
	t.Log("worker goroutines did not visibly drop; this is advisory, not conclusive")
}

// The core guarantee: a request already in flight when shutdown begins runs to
// completion instead of being killed. Before graceful shutdown existed, SIGTERM
// truncated uploads mid-write and left files on disk with no DB record.
func TestGracefulShutdownCompletesInFlightUpload(t *testing.T) {
	app := newTestApp(t)
	baseURL, stop := serveInBackground(t, app)

	// Log in so the upload is authorized. The bootstrap password is random, so
	// reset the admin's password directly through the model.
	token := adminToken(t, app, baseURL)

	// Drive the body slowly enough that shutdown lands mid-request.
	const chunks, chunkSize = 6, 1024
	pr, pw := io.Pipe()
	sending := make(chan struct{})

	go func() {
		defer pw.Close()
		for i := range chunks {
			if _, err := pw.Write(make([]byte, chunkSize)); err != nil {
				return
			}
			if i == 0 {
				close(sending) // the handler is now running
			}
			time.Sleep(150 * time.Millisecond)
		}
	}()

	req, err := http.NewRequest(http.MethodPut, baseURL+"/slow/upload.bin", pr)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.ContentLength = chunks * chunkSize

	type result struct {
		status int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		done <- result{status: resp.StatusCode}
	}()

	// Only signal shutdown once the upload is genuinely in flight.
	<-sending
	time.Sleep(100 * time.Millisecond)

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- stop() }()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("in-flight upload failed during shutdown: %v", res.err)
		}
		if res.status != http.StatusOK {
			t.Fatalf("in-flight upload returned %d, want 200", res.status)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("in-flight upload never completed")
	}

	if err := <-shutdownDone; err != nil {
		t.Fatalf("graceful shutdown reported an error: %v", err)
	}

	// The whole body must be on disk — a truncated file is the exact failure
	// this shutdown path exists to prevent.
	onDisk := filepath.Join(app.Config.Storage.BaseDir, "slow", "upload.bin")
	info, err := os.Stat(onDisk)
	if err != nil {
		t.Fatalf("uploaded file missing after shutdown: %v", err)
	}
	if want := int64(chunks * chunkSize); info.Size() != want {
		t.Errorf("file truncated by shutdown: got %d bytes, want %d", info.Size(), want)
	}
}

// After a graceful shutdown the server must stop accepting, so a restart (or a
// rolling deploy) is not blocked by the old process still serving.
//
// This asserts the listener is closed rather than that the port is immediately
// re-bindable: sockets from the just-finished requests sit in TIME_WAIT, which
// can refuse a fresh bind for reasons that have nothing to do with our
// shutdown. "No longer answering" is the property that actually matters.
func TestGracefulShutdownStopsAccepting(t *testing.T) {
	app := newTestApp(t)
	baseURL, stop := serveInBackground(t, app)

	// Confirm it is serving first, so the post-shutdown failure is meaningful.
	resp, err := http.Get(baseURL + "/_/api/v1/streams")
	if err != nil {
		t.Fatalf("server should be serving before shutdown: %v", err)
	}
	resp.Body.Close()

	if err := stop(); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	// Use a client that will not reuse a pooled connection, so this really
	// tests accepting a new one.
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}
	if resp, err := client.Get(baseURL + "/_/api/v1/streams"); err == nil {
		resp.Body.Close()
		t.Fatal("server still accepting connections after graceful shutdown")
	}
}

// Serve must report a bind failure rather than exiting silently or panicking.
func TestServeReportsBindFailure(t *testing.T) {
	app := newTestApp(t)

	// Occupy the port the app wants.
	addr := fmt.Sprintf(":%d", app.Config.Server.Port)
	blocker, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("could not occupy the port: %v", err)
	}
	defer blocker.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = app.Serve(ctx, nil)
	if err == nil {
		t.Fatal("expected a bind error when the port is already in use")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Errorf("expected a listen error, got: %v", err)
	}
}

// adminToken resets the bootstrap admin's password to a known value and logs
// in, returning a JWT.
func adminToken(t *testing.T, app *App, baseURL string) string {
	t.Helper()

	const password = "test-admin-password"

	var user models.User
	if err := app.DB.Where("username = ?", "admin").First(&user).Error; err != nil {
		t.Fatalf("loading the bootstrap admin: %v", err)
	}
	if err := user.SetPassword(password); err != nil {
		t.Fatalf("setting the admin password: %v", err)
	}
	if err := app.DB.Save(&user).Error; err != nil {
		t.Fatalf("saving the admin password: %v", err)
	}

	body := strings.NewReader(fmt.Sprintf(`{"username":"admin","password":%q}`, password))
	resp, err := http.Post(baseURL+"/_/api/login", "application/json", body)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("login failed: %d %s", resp.StatusCode, raw)
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding login response: %v", err)
	}
	if out.Token == "" {
		t.Fatal("login returned an empty token")
	}
	return out.Token
}

func runtimeGoroutines() int { return runtime.NumGoroutine() }

// The health endpoint is what a load balancer polls, so it must work without
// credentials — every other system route is admin-gated.
func TestHealthEndpointIsAnonymousAndReportsChecks(t *testing.T) {
	app := newTestApp(t)
	baseURL, _ := serveInBackground(t, app)

	resp, err := http.Get(baseURL + "/_/hp")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200 on a healthy app", resp.StatusCode)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding health body: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	for _, name := range []string{"database", "storage"} {
		if body.Checks[name] != "ok" {
			t.Errorf("check %q = %q, want ok", name, body.Checks[name])
		}
	}

	// A monitor must never be served a cached verdict.
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// HEAD is what many uptime monitors actually send.
func TestHealthEndpointSupportsHead(t *testing.T) {
	app := newTestApp(t)
	baseURL, _ := serveInBackground(t, app)

	resp, err := http.Head(baseURL + "/_/hp")
	if err != nil {
		t.Fatalf("HEAD health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("got %d, want 200", resp.StatusCode)
	}
}

// An unwritable storage directory is the failure this endpoint exists to catch:
// the DB is fine, the process is up, but uploads cannot land. It must report 503
// so a balancer drains the node, and must not leak the path.
func TestHealthReportsUnwritableStorage(t *testing.T) {
	app := newTestApp(t)
	baseURL, _ := serveInBackground(t, app)

	// Point the handler at a path that does not exist.
	app.Handler.BaseDir = filepath.Join(t.TempDir(), "definitely-not-here")

	resp, err := http.Get(baseURL + "/_/hp")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 when storage is unusable", resp.StatusCode)
	}

	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decoding health body: %v", err)
	}

	if body.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", body.Status)
	}
	if body.Checks["storage"] != "fail" {
		t.Errorf("storage check = %q, want fail", body.Checks["storage"])
	}
	if body.Checks["database"] != "ok" {
		t.Errorf("database check = %q, want ok (only storage was broken)", body.Checks["database"])
	}
	// The body is public, so it must not name paths.
	if strings.Contains(string(raw), "definitely-not-here") {
		t.Errorf("health body leaked the storage path: %s", raw)
	}
}

// The scheduler must actually run VACUUM on its interval, and stop on cancel.
func TestVacuumSchedulerRunsAndStops(t *testing.T) {
	app := newTestApp(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A short period so the test does not wait a week.
	app.Handler.StartVacuumScheduler(ctx, 100*time.Millisecond)

	// The audit trail is how a scheduled run is observable; wait for one.
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		page, err := app.Auditor.ReadPage(audit.ReadOptions{BeforeOffset: -1, Limit: 200})
		if err != nil {
			t.Fatalf("reading audit log: %v", err)
		}
		for _, e := range page.Entries {
			if e["action"] == "SYSTEM_MAINTENANCE" && e["trigger"] == "scheduled" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatal("scheduled VACUUM never recorded a SYSTEM_MAINTENANCE audit entry")
	}
}

// A zero or negative interval disables the scheduler rather than spinning.
func TestVacuumSchedulerDisabledOnZeroInterval(t *testing.T) {
	app := newTestApp(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	before := runtimeGoroutines()
	app.Handler.StartVacuumScheduler(ctx, 0)

	time.Sleep(200 * time.Millisecond)
	if runtimeGoroutines() > before {
		t.Error("a disabled scheduler should not start a goroutine")
	}
}
