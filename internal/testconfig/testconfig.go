// Package testconfig exposes the shared test settings defined in
// testdata/test-env.sh.
//
// The Go and Python suites both need the same JWT secret and protected-path
// convention. Hardcoding them in two languages meant a change in one place
// produced auth failures only in the other suite, which is a slow thing to
// debug. Both now read the same file.
package testconfig

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvFile is the shared settings file, relative to the repository root.
const EnvFile = "testdata/test-env.sh"

var (
	once    sync.Once
	values  map[string]string
	loadErr error
)

// load parses the shell-style export lines in the settings file. It is not a
// general shell parser: it understands `export KEY="value"` and `KEY=value`,
// which is all the file uses.
func load() (map[string]string, error) {
	root, err := repoRoot()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(root, EnvFile)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	out := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		out[strings.TrimSpace(key)] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return out, nil
}

// repoRoot walks upward until it finds go.mod, so callers work regardless of
// which package directory the test binary runs in.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

func get(key string) string {
	once.Do(func() { values, loadErr = load() })
	if loadErr != nil {
		panic(fmt.Sprintf("testconfig: %v", loadErr))
	}
	v, ok := values[key]
	if !ok {
		panic(fmt.Sprintf("testconfig: %s is not defined in %s", key, EnvFile))
	}
	return v
}

// JWTSecret is the signing secret both suites use.
func JWTSecret() string { return get("YAAR_TEST_JWT_SECRET") }

// ProtectedPaths is the protected-path list the UI suite relies on.
func ProtectedPaths() string { return get("YAAR_TEST_PROTECTED_PATHS") }

// UIPort is the port the browser-driven suite binds.
func UIPort() string { return get("YAAR_TEST_UI_PORT") }
