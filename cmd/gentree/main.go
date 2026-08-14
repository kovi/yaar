// Command gentree reads a yaar metadata database and materializes the file and
// directory structure it describes onto disk as empty placeholder files.
//
// This lets you point a yaar instance at a real-looking storage tree and test
// migration / browsing without copying the actual (potentially huge) artifacts.
//
// Example:
//
//	go run ./cmd/gentree \
//	  -db db-migrate-error/yaar/db/artifactory.db \
//	  -out /tmp/yaar-data
//
// Files are created sparse by default (correct apparent size, ~no disk usage).
// Use -empty to create zero-length files instead.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type resource struct {
	Path    string
	Type    string
	Size    int64
	ModTime time.Time
}

func main() {
	dbFile := flag.String("db", "", "path to the yaar sqlite database (required)")
	outDir := flag.String("out", "", "target base directory to generate the tree into (required)")
	empty := flag.Bool("empty", false, "create zero-length files instead of sparse files with the recorded size")
	dryRun := flag.Bool("dry-run", false, "print what would be created without touching the filesystem")
	flag.Parse()

	if *dbFile == "" || *outDir == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*dbFile, *outDir, *empty, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(dbFile, outDir string, empty, dryRun bool) error {
	// Open read-only so we never disturb a DB that may be live / mid-migration.
	// Note: do NOT use immutable=1 — it makes SQLite ignore the WAL, so against
	// a live database we'd read a stale snapshot and miss uncommitted rows.
	dsn := fmt.Sprintf("file:%s?mode=ro", dbFile)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}

	var rows []resource
	if err := db.Table("meta_resources").
		Select("path, type, size, mod_time").
		Order("path").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("query resources: %w", err)
	}

	abs, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}

	var dirs, files int
	var totalSize int64
	for _, r := range rows {
		// DB paths are absolute ("/a/b/c"); join them under the output base.
		rel := strings.TrimPrefix(filepath.Clean(r.Path), string(os.PathSeparator))
		target := filepath.Join(abs, rel)

		// Refuse to escape the output dir (defense against ".." in stored paths).
		if target != abs && !strings.HasPrefix(target, abs+string(os.PathSeparator)) {
			return fmt.Errorf("path %q escapes output dir", r.Path)
		}

		switch r.Type {
		case "dir":
			dirs++
			if dryRun {
				fmt.Printf("DIR  %s\n", target)
				continue
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		default: // "file"
			files++
			totalSize += r.Size
			if dryRun {
				fmt.Printf("FILE %s (%d bytes)\n", target, r.Size)
				continue
			}
			// Files can sit in directories that have no explicit "dir" row.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			if err := writeFile(target, r.Size, empty); err != nil {
				return fmt.Errorf("write %s: %w", target, err)
			}
			// Replicate the recorded mod_time so the tree looks authentic.
			// Skip Go zero-time values, which would stamp the year 0001.
			if !r.ModTime.IsZero() {
				if err := os.Chtimes(target, r.ModTime, r.ModTime); err != nil {
					return fmt.Errorf("chtimes %s: %w", target, err)
				}
			}
		}
	}

	mode := "sparse"
	if empty {
		mode = "empty"
	}
	if dryRun {
		mode = "dry-run"
	}
	fmt.Printf("done: %d dirs, %d files (%s) under %s\n", dirs, files, mode, abs)
	return nil
}

// writeFile creates target. When empty is false and size > 0 the file is made
// sparse: it reports the recorded size but consumes almost no disk.
func writeFile(target string, size int64, empty bool) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if empty || size <= 0 {
		return nil
	}
	// Sparse: seek to size-1 and write a single byte so the file has the right
	// apparent size without allocating the blocks.
	if _, err := f.Seek(size-1, 0); err != nil {
		return err
	}
	_, err = f.Write([]byte{0})
	return err
}
