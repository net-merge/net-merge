package main

import (
	"flag"

	"net-merge/internal/state"
	"net-merge/internal/tray"
)

const defaultTrayPaused = true

func cmdPreview(args []string) {
	fs := flag.NewFlagSet("_preview", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "status directory to preview")
	fs.Parse(args)
	if err := tray.PreviewWindows(*dir, version); err != nil {
		die(err)
	}
}
