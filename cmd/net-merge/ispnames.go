package main

import (
	"flag"
	"fmt"
	"os"

	"net-merge/internal/state"
)

// cmdISPNames saves the tray's Show ISP Names choice without the tray, for the Windows installer's
// tick box. A tray that is already running picks it up at its next start.
func cmdISPNames(args []string) {
	fs := flag.NewFlagSet("isp-names", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	if fs.NArg() != 1 || (fs.Arg(0) != "on" && fs.Arg(0) != "off") {
		fmt.Fprintln(os.Stderr, "usage: net-merge isp-names on|off")
		os.Exit(2)
	}
	if err := setISPNames(*dir, fs.Arg(0) == "on"); err != nil {
		die(err)
	}
	fmt.Printf("Show ISP Names is %s from the tray's next start.\n", fs.Arg(0))
}

func setISPNames(dir string, on bool) error {
	p, err := state.LoadTrayPreferences(dir)
	if err != nil {
		return err // a file we can't read is not overwritten
	}
	p.LookupISP = on
	return state.SaveTrayPreferences(dir, p)
}
