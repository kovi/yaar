package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultMaxSizeBytes is the size at which the audit log rotates when no explicit
// limit is configured.
const DefaultMaxSizeBytes int64 = 100 * 1024 * 1024

// DefaultMaxBackups is how many rotated files are retained by default.
const DefaultMaxBackups = 5

// rotatingWriter is an io.Writer that renames its file aside once it exceeds
// maxSize, keeping at most maxBackups generations: audit.log.1 is the most
// recent, audit.log.2 older, and so on.
//
// This is written out rather than pulled in from lumberjack because the audit
// reader pages through the file by byte offset and has to be able to keep
// reading into the rotated generations (see reader.go). That coupling means the
// naming scheme is part of the package's contract, not an implementation detail
// we can delegate.
type rotatingWriter struct {
	mu sync.Mutex

	path       string
	maxSize    int64
	maxBackups int

	file *os.File
	size int64
}

func newRotatingWriter(path string, maxSize int64, maxBackups int) (*rotatingWriter, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSizeBytes
	}
	if maxBackups < 0 {
		maxBackups = 0
	}

	w := &rotatingWriter{path: path, maxSize: maxSize, maxBackups: maxBackups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// open attaches to the current log file, picking up the size of an existing one
// so a restart does not reset the rotation budget.
func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	size := int64(0)
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}

	w.file = f
	w.size = size
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Rotate before writing rather than after, so a single entry is never split
	// across two generations. An entry larger than maxSize still goes through
	// whole — a truncated JSON line would be worse than an oversized file.
	if w.size > 0 && w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file, shifts the existing backups down one, moves
// the live file to .1, and opens a fresh one.
//
// The writer holds an open handle, so the file must be reopened after the
// rename: without that the process would keep appending to the renamed inode
// and every subsequent entry would be invisible to the reader, which resolves
// the log by path.
func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close audit log for rotation: %w", err)
	}

	// Shift backups from the oldest down, so nothing is overwritten while a
	// lower-numbered file still needs its slot.
	if w.maxBackups > 0 {
		oldest := backupPath(w.path, w.maxBackups)
		if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove oldest audit backup: %w", err)
		}

		for i := w.maxBackups - 1; i >= 1; i-- {
			from := backupPath(w.path, i)
			to := backupPath(w.path, i+1)
			if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("shift audit backup %s: %w", from, err)
			}
		}

		if err := os.Rename(w.path, backupPath(w.path, 1)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate audit log: %w", err)
		}
	} else {
		// No backups retained: the live file is simply discarded.
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove audit log: %w", err)
		}
	}

	return w.open()
}

// Close releases the underlying file.
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// backupPath returns the name of the nth rotated generation: audit.log -> audit.log.1.
func backupPath(path string, n int) string {
	return path + "." + strconv.Itoa(n)
}

// backupGenerations returns the rotated files for path that exist on disk, in
// read order — newest first, i.e. .1 then .2 and so on.
//
// The reader uses this to continue paging past the live file, so history stays
// browsable through the API after a rotation instead of appearing to vanish.
func backupGenerations(path string, maxBackups int) []string {
	if maxBackups <= 0 {
		maxBackups = DefaultMaxBackups
	}

	var out []string
	for i := 1; i <= maxBackups; i++ {
		p := backupPath(path, i)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// discoverBackups finds rotated generations on disk without assuming a backup
// count, for readers constructed without rotation configured (as the audit-log
// API handler's Auditor may be, and as tests are).
func discoverBackups(path string) []string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type gen struct {
		n    int
		path string
	}
	var found []gen

	prefix := base + "."
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
		if err != nil || n < 1 {
			continue
		}
		found = append(found, gen{n: n, path: filepath.Join(dir, name)})
	}

	// Newest first: .1 before .2.
	sort.Slice(found, func(i, j int) bool { return found[i].n < found[j].n })

	out := make([]string, 0, len(found))
	for _, g := range found {
		out = append(out, g.path)
	}
	return out
}
