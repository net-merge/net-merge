//go:build !windows

package state

import "os"

// replaceFile renames tmp over dst.
func replaceFile(tmp, dst string) error { return os.Rename(tmp, dst) }
