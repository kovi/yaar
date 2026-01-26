package models

import "fmt"

type BatchDownloadMode string

const (
	BatchModeLiteral BatchDownloadMode = "literal" // Preserves the selected folder name
	BatchModeMerge   BatchDownloadMode = "merge"   // Flattens selected folders into the root
)

// IsValid checks if the mode is one of the supported constants
func (m BatchDownloadMode) IsValid() bool {
	return m == BatchModeLiteral || m == BatchModeMerge
}

// ParseBatchMode returns the enum or an error if the string is invalid
func ParseBatchMode(s string) (BatchDownloadMode, error) {
	mode := BatchDownloadMode(s)
	if mode.IsValid() {
		return mode, nil
	}
	return "", fmt.Errorf("invalid batch mode: %q. Supported: 'literal', 'merge'", s)
}
