// Package tun is the system-wide front-end: a virtual adapter whose routes capture every flow,
// terminated in a userland TCP/IP stack and re-dialed through the Balancer.
package tun

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/core/option"
	tlog "github.com/xjasonlyu/tun2socks/v2/log"
	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy/reject"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"

	"net-merge/internal/engine"
	"net-merge/internal/logx"
)

const (
	tunName = "net-merge"
	// A ULA on purpose. The OS's source-address policy (RFC 6724, on Windows and in glibc) prefers
	// IPv4 over IPv6 when the only IPv6 source on the route is a ULA, so dual-stack sites go over
	// IPv4 and spread across every adapter, while IPv6-only sites (and explicit IPv6) still work
	// through the IPv6 path. A global-scope address here makes the OS prefer IPv6 everywhere, which
	// funnels all traffic onto whichever adapters happen to have IPv6. Nothing is ever sent from
	// this address.
	tunIP6 = "fd6e:6d65:7267::1"
)

// tunRange is where the adapter's IPv4 subnet comes from. 198.18.0.0/15 is reserved for benchmarks
// (RFC 2544), so no real network uses it, but other TUN apps (Clash, sing-box, v2rayN) like it too,
// and Windows refuses an address another adapter already has (#43). So On picks a free /24 in it.
var tunRange = netip.MustParsePrefix("198.18.0.0/15")

// pickNet returns the adapter's address and subnet: .1 of the first /24 in tunRange that overlaps none
// of used, or else of the first whose .1 nobody holds (sitting inside another app's bigger subnet
// works, a duplicate address does not).
func pickNet(used []netip.Prefix) netip.Prefix {
	var spare netip.Prefix
	for i := range 1 << (24 - tunRange.Bits()) {
		b := tunRange.Addr().As4()
		b[1] += byte(i >> 8)
		b[2], b[3] = byte(i), 1
		p := netip.PrefixFrom(netip.AddrFrom4(b), 24)
		free, held := true, false
		for _, u := range used {
			free = free && !p.Overlaps(u)
			held = held || u.Addr() == p.Addr()
		}
		if free {
			return p
		}
		if !held && !spare.IsValid() {
			spare = p
		}
	}
	if spare.IsValid() {
		return spare
	}
	return netip.PrefixFrom(tunRange.Addr().Next(), 24)
}

// usedNets lists the IPv4 subnets of every adapter except ours (skip).
func usedNets(skip string) []netip.Prefix {
	ifs, _ := net.Interfaces()
	var out []netip.Prefix
	for _, ifc := range ifs {
		if ifc.Name == skip {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ones, bits := n.Mask.Size()
			if bits == 128 {
				ones -= 96
			}
			out = append(out, netip.PrefixFrom(netip.AddrFrom4([4]byte(n.IP.To4())), ones))
		}
	}
	return out
}

// cmdError puts the tool's own message first. The tray shows only an error's first 70 characters,
// and with the command line first that was all command and no reason (#43).
func cmdError(name string, args []string, err error, out []byte) error {
	cmdline := name + " " + strings.Join(args, " ")
	if msg := strings.Join(strings.Fields(string(out)), " "); msg != "" {
		return fmt.Errorf("%s (%s: %v)", msg, cmdline, err)
	}
	return fmt.Errorf("%s: %v", cmdline, err)
}

// openName is what the OS is asked to call the adapter; macOS only accepts "utunN" and picks the number.
var openName = tunName

// staleHook, if set by a platform, also clears what a crashed run left outside the adapter.
var staleHook func()

// Two /1 routes per family beat any default route (/0) without touching it, so Off() is just
// deleting these.
var (
	routes4 = []string{"0.0.0.0/1", "128.0.0.0/1"}
	routes6 = []string{"::/1", "8000::/1"}
)

// tunProxy plugs the Balancer into tun2socks: every TCP/UDP flow that enters the TUN is dialed
// out through a pinned NIC. There is no SOCKS hop in between.
type tunProxy struct{ b *engine.Balancer }

func (p tunProxy) DialContext(ctx context.Context, m *M.Metadata) (net.Conn, error) {
	return p.b.DialPinned(ctx, m.DstIP.Unmap().String(), int(m.DstPort))
}

func (p tunProxy) DialUDP(m *M.Metadata) (net.PacketConn, error) {
	f := engine.Fam4
	if d := m.DstIP.Unmap(); d.Is6() {
		f = engine.Fam6
	}
	return p.b.ListenUDP(context.Background(), f, m.SrcPort)
}

// Tun is the system-wide switch: On creates the adapter and routes, Off removes them. Both are
// idempotent. If the process dies, the adapter and its routes die with it.
type Tun struct {
	mu  sync.Mutex
	B   *engine.Balancer // set before On
	MTU int              // set before On; see DefaultMTU
	dev device.Device
	stk *stack.Stack
	idx int
	on  bool
}

// DefaultMTU is the TUN's MTU, used both by the userland stack and by the OS. TUN traffic never
// leaves the machine (flows are re-dialed with normal sockets), so it isn't bound by the links'
// 1500. Bigger packets mean far fewer per-packet trips through the userland stack: about 6x fewer
// for bulk TCP at 9000. Not 65535: the TUN library allocates a full-MTU buffer for every inbound
// packet, even a 40-byte ACK.
const DefaultMTU = 9000

// udpSessionTimeout ends idle UDP sessions sooner than tun2socks' 60s. Torrent DHT opens a session
// per peer queried, each holding a socket, two goroutines and two 64 KB buffers. 30s stays above the
// ~25s keepalives common in VPN and NAT-traversal protocols.
const udpSessionTimeout = 30 * time.Second

func New(b *engine.Balancer) *Tun { return &Tun{B: b, MTU: DefaultMTU} }

func (t *Tun) IsOn() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.on
}

// keepDevice makes Off pause instead of tear down: only the capture routes go, the adapter stays.
// macOS sets it, because there taking a utun away or adding one makes the system drop other
// virtual links such as a TetherKit phone, so pausing and resuming must never touch the adapter.
var keepDevice bool

// open creates the adapter and the userland stack, without capturing anything yet.
func (t *Tun) open() error {
	// "error", not "warn": tun2socks warns once per dropped UDP packet ("symmetric NAT ... drop
	// packet"), which a torrent client triggers continuously.
	if lvl, e := tlog.ParseLevel("error"); e == nil {
		tlog.SetLogger(tlog.Must(tlog.NewLeveled(lvl)))
	}
	tunnel.T().SetUDPTimeout(udpSessionTimeout)
	CleanupStale()

	dev, err := tun.Open(openName, uint32(t.MTU))
	if err != nil {
		return fmt.Errorf("open TUN (%s): %w", openHint, err)
	}
	stk, err := core.CreateStack(&core.Config{
		LinkEndpoint:     dev,
		TransportHandler: tunnel.T(),
		Options:          []option.Option{option.WithTCPModerateReceiveBuffer(true)},
	})
	if err != nil {
		dev.Close()
		return err
	}
	installForwarders(stk, t.B.V6Allowed)
	t.dev, t.stk = dev, stk
	return nil
}

// Warm creates the adapter ahead of time, so a later On only adds routes. Only platforms with
// keepDevice need it; elsewhere it does nothing.
func (t *Tun) Warm() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !keepDevice || t.dev != nil {
		return nil
	}
	return t.open()
}

func (t *Tun) On() (err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.on {
		return nil
	}
	if t.dev == nil {
		if err = t.open(); err != nil {
			return err
		}
	}
	defer func() {
		if err != nil {
			t.teardown()
		}
	}()
	tunnel.T().SetProxy(tunProxy{t.B})

	ip4 := pickNet(usedNets(t.dev.Name()))
	if ip4.Addr() != tunRange.Addr().Next() {
		logx.Printf("note: another adapter is on %s, using %s\n", netip.PrefixFrom(tunRange.Addr(), 24), ip4)
	}
	if t.idx, err = configure(t.dev.Name(), t.MTU, ip4); err != nil {
		return err
	}
	t.on = true
	return nil
}

// Off stops capturing: with keepDevice it only removes the routes, otherwise it removes the adapter.
func (t *Tun) Off() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if keepDevice && t.dev != nil {
		if t.idx != 0 {
			delRoutes(t.idx) // traffic falls straight back to the normal default route
		}
		tunnel.T().SetProxy(&reject.Reject{})
		t.on = false
		return
	}
	t.teardown()
}

// Close removes everything, including a kept adapter. Call it when the program exits.
func (t *Tun) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.teardown()
}

func (t *Tun) teardown() {
	if t.idx != 0 {
		delRoutes(t.idx) // traffic falls straight back to the normal default route
	}
	tunnel.T().SetProxy(&reject.Reject{})
	if t.dev != nil {
		t.dev.Close()
	}
	if t.stk != nil {
		done := make(chan struct{})
		go func() { t.stk.Close(); t.stk.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	t.dev, t.stk, t.idx, t.on = nil, nil, 0, false
}

// CleanupStale removes routes left on a surviving net-merge adapter (e.g. after a crash).
func CleanupStale() {
	if staleHook != nil {
		staleHook()
	}
	if ifc, err := net.InterfaceByName(tunName); err == nil {
		delRoutes(ifc.Index)
	}
}
