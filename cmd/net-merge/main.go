package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"net-merge/internal/engine"
	"net-merge/internal/icon"
	"net-merge/internal/logx"
	"net-merge/internal/nics"
	"net-merge/internal/platform"
	"net-merge/internal/state"
	"net-merge/internal/tun"
)

const usage = `net-merge: use all your network adapters at once.

  net-merge tray       tray icon: its menu turns system-wide ON/OFF and shows speeds
  net-merge run        system-wide ON in a console window (asks for admin/root); Ctrl+C or "off" turns it OFF
  net-merge on|off     turn system-wide mode on/off in a running tray (off also works for run)
  net-merge quit       turn it off and exit the tray
  net-merge status     show whether it is on, and per-adapter load
  net-merge limit <adapter> <Mbit/s>   cap an adapter (0 or unlimited removes the cap)
  net-merge limit      list the saved speed limits
  net-merge alias <adapter> [name]   set a display name (omit name to remove it)
  net-merge isp-names on|off   show each connection's provider in the tray (on by default)
  net-merge socks      plain SOCKS5 proxy on 127.0.0.1:1080 (no admin, per-app)
  net-merge list       show detected adapters
  net-merge autostart on|off|status   start the tray at sign-in (Windows: no UAC prompt)
  net-merge cleanup    remove leftover routes after a crash (admin/root)
  net-merge install    macOS: sudo net-merge install lets the tray start without a password
  net-merge version

Flags for run/socks: -include "WiFi,Ethernet 3"  -weight "Name=3"  -auto-weight=false  -stats 5s
Phone hotspots: -phone-cooling (slow a phone down after long heavy use)  -phone-cool 20  -phone "Name"
`

type weightFlags map[string]float64

func (w weightFlags) String() string { return "" }
func (w weightFlags) Set(v string) error {
	i := strings.LastIndex(v, "=")
	if i < 1 {
		return fmt.Errorf(`want "Name=3", got %q`, v)
	}
	n, err := strconv.ParseFloat(v[i+1:], 64)
	if err != nil || n <= 0 {
		return fmt.Errorf("weight must be > 0 in %q", v)
	}
	w[v[:i]] = n
	return nil
}

type common struct {
	include  string
	weights  weightFlags
	rescan   time.Duration
	stats    time.Duration
	auto     bool
	logFile  string
	stateDir string
	ipv6     string
	mtu      int
	pprof    string
	phones   string
	cooling  optBool
	cool     float64
}

// optBool is a bool flag that remembers whether it was given, so a saved choice applies unless
// the command line says otherwise.
type optBool struct{ set, val bool }

func (b *optBool) String() string   { return strconv.FormatBool(b.val) }
func (b *optBool) IsBoolFlag() bool { return true }
func (b *optBool) Set(v string) error {
	val, err := strconv.ParseBool(v)
	if err != nil {
		return err
	}
	b.set, b.val = true, val
	return nil
}

// or is the flag's value if it was given, else def.
func (b *optBool) or(def bool) bool {
	if b.set {
		return b.val
	}
	return def
}

func (c *common) register(fs *flag.FlagSet) {
	c.weights = weightFlags{}
	fs.StringVar(&c.include, "include", "", "comma-separated adapter names to use (default: all non-virtual)")
	fs.Var(c.weights, "weight", `fixed adapter weight, repeatable: -weight "Wi-Fi=3"`)
	fs.DurationVar(&c.rescan, "rescan", 3*time.Second, "adapter re-scan interval")
	fs.DurationVar(&c.stats, "stats", 5*time.Second, "stats print interval (0 = off)")
	fs.BoolVar(&c.auto, "auto-weight", true, "weight adapters by measured throughput (off when -weight is given)")
	fs.StringVar(&c.ipv6, "ipv6", "auto", "auto: IPv6 only if every adapter has it (else apps use IPv4 on all adapters); on: always; off: never")
	fs.IntVar(&c.mtu, "mtu", tun.DefaultMTU, "MTU of the virtual adapter (1280-65535); larger means fewer packets and less CPU")
	fs.Var(&c.cooling, "phone-cooling", "hold a phone hotspot to -phone-cool after 3 minutes of heavy use, until it has had a rest (default: the tray's saved choice, off unless ticked)")
	fs.Float64Var(&c.cool, "phone-cool", engine.DefaultCoolMbps, "Mbit/s a phone hotspot is held to by -phone-cooling (0 = never held back)")
	fs.StringVar(&c.phones, "phone", "", "comma-separated adapters to treat as phone hotspots, besides the iPhones net-merge recognises by itself")
	fs.StringVar(&c.pprof, "pprof", "", "serve Go's profiler on this loopback address, e.g. 127.0.0.1:6060")
	fs.StringVar(&c.logFile, "log", "", "write output to this file instead of the console")
	fs.StringVar(&c.stateDir, "state-dir", state.DefaultDir(), "where status/off marker files live")
}

func (c *common) nicsDetailed() ([]nics.Nic, []nics.Skip, error) {
	found, skipped, err := nics.DiscoverNicsDetailed(splitNames(c.include), c.weights)
	markPhones(found, splitNames(c.phones))
	return found, skipped, err
}

func splitNames(list string) (names []string) {
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			names = append(names, s)
		}
	}
	return names
}

// markPhones adds the adapters named with -phone to the phones net-merge found by itself.
func markPhones(found []nics.Nic, phones []string) {
	for i := range found {
		for _, name := range phones {
			if strings.EqualFold(name, found[i].Name) {
				found[i].Phone = true
			}
		}
	}
}

func (c *common) nics() ([]nics.Nic, error) {
	nics, _, err := c.nicsDetailed()
	return nics, err
}

// setup opens the log file (if any) and the state dir.
func (c *common) setup() error {
	// Without admin (`socks`) there may be no state dir to make, e.g. the root-owned one on macOS.
	if err := state.Prepare(c.stateDir); err != nil && platform.IsAdmin() {
		return err
	}
	if c.logFile != "" {
		f, err := os.OpenFile(c.logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		logx.SetOutput(f)
		platform.RedirectStd(f)
	}
	return nil
}

// boot discovers adapters and starts the background loops: re-scan, 1s throughput sampling
// (which also drives auto-weights and the status file), and periodic stats printing.
func (c *common) boot(mode func() string) (*engine.Scheduler, *engine.Balancer, error) {
	if c.mtu < 1280 || c.mtu > 65535 {
		return nil, nil, fmt.Errorf("-mtu must be between 1280 and 65535, got %d", c.mtu)
	}
	if c.pprof != "" {
		if err := startPprof(c.pprof); err != nil {
			return nil, nil, err
		}
	}
	nics, err := c.nics()
	if err != nil {
		return nil, nil, err
	}
	if len(nics) == 0 {
		return nil, nil, fmt.Errorf("no usable IPv4 adapters found (try: net-merge list)")
	}
	if c.cool < 0 || c.cool > engine.LimitMaxMbps {
		return nil, nil, fmt.Errorf("-phone-cool must be between 0 and %.0f Mbit/s, got %v", float64(engine.LimitMaxMbps), c.cool)
	}
	sched := engine.NewScheduler(nics)
	sched.SetAuto(c.auto && len(c.weights) == 0)
	settings, err := state.LoadSettings(c.stateDir)
	if err != nil {
		logx.Printf("warning: %v (ignored)\n", err) // a bad settings file must not stop merging
	}
	sched.SetPhoneCooling(c.cooling.or(settings.PhoneCooling))
	sched.SetCoolRate(c.cool)
	sched.SetGameMode(settings.GameMode)
	if limits, err := state.LoadLimits(c.stateDir); err != nil {
		logx.Printf("warning: %v (ignored)\n", err) // a bad limits file must not stop merging
	} else {
		sched.SetLimits(limits)
	}
	for _, n := range nics {
		logx.Printf("  + %s\n", describeNic(n))
	}
	if len(nics) == 1 {
		logx.Printf("  note: only one adapter found, nothing to aggregate yet\n")
	}

	bal := engine.NewBalancer(sched)
	v6, ok := engine.ParseV6Mode(c.ipv6)
	if !ok {
		return nil, nil, fmt.Errorf(`-ipv6 must be auto, on or off, got %q`, c.ipv6)
	}
	bal.SetV6Mode(v6)

	since := time.Now()
	go sched.RunProbes(nil)
	go func() {
		for range time.Tick(c.rescan) {
			if ns, err := c.nics(); err == nil {
				sched.Update(ns)
			}
		}
	}()
	go func() {
		last := time.Now()
		for now := range time.Tick(time.Second) {
			sched.Tick(now.Sub(last))
			last = now
			if mode != nil {
				if m := mode(); m != "" {
					state.Write(c.stateDir, state.Status{PID: os.Getpid(), Mode: m, V6: bal.V6State(), Since: since, Updated: now, NICs: sched.Snapshot()})
				}
			}
		}
	}()
	if c.stats > 0 {
		go func() {
			for range time.Tick(c.stats) {
				logx.Printf("--- %s\n%s", time.Now().Format("15:04:05"), formatNICs(sched.Snapshot()))
			}
		}()
	}
	return sched, bal, nil
}

func describeNic(n nics.Nic, aliases ...map[string]string) string {
	s := n.Name
	if len(aliases) != 0 {
		s = state.AdapterLabel(s, aliases[0])
	}
	if n.IP != nil {
		s += "  v4 " + n.IP.String()
	}
	if n.IP6 != nil {
		s += "  v6 " + n.IP6.String()
	}
	if n.Phone {
		s += "  (phone hotspot)"
	}
	return s
}

func formatNICs(snap []engine.SlotStats, aliases ...map[string]string) string {
	var sb strings.Builder
	for _, s := range snap {
		gone := ""
		if !s.Present {
			gone = " (gone)"
		}
		// "link" is the adapter's total traffic by any app (Windows' counters); "via net-merge" is the
		// part net-merge relayed. Apps that bind to adapters directly (qBittorrent) only show in "link".
		lim := ""
		if s.LimitMbps > 0 {
			lim = "  limit " + limitPhrase(s.LimitMbps)
		}
		if s.Phone {
			lim += "  phone"
		}
		if s.CoolMbps > 0 {
			lim += "  cooling at " + limitPhrase(s.CoolMbps)
		}
		name := s.Name
		if len(aliases) != 0 {
			name = state.AdapterLabel(name, aliases[0])
		}
		fmt.Fprintf(&sb, "%-14s v4 %-11s v6 %-11s link down %6.1f up %5.1f Mbit/s  via net-merge %6.1f  active %3d  total %5d  w %.2f/%.2f  failed %d%s%s\n",
			name, s.State, s.State6, s.LinkIn*8/1e6, s.LinkOut*8/1e6, (s.RateIn+s.RateOut)*8/1e6, s.Active, s.Total, s.Weight, s.Weight6, s.Failed, lim, gone)
	}
	return sb.String()
}

// Set at build time: -X main.version=... and, for the windowless tray build, -X main.defaultCmd=tray.
var (
	version    = "dev"
	defaultCmd = ""
)

func main() {
	var cmd string
	var args []string
	switch {
	case len(os.Args) >= 2:
		cmd, args = os.Args[1], os.Args[2:]
	case defaultCmd != "":
		cmd = defaultCmd
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch cmd {
	case "version":
		fmt.Println("net-merge", version)
	case "install", "uninstall":
		if err := platform.Install(cmd == "uninstall"); err != nil {
			die(err)
		}
	case "autostart":
		if len(args) != 1 || (args[0] != "on" && args[0] != "off" && args[0] != "status") {
			fmt.Fprintln(os.Stderr, "usage: net-merge autostart on|off|status")
			os.Exit(2)
		}
		if err := platform.Autostart(args[0]); err != nil {
			die(err)
		}
	case "_icon": // build helper: writes the tray icon as an .ico
		if len(args) != 1 {
			os.Exit(2)
		}
		if err := os.WriteFile(args[0], icon.ICO(true), 0o644); err != nil {
			die(err)
		}
	case "tray":
		cmdTray(args)
	case "run":
		cmdRun(args)
	case "on", "off", "quit":
		cmdControl(cmd, args)
	case "status":
		cmdStatus(args)
	case "limit":
		cmdLimit(args)
	case "alias":
		cmdAlias(args)
	case "isp-names":
		cmdISPNames(args)
	case "socks":
		cmdSocks(args)
	case "list":
		cmdList(args)
	case "cleanup":
		cmdCleanup()
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	if !platform.HasConsole() {
		platform.ShowError(err.Error())
	}
	os.Exit(1)
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var c common
	c.register(fs)
	fs.Parse(args)
	nics, skipped, err := c.nicsDetailed()
	if err != nil {
		die(err)
	}
	if len(nics) == 0 {
		fmt.Println("No usable adapters found.")
	}
	prefs, _ := state.LoadTrayPreferences(c.stateDir)
	for _, n := range nics {
		fmt.Printf("USE   %s  (index %d)\n", describeNic(n, prefs.Aliases), n.Index)
	}
	for _, s := range skipped {
		fmt.Printf("skip  %s: %s\n", state.AdapterLabel(s.Name, prefs.Aliases), s.Reason)
	}
}

func cmdSocks(args []string) {
	fs := flag.NewFlagSet("socks", flag.ExitOnError)
	var c common
	c.register(fs)
	listen := fs.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	fs.Parse(args)
	if err := c.setup(); err != nil {
		die(err)
	}
	_, bal, err := c.boot(nil)
	if err != nil {
		die(err)
	}
	ln, err := net.Listen("tcp4", *listen)
	if err != nil {
		die(err)
	}
	logx.Printf("net-merge SOCKS5 listening on %s\n", *listen)
	die(engine.ServeSocks5(ln, bal))
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var c common
	c.register(fs)
	hidden := fs.Bool("hidden", false, "when elevating, hide the new console window")
	crashAfter := fs.Duration("crash-after", 0, "test only: exit abruptly (no cleanup) after this long")
	runFor := fs.Duration("for", 0, "turn off automatically after this long (0 = until stopped)")
	fs.Parse(args)

	if !platform.IsAdmin() {
		// Re-run elevated, handing over where to log and keep state.
		child := append([]string{"run"}, args...)
		child = append(child, "-state-dir", c.stateDir)
		state.Prepare(c.stateDir) // as this user, so `net-merge off` can still drop markers in it
		if err := platform.RelaunchElevated(child, *hidden); err != nil {
			die(fmt.Errorf("could not start elevated: %w", err))
		}
		fmt.Println("Approve the UAC prompt. System-wide mode runs in the new window; `net-merge off` turns it off.")
		return
	}
	if !platform.SingleInstance() {
		fmt.Println("net-merge is already running (check the system tray, or run `net-merge off`).")
		return
	}
	if err := c.setup(); err != nil {
		die(err)
	}
	if err := platform.CheckDriver(); err != nil {
		logx.Printf("error: %v\n", err)
		os.Exit(1)
	}
	state.TakeMarker(c.stateDir, "off") // drop a stale marker

	sched, bal, err := c.boot(func() string { return "system-wide" })
	if err != nil {
		logx.Printf("error: %v\n", err)
		os.Exit(1)
	}
	t := tun.New(bal)
	t.MTU = c.mtu
	if err := t.On(); err != nil {
		logx.Printf("error: %v\n", err)
		os.Exit(1)
	}
	logx.Printf("net-merge is ON: all IPv4 traffic is balanced across the adapters above. Ctrl+C or `net-merge off` to stop.\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	var crash, timeUp <-chan time.Time
	if *crashAfter > 0 {
		crash = time.After(*crashAfter)
	}
	if *runFor > 0 {
		timeUp = time.After(*runFor)
	}
	poll := time.NewTicker(300 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-sig:
		case <-timeUp:
		case <-crash:
			os.Exit(3) // simulates a crash: no teardown runs
		case <-poll.C:
			if req, ok := state.TakeAlias(c.stateDir); ok {
				if err := state.SetAlias(c.stateDir, req.Adapter, req.Alias); err != nil {
					logx.Printf("warning: saving alias: %v\n", err)
				}
			}
			if req, ok := state.TakeLimit(c.stateDir); ok {
				sched.SetLimit(req.Adapter, req.Mbps)
				if err := state.SaveLimits(c.stateDir, sched.Limits()); err != nil {
					logx.Printf("warning: saving speed limits: %v\n", err)
				}
			}
			if !state.TakeMarker(c.stateDir, "off") && !state.TakeMarker(c.stateDir, "quit") {
				continue
			}
		}
		break
	}
	t.Close()
	state.Clear(c.stateDir)
	logx.Printf("net-merge is OFF.\n")
}

// cmdControl implements `on`, `off` and `quit` against a running tray/run instance.
func cmdControl(name string, args []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	if !platform.InstanceRunning() {
		if name == "off" || name == "quit" {
			fmt.Println("net-merge is not running (already off).")
			return
		}
		die(fmt.Errorf("net-merge is not running; start it with `net-merge tray`"))
	}
	_, wasOn := state.Read(*dir)
	if err := state.SendMarker(*dir, name); err != nil {
		die(err)
	}
	want := name == "on"
	for i := 0; i < 60; i++ {
		time.Sleep(150 * time.Millisecond)
		_, on := state.Read(*dir)
		switch {
		case name == "on" && on == want:
			fmt.Println("net-merge is ON.")
			return
		case name != "on" && !on:
			fmt.Println("net-merge is OFF.")
			return
		}
	}
	_ = wasOn
	die(fmt.Errorf("sent %q but it did not take effect (does this instance support it? run: net-merge tray)", name))
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	st, on := state.Read(*dir)
	if !on {
		fmt.Println("net-merge is OFF.")
		return
	}
	prefs, _ := state.LoadTrayPreferences(*dir)
	fmt.Printf("net-merge is ON (%s, pid %d, up %s)\nIPv6: %s\n%s", st.Mode, st.PID, time.Since(st.Since).Truncate(time.Second), st.V6, formatNICs(st.NICs, prefs.Aliases))
}

// cmdLimit lists the saved caps, or sets one. A running instance gets the change through a marker
// file and saves it; with nothing running, limits.json is updated directly so the next start picks
// it up.
func cmdLimit(args []string) {
	fs := flag.NewFlagSet("limit", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	rest := fs.Args()

	if len(rest) == 0 {
		limits, err := state.LoadLimits(*dir)
		if err != nil {
			die(err)
		}
		if len(limits) == 0 {
			fmt.Println("No speed limits set.")
			return
		}
		names := make([]string, 0, len(limits))
		for name := range limits {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("%-14s %s\n", name, limitPhrase(limits[name]))
		}
		return
	}
	if len(rest) != 2 {
		fmt.Fprintln(os.Stderr, "usage: net-merge limit <adapter> <Mbit/s>   (0, off or unlimited removes the cap)")
		os.Exit(2)
	}
	name := strings.TrimSpace(rest[0])
	mbitPerSec, err := engine.ParseLimit(rest[1])
	if err != nil {
		die(err)
	}
	live, err := limitApply(*dir, name, mbitPerSec, platform.InstanceRunning(), limitWait)
	if err != nil {
		die(err)
	}
	if live {
		fmt.Printf("%s: %s.\n", name, limitPhrase(mbitPerSec))
	} else {
		fmt.Printf("%s: %s. It takes effect the next time net-merge starts.\n", name, limitPhrase(mbitPerSec))
	}
}

// limitWait is how long `net-merge limit` waits for a running instance to apply and save the cap.
var limitWait = 4 * time.Second

// limitApply saves a cap. With a running instance the request goes through ctl/ and the instance
// applies it and saves limits.json; with nothing running this writes limits.json itself, so the
// next start picks it up. It reports whether a running instance applied it.
func limitApply(dir, name string, mbitPerSec float64, running bool, wait time.Duration) (live bool, err error) {
	if !running {
		limits, err := state.LoadLimits(dir)
		if err != nil {
			return false, err
		}
		if mbitPerSec <= 0 {
			delete(limits, name)
		} else {
			limits[name] = mbitPerSec
		}
		return false, state.SaveLimits(dir, limits)
	}
	if err := state.SendLimit(dir, state.LimitRequest{Adapter: name, Mbps: mbitPerSec}); err != nil {
		return true, err
	}
	// The instance applies it within a second and saves limits.json; wait for that as proof.
	deadline := time.Now().Add(wait)
	for {
		if limits, err := state.LoadLimits(dir); err == nil {
			got, set := limits[name]
			if (mbitPerSec <= 0 && !set) || (set && got == mbitPerSec) {
				return true, nil
			}
		}
		if time.Now().After(deadline) {
			return true, fmt.Errorf("sent the limit, but the running instance did not apply it")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func limitPhrase(mbitPerSec float64) string {
	if mbitPerSec <= 0 {
		return "unlimited"
	}
	return strconv.FormatFloat(mbitPerSec, 'f', -1, 64) + " Mbit/s"
}

func cmdCleanup() {
	if !platform.IsAdmin() {
		if err := platform.RelaunchElevated([]string{"cleanup"}, false); err != nil {
			die(err)
		}
		return
	}
	tun.CleanupStale()
	fmt.Println("done.")
}
