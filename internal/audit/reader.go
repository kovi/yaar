package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

const (
	defaultMaxScanBytes = 50 * 1024 * 1024
	defaultLimit        = 50
	maxLimit            = 200
	chunkSize           = 32 * 1024
)

type ReadOptions struct {
	BeforeOffset int64  // scan backward from here; -1 = start from EOF
	Limit        int    // max entries; default 50, max 200
	Filter       string // case-insensitive substring match on raw JSON line
	MaxScanBytes int64  // 0 = 50 MB default
	// Generation selects which file to read: 0 is the live audit.log, 1 is
	// audit.log.1 and so on. Paging walks forward through generations as each is
	// exhausted, so rotation does not make older entries unreachable.
	Generation int
}

type AuditPage struct {
	Entries          []map[string]any `json:"entries"`
	NextBeforeOffset int64            `json:"next_before_offset"` // -1 = no more entries
	ScanLimitHit     bool             `json:"scan_limit_hit"`
	// NextGeneration is the file the next page continues in. It differs from the
	// requested generation once the current file is exhausted and an older
	// rotated one is available. Clients must echo both this and
	// NextBeforeOffset back to page correctly across a rotation.
	NextGeneration int `json:"next_generation"`
	// HasMore reports whether another page exists. NextBeforeOffset alone cannot
	// answer this once rotation is in play: -1 means "from the end of the file",
	// which is exactly how a continuation into an older generation starts.
	HasMore bool `json:"has_more"`
}

// generationPath resolves a generation number to a file: 0 is the live log, n>0
// is the nth rotated backup.
func (a *Auditor) generationPath(n int) string {
	if n <= 0 {
		return a.filePath
	}
	return backupPath(a.filePath, n)
}

// generations returns every readable generation in read order, newest first,
// starting with the live file.
func (a *Auditor) generations() []string {
	out := []string{a.filePath}

	// An Auditor built only for reading (as the API handler's may be) has no
	// configured backup count, so fall back to discovering them on disk.
	if a.maxBackups > 0 {
		out = append(out, backupGenerations(a.filePath, a.maxBackups)...)
	} else {
		out = append(out, discoverBackups(a.filePath)...)
	}
	return out
}

// ReadPage reads up to opts.Limit audit entries backward from opts.BeforeOffset.
// Entries are returned newest-first.
//
// Reading continues into rotated generations: when the requested file is
// exhausted before the limit is reached, the scan rolls over to the next older
// file. Without that, everything written before the most recent rotation would
// become unreachable through the API the moment the live file was renamed.
func (a *Auditor) ReadPage(opts ReadOptions) (*AuditPage, error) {
	if opts.Limit <= 0 {
		opts.Limit = defaultLimit
	}
	if opts.Limit > maxLimit {
		opts.Limit = maxLimit
	}
	if opts.MaxScanBytes <= 0 {
		opts.MaxScanBytes = defaultMaxScanBytes
	}
	if opts.Generation < 0 {
		opts.Generation = 0
	}

	page := &AuditPage{
		Entries:          []map[string]any{},
		NextBeforeOffset: -1,
		NextGeneration:   opts.Generation,
	}

	gen := opts.Generation
	before := opts.BeforeOffset
	remaining := opts.MaxScanBytes

	// Roll forward through generations until the limit is filled, the scan
	// budget is spent, or there are no older files left.
	for {
		// Only the offset, remaining limit and remaining budget vary per generation.
		gopts := opts
		gopts.BeforeOffset = before
		gopts.Limit = opts.Limit - len(page.Entries)
		gopts.MaxScanBytes = remaining

		res, err := a.readPageFile(a.generationPath(gen), gopts)
		if err != nil {
			return nil, err
		}

		page.Entries = append(page.Entries, res.Entries...)
		// Sticky: a budget exhausted in an earlier generation still means the
		// caller is seeing a truncated view.
		page.ScanLimitHit = page.ScanLimitHit || res.ScanLimitHit
		remaining -= res.scanned

		// This file still holds unread entries — either it stopped mid-file with
		// a cursor, or it stopped early with the page full. Stay in it.
		if !res.exhausted {
			page.NextBeforeOffset = res.NextBeforeOffset
			page.NextGeneration = gen
			page.HasMore = true
			return page, nil
		}

		// The file is fully read. Continue in the next older generation.
		next, hasNext := a.nextGeneration(gen)
		if !hasNext {
			page.NextBeforeOffset = -1
			page.NextGeneration = gen
			page.HasMore = false
			return page, nil
		}

		// The next page resumes at the end of the older file. Hand the cursor
		// back now if this page is already full or the budget is spent;
		// otherwise keep filling from that file in this same request.
		gen = next
		before = -1

		if len(page.Entries) >= opts.Limit || page.ScanLimitHit || remaining <= 0 {
			page.NextBeforeOffset = -1
			page.NextGeneration = gen
			page.HasMore = true
			return page, nil
		}
	}
}

// nextGeneration returns the generation to continue in after gen is exhausted.
func (a *Auditor) nextGeneration(gen int) (int, bool) {
	gens := a.generations()
	// gens[i] is generation i, so the next one exists when it is in range.
	if gen+1 < len(gens) {
		return gen + 1, true
	}
	return 0, false
}

// filePage is one generation's worth of results, plus the bytes it consumed from
// the caller's scan budget.
type filePage struct {
	Entries          []map[string]any
	NextBeforeOffset int64
	ScanLimitHit     bool
	scanned          int64
	// exhausted distinguishes "read to the start of the file" from "stopped
	// early because the limit filled". Both leave NextBeforeOffset at -1 when no
	// entry was returned, so the caller cannot tell them apart otherwise — and
	// rolling to the next generation on the second case would skip entries.
	exhausted bool
}

// readPageFile scans a single file backward. It is the original single-file
// ReadPage logic, with the byte count reported so the caller can carry one scan
// budget across generations.
func (a *Auditor) readPageFile(path string, opts ReadOptions) (*filePage, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &filePage{Entries: []map[string]any{}, NextBeforeOffset: -1, exhausted: true}, nil
		}
		return nil, err
	}
	defer f.Close()

	startPos := opts.BeforeOffset
	if startPos < 0 {
		startPos, err = f.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, err
		}
	}

	filterLower := strings.ToLower(opts.Filter)
	entries := []map[string]any{}
	nextBeforeOffset := int64(-1)
	scanLimitHit := false
	pos := startPos
	// remainder holds the leading bytes of a line whose \n was in the previous (later) chunk
	remainder := []byte{}
	var scanned int64

outer:
	for pos > 0 && len(entries) < opts.Limit {
		readSize := int64(chunkSize)
		if readSize > pos {
			readSize = pos
		}

		// combined byte count for scan budget
		combinedSize := readSize + int64(len(remainder))
		if scanned+combinedSize > opts.MaxScanBytes {
			scanLimitHit = true
			break
		}

		readStart := pos - readSize
		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, readStart); err != nil && err != io.EOF {
			return nil, err
		}

		// combined[j] maps to file offset readStart+j (proof: readStart+j for j<len(buf),
		// and for j>=len(buf): pos+(j-len(buf)) = readStart+len(buf)+(j-len(buf)) = readStart+j)
		combined := append(buf, remainder...)

		lines := bytes.Split(combined, []byte{'\n'})
		processFrom := 0

		if readStart > 0 {
			// lines[0] is a partial line — its start is before readStart
			remainder = clone(lines[0])
			processFrom = 1
		} else {
			remainder = nil
		}

		// Process lines newest-first (reverse within this chunk)
		for i := len(lines) - 1; i >= processFrom; i-- {
			line := bytes.TrimSpace(lines[i])
			if len(line) == 0 {
				continue
			}

			if filterLower != "" && !bytes.Contains(bytes.ToLower(line), []byte(filterLower)) {
				continue
			}

			var entry map[string]any
			if err := json.Unmarshal(line, &entry); err != nil {
				continue
			}

			lineStart := readStart + int64(lineStartOffset(lines, i))
			entries = append(entries, entry)
			nextBeforeOffset = lineStart // tracks the oldest entry seen so far

			if len(entries) >= opts.Limit {
				break outer
			}
		}

		scanned += combinedSize
		pos = readStart
	}

	// We've consumed the whole file — no more pages in this generation
	exhausted := pos == 0 && !scanLimitHit
	if exhausted {
		nextBeforeOffset = -1
	}

	return &filePage{
		Entries:          entries,
		NextBeforeOffset: nextBeforeOffset,
		ScanLimitHit:     scanLimitHit,
		scanned:          scanned,
		exhausted:        exhausted,
	}, nil
}

// lineStartOffset returns the byte offset of lines[idx] within the combined slice.
func lineStartOffset(lines [][]byte, idx int) int {
	offset := 0
	for i := 0; i < idx; i++ {
		offset += len(lines[i]) + 1 // +1 for the \n separator
	}
	return offset
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
