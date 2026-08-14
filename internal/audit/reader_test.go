package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAuditorFromFile builds an Auditor that reads from an already-written file.
func newAuditorFromFile(path string) *Auditor {
	return &Auditor{filePath: path}
}

// writeEntries writes n JSON-lines entries to a temp file and returns an Auditor for it.
func writeEntries(t *testing.T, entries []map[string]any) *Auditor {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "audit*.log")
	require.NoError(t, err)
	for _, e := range entries {
		data, _ := json.Marshal(e)
		_, _ = f.Write(data)
		_, _ = f.Write([]byte("\n"))
	}
	require.NoError(t, f.Close())
	return newAuditorFromFile(f.Name())
}

func makeEntry(i int, action string) map[string]any {
	return map[string]any{
		"time":     fmt.Sprintf("2026-06-01T12:00:%02dZ", i%60),
		"action":   action,
		"resource": fmt.Sprintf("/file%04d.txt", i),
		"status":   "SUCCESS",
		"user":     "admin",
		"ip":       "127.0.0.1",
		"level":    "info",
		"msg":      "audit",
	}
}

func TestReadPage_NonExistentFile(t *testing.T) {
	a := newAuditorFromFile("/tmp/audit-does-not-exist-xyzxyz.log")
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Entries)
	assert.Equal(t, int64(-1), page.NextBeforeOffset)
	assert.False(t, page.ScanLimitHit)
}

func TestReadPage_EmptyFile(t *testing.T) {
	a := writeEntries(t, nil)
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, page.Entries)
	assert.Equal(t, int64(-1), page.NextBeforeOffset)
}

func TestReadPage_SingleEntry(t *testing.T) {
	a := writeEntries(t, []map[string]any{makeEntry(0, "FILE_UPLOAD")})
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "FILE_UPLOAD", page.Entries[0]["action"])
	assert.Equal(t, "/file0000.txt", page.Entries[0]["resource"])
	assert.Equal(t, int64(-1), page.NextBeforeOffset)
}

func TestReadPage_NewestFirst(t *testing.T) {
	entries := make([]map[string]any, 5)
	for i := range entries {
		entries[i] = makeEntry(i, "FILE_UPLOAD")
	}
	a := writeEntries(t, entries)

	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Entries, 5)
	// Newest (highest index) first
	assert.Equal(t, "/file0004.txt", page.Entries[0]["resource"])
	assert.Equal(t, "/file0000.txt", page.Entries[4]["resource"])
	assert.Equal(t, int64(-1), page.NextBeforeOffset)
}

func TestReadPage_Pagination(t *testing.T) {
	const total = 10
	entries := make([]map[string]any, total)
	for i := range entries {
		entries[i] = makeEntry(i, "FILE_UPLOAD")
	}
	a := writeEntries(t, entries)

	// Collect all entries via paginated requests
	var all []map[string]any
	offset := int64(-1)
	for {
		page, err := a.ReadPage(ReadOptions{BeforeOffset: offset, Limit: 3})
		require.NoError(t, err)
		all = append(all, page.Entries...)
		if page.NextBeforeOffset < 0 {
			break
		}
		offset = page.NextBeforeOffset
	}

	assert.Len(t, all, total)
	// Verify order: newest (index 9) to oldest (index 0)
	assert.Equal(t, "/file0009.txt", all[0]["resource"])
	assert.Equal(t, "/file0000.txt", all[total-1]["resource"])

	// Verify no duplicates
	seen := map[string]bool{}
	for _, e := range all {
		r := e["resource"].(string)
		assert.False(t, seen[r], "duplicate entry: %s", r)
		seen[r] = true
	}
}

func TestReadPage_Filter(t *testing.T) {
	entries := []map[string]any{
		makeEntry(0, "FILE_UPLOAD"),
		makeEntry(1, "FILE_DELETE"),
		makeEntry(2, "FILE_UPLOAD"),
		makeEntry(3, "FILE_DELETE"),
		makeEntry(4, "FILE_UPLOAD"),
	}
	a := writeEntries(t, entries)

	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10, Filter: "FILE_DELETE"})
	require.NoError(t, err)
	require.Len(t, page.Entries, 2)
	for _, e := range page.Entries {
		assert.Equal(t, "FILE_DELETE", e["action"])
	}
}

func TestReadPage_FilterCaseInsensitive(t *testing.T) {
	a := writeEntries(t, []map[string]any{makeEntry(0, "FILE_UPLOAD")})
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10, Filter: "file_upload"})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
}

func TestReadPage_FilterNoMatch(t *testing.T) {
	a := writeEntries(t, []map[string]any{makeEntry(0, "FILE_UPLOAD"), makeEntry(1, "FILE_UPLOAD")})
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10, Filter: "DOES_NOT_EXIST"})
	require.NoError(t, err)
	assert.Empty(t, page.Entries)
	assert.Equal(t, int64(-1), page.NextBeforeOffset)
}

func TestReadPage_ScanLimit(t *testing.T) {
	// Write 20 entries (~150 bytes each = ~3KB total), cap scan at 200 bytes.
	// Since the file fits in one 32KB chunk, the budget check fires before any
	// entries are read, so we expect 0 entries and ScanLimitHit = true.
	entries := make([]map[string]any, 20)
	for i := range entries {
		entries[i] = makeEntry(i, "FILE_UPLOAD")
	}
	a := writeEntries(t, entries)

	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 100, MaxScanBytes: 200})
	require.NoError(t, err)
	assert.True(t, page.ScanLimitHit)
	assert.Less(t, len(page.Entries), 20)
}

func TestReadPage_LimitClamp(t *testing.T) {
	entries := make([]map[string]any, 5)
	for i := range entries {
		entries[i] = makeEntry(i, "FILE_UPLOAD")
	}
	a := writeEntries(t, entries)

	// maxLimit = 200; requesting 999 should still return at most 5 (all entries)
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 999})
	require.NoError(t, err)
	assert.Len(t, page.Entries, 5)
}

func TestReadPage_ChunkBoundaryCrossing(t *testing.T) {
	// Write enough entries to span multiple 32KB chunks (~300 entries × ~150B = ~45KB)
	const total = 300
	entries := make([]map[string]any, total)
	for i := range entries {
		entries[i] = makeEntry(i, "FILE_UPLOAD")
	}
	a := writeEntries(t, entries)

	var all []map[string]any
	offset := int64(-1)
	for {
		page, err := a.ReadPage(ReadOptions{BeforeOffset: offset, Limit: 50})
		require.NoError(t, err)
		all = append(all, page.Entries...)
		if page.NextBeforeOffset < 0 {
			break
		}
		offset = page.NextBeforeOffset
	}

	assert.Len(t, all, total)

	// No duplicates
	seen := map[string]bool{}
	for _, e := range all {
		r := e["resource"].(string)
		assert.False(t, seen[r], "duplicate: %s", r)
		seen[r] = true
	}

	// Correct order: newest first
	assert.Equal(t, fmt.Sprintf("/file%04d.txt", total-1), all[0]["resource"])
	assert.Equal(t, "/file0000.txt", all[total-1]["resource"])
}

func TestReadPage_SkipsInvalidLines(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "audit*.log")
	require.NoError(t, err)

	// Mix valid and invalid JSON lines
	lines := []string{
		`{"action":"FILE_UPLOAD","resource":"/a.txt","status":"SUCCESS"}`,
		`not valid json at all`,
		`{"action":"FILE_DELETE","resource":"/b.txt","status":"SUCCESS"}`,
		``,
		`{"action":"DIR_CREATE","resource":"/dir","status":"SUCCESS"}`,
	}
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
	require.NoError(t, f.Close())

	a := newAuditorFromFile(f.Name())
	page, err := a.ReadPage(ReadOptions{BeforeOffset: -1, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, page.Entries, 3)
}
