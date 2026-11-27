package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"net-merge/internal/platform"
	"net-merge/internal/state"
)

func cmdAlias(args []string) {
	fs := flag.NewFlagSet("alias", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 || len(rest) > 2 {
		fmt.Fprintln(os.Stderr, "usage: net-merge alias <adapter> [display name]   (omit the display name to remove it)")
		os.Exit(2)
	}
	name, alias := strings.TrimSpace(rest[0]), ""
	if len(rest) == 2 {
		alias = strings.TrimSpace(rest[1])
	}
	if err := aliasApply(*dir, name, alias, platform.InstanceRunning(), 4*time.Second); err != nil {
		die(err)
	}
	if alias == "" {
		fmt.Printf("%s: display name removed.\n", name)
	} else {
		fmt.Printf("%s: display name is %s.\n", name, alias)
	}
}

func aliasApply(dir, name, alias string, running bool, wait time.Duration) error {
	if err := state.ValidateAlias(name, alias); err != nil {
		return err
	}
	if !running {
		return state.SetAlias(dir, name, alias)
	}
	if err := state.SendAlias(dir, state.AliasRequest{Adapter: name, Alias: alias}); err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	for {
		if p, err := state.LoadTrayPreferences(dir); err == nil && p.Aliases[name] == alias {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sent the alias, but the running instance did not save it (it may need updating)")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
