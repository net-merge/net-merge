//go:build unix

package state

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestAliasRequestRejectsSymlinksAndFIFOs(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := Prepare(dir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "ctl", "alias")
			var err error
			if kind == "symlink" {
				err = os.Symlink("/dev/zero", path)
			} else {
				err = syscall.Mkfifo(path, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan bool, 1)
			go func() { _, ok := TakeAlias(dir); done <- ok }()
			select {
			case ok := <-done:
				if ok {
					t.Fatal("accepted an unsafe alias request")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("unsafe request blocked the menu loop")
			}
		})
	}
}
