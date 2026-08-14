package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countLines returns the number of non-empty lines in a file.
func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	require.NoError(t, sc.Err())
	return n
}

func TestRotation_CreatesBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Small cap so a handful of entries forces several rotations.
	a, err := NewAuditor(path, WithRotation(512, 3))
	require.NoError(t, err)
	defer a.Close()

	for i := 0; i < 200; i++ {
		a.Success("FILE_UPLOAD", fmt.Sprintf("/file%04d.txt", i))
	}

	assert.FileExists(t, path)
	assert.FileExists(t, backupPath(path, 1))

	// Retention is enforced: nothing beyond maxBackups survives.
	assert.NoFileExists(t, backupPath(path, 4))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.LessOrEqual(t, info.Size(), int64(512),
		"the live file must stay within the configured cap")
}

func TestRotation_WriterReopensAfterRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := NewAuditor(path, WithRotation(400, 2))
	require.NoError(t, err)
	defer a.Close()

	for i := 0; i < 50; i++ {
		a.Success("FILE_UPLOAD", fmt.Sprintf("/pre%04d.txt", i))
	}
	require.FileExists(t, backupPath(path, 1), "expected at least one rotation")

	// The writer holds an open handle. If it kept writing to the renamed inode
	// instead of reopening the path, this entry would never appear in the file
	// the reader resolves by name.
	a.Success("FILE_UPLOAD", "/after-rotation.txt")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "/after-rotation.txt",
		"entries written after a rotation must land in the live file")
}

func TestRotation_NoEntryIsSplitAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := NewAuditor(path, WithRotation(600, 5))
	require.NoError(t, err)
	defer a.Close()

	const total = 300
	for i := 0; i < total; i++ {
		a.Success("FILE_UPLOAD", fmt.Sprintf("/file%04d.txt", i))
	}
	require.NoError(t, a.Close())

	// Every line in every generation must be complete, parseable JSON. Rotating
	// mid-entry would leave a truncated line at a file boundary.
	files := append([]string{path}, discoverBackups(path)...)
	for _, p := range files {
		f, err := os.Open(p)
		require.NoError(t, err)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var m map[string]any
			assert.NoError(t, json.Unmarshal([]byte(line), &m),
				"truncated entry in %s: %q", p, line)
		}
		f.Close()
	}
}

func TestRotation_ResumesSizeOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := NewAuditor(path, WithRotation(2048, 2))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		a.Success("FILE_UPLOAD", fmt.Sprintf("/first%d.txt", i))
	}
	require.NoError(t, a.Close())

	before, err := os.Stat(path)
	require.NoError(t, err)

	// A restart must pick up the existing size rather than resetting the budget,
	// otherwise a frequently-restarted process never rotates.
	b, err := NewAuditor(path, WithRotation(2048, 2))
	require.NoError(t, err)
	defer b.Close()
	b.Success("FILE_UPLOAD", "/second.txt")

	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Greater(t, after.Size(), before.Size(),
		"reopening must append, not truncate")
	assert.Equal(t, 6, countLines(t, path))
}

func TestRotation_ZeroBackupsDiscards(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// maxBackups=0 is a real setting — rotate and keep nothing — and must not be
	// confused with "unset, use the default".
	a, err := NewAuditor(path, WithRotation(400, 0))
	require.NoError(t, err)
	defer a.Close()

	for i := 0; i < 100; i++ {
		a.Success("FILE_UPLOAD", fmt.Sprintf("/file%04d.txt", i))
	}

	assert.FileExists(t, path)
	assert.NoFileExists(t, backupPath(path, 1),
		"no backups should be retained when maxBackups is 0")
}

func TestRotation_ConcurrentWritesAreSerialized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := NewAuditor(path, WithRotation(1024, 5))
	require.NoError(t, err)

	const writers = 8
	const perWriter = 50

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				a.Success("FILE_UPLOAD", fmt.Sprintf("/w%d-file%04d.txt", w, i))
			}
		}(w)
	}
	wg.Wait()
	require.NoError(t, a.Close())

	// Retention deliberately discards older generations, so the count is capped
	// rather than equal to the number written. What must hold is that no write
	// was interleaved or torn: every surviving line is complete JSON.
	total := countLines(t, path)
	for _, p := range discoverBackups(path) {
		total += countLines(t, p)
	}
	assert.Greater(t, total, 0)
	assert.LessOrEqual(t, total, writers*perWriter)

	files := append([]string{path}, discoverBackups(path)...)
	for _, p := range files {
		f, err := os.Open(p)
		require.NoError(t, err)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &m),
				"torn line under concurrent writes in %s: %q", p, line)
		}
		f.Close()
	}
}

func TestReadPage_ContinuesIntoRotatedGenerations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Sized so every entry is retained across several generations: the point of
	// the test is that paging crosses file boundaries without losing entries,
	// not that retention drops them.
	const total = 120
	a, err := NewAuditor(path, WithRotation(4096, 20))
	require.NoError(t, err)
	defer a.Close()

	for i := 0; i < total; i++ {
		a.Success("FILE_UPLOAD", fmt.Sprintf("/file%04d.txt", i))
	}
	require.NotEmpty(t, discoverBackups(path), "test needs at least one rotation")

	// Page through exactly as the UI does, carrying both cursors.
	var all []map[string]any
	offset := int64(-1)
	gen := 0
	for i := 0; i < 200; i++ { // bound the loop so a bug fails instead of hanging
		page, err := a.ReadPage(ReadOptions{BeforeOffset: offset, Generation: gen, Limit: 7})
		require.NoError(t, err)
		all = append(all, page.Entries...)
		if !page.HasMore {
			break
		}
		offset = page.NextBeforeOffset
		gen = page.NextGeneration
	}

	// Entries written before the rotation must still be reachable.
	assert.Len(t, all, total,
		"paging must span every retained generation, not stop at the live file")

	seen := map[string]bool{}
	for _, e := range all {
		r, _ := e["resource"].(string)
		assert.False(t, seen[r], "duplicate entry across generations: %s", r)
		seen[r] = true
	}

	// Newest-first ordering holds across the file boundary.
	assert.Equal(t, "/file0119.txt", all[0]["resource"])
	assert.Equal(t, "/file0000.txt", all[len(all)-1]["resource"])
}

func TestReadPage_FilterSpansGenerations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := NewAuditor(path, WithRotation(4096, 20))
	require.NoError(t, err)
	defer a.Close()

	for i := 0; i < 120; i++ {
		action := "FILE_UPLOAD"
		if i%10 == 0 {
			action = "USER_LOGIN"
		}
		a.Success(action, fmt.Sprintf("/file%04d.txt", i))
	}
	require.NotEmpty(t, discoverBackups(path))

	var all []map[string]any
	offset := int64(-1)
	gen := 0
	for i := 0; i < 200; i++ {
		page, err := a.ReadPage(ReadOptions{
			BeforeOffset: offset, Generation: gen, Limit: 5, Filter: "USER_LOGIN",
		})
		require.NoError(t, err)
		all = append(all, page.Entries...)
		if !page.HasMore {
			break
		}
		offset = page.NextBeforeOffset
		gen = page.NextGeneration
	}

	assert.Len(t, all, 12, "filtering must apply across rotated files too")
	for _, e := range all {
		assert.Equal(t, "USER_LOGIN", e["action"])
	}
}

// A caller's detail key must never overwrite the fields that identify the event.
// "action" as a detail used to replace the action itself, so every
// SYSTEM_MAINTENANCE entry was written as action=vacuum and the category was
// unsearchable in the log.
func TestRecord_DetailKeysCannotOverwriteReservedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	a, err := NewAuditor(path)
	require.NoError(t, err)
	defer a.Close()

	a.Success("SYSTEM_MAINTENANCE", "database",
		"action", "vacuum",
		"resource", "spoofed",
		"user", "spoofed",
		"duration_ms", 12)

	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	e := page.Entries[0]

	assert.Equal(t, "SYSTEM_MAINTENANCE", e["action"], "the event category must survive")
	assert.Equal(t, "database", e["resource"])
	assert.Equal(t, "system", e["user"])

	// The detail is preserved under a prefixed key rather than dropped.
	assert.Equal(t, "vacuum", e["detail_action"])
	assert.Equal(t, "spoofed", e["detail_resource"])
	assert.EqualValues(t, 12, e["duration_ms"], "non-reserved keys pass through unchanged")
}
