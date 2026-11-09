package state

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// replaceFile renames tmp over dst. Windows refuses that while another process has dst open, and
// `net-merge limit` opens limits.json every 50 ms while it waits for the running instance to save
// it, so wait a moment for the reader to finish instead of failing.
func replaceFile(tmp, dst string) error {
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(tmp, dst)
		if err == nil || time.Now().After(deadline) ||
			!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
