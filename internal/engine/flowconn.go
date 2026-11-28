package engine

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"net-merge/internal/nics"
)

// slotConn counts a pinned TCP flow's bytes and releases its slot on Close. It exposes
// CloseRead/CloseWrite because the TUN relay half-closes each direction separately.
type slotConn struct {
	*net.TCPConn
	sl       *Slot
	fam      Family
	s        *Scheduler
	once     sync.Once
	activity *flowActivity
}

func (c *slotConn) Read(p []byte) (int, error) {
	if pair := c.sl.limit.Load(); pair != nil && len(p) > pair.chunk {
		p = p[:pair.chunk] // hand out a chunk at a time, so a limited flow stays smooth
	}
	n, err := c.TCPConn.Read(p)
	if n > 0 {
		c.sl.bytesIn[c.fam].Add(int64(n))
		if c.activity != nil {
			c.activity.bytes.Add(int64(n))
		}
		c.sl.throttle(true, n)
	}
	return n, err
}

func (c *slotConn) Write(p []byte) (int, error) {
	n, err := c.sl.writePaced(c.TCPConn, p, false)
	c.sl.bytesOut[c.fam].Add(int64(n))
	if c.activity != nil {
		c.activity.bytes.Add(int64(n))
	}
	return n, err
}

// release frees the slot's balancing count. It happens at the first half-close, not at the final
// Close: the relay can keep a finished flow open for up to a minute waiting for the peer's FIN,
// and counting those as active would steer new flows away from the NIC that just finished.
func (c *slotConn) release() {
	c.once.Do(func() {
		c.s.untrack(c.sl, c.fam, c.activity)
		c.s.ReleaseFor(c.sl, c.fam)
	})
}

func (c *slotConn) CloseRead() error  { c.release(); return c.TCPConn.CloseRead() }
func (c *slotConn) CloseWrite() error { c.release(); return c.TCPConn.CloseWrite() }
func (c *slotConn) Close() error      { c.release(); return c.TCPConn.Close() }

// DialPinned is DialTCP for front-ends that hand the conn to someone else to pump.
func (b *Balancer) DialPinned(ctx context.Context, host string, port int) (net.Conn, error) {
	c, sl, f, err := b.DialTCP(ctx, host, port)
	if err != nil {
		return nil, err
	}
	return &slotConn{TCPConn: c.(*net.TCPConn), sl: sl, fam: f, s: b.S, activity: b.S.track(sl, f)}, nil
}

// udpFlowActivity tracks per-socket UDP packet statistics within an active burst window to detect real-time game or voice traffic.
type udpFlowActivity struct {
	mu                        sync.Mutex
	started, lastActive       time.Time
	packets, smallPkts, bytes int64

	app     *appPeers // shared by every socket of the same application port
	appPort uint16
}

// appPeers is who one application port talks to, over the current and the last peerWindow.
type appPeers struct {
	sockets int   // guarded by Scheduler.mu
	playing *Slot // the link its game or call is on, as of the last Tick; guarded by Scheduler.mu

	mu    sync.Mutex
	peers map[netip.AddrPort]struct{}
	since time.Time
	last  int
}

func (p *appPeers) add(peer netip.AddrPort, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now.Sub(p.since) > peerWindow {
		p.last, p.peers, p.since = len(p.peers), nil, now
	}
	if len(p.peers) <= gamePeers {
		if p.peers == nil {
			p.peers = map[netip.AddrPort]struct{}{}
		}
		p.peers[peer] = struct{}{}
	}
}

func (p *appPeers) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(len(p.peers), p.last)
}

// A game or call talks to a few peers. A torrent client's DHT sends the same kind of small, steady
// packets to hundreds. ponytail: a fixed cut-off; raise it if a peer-to-peer game is missed.
const (
	gamePeers  = 16
	peerWindow = 10 * time.Second
)

func (u *udpFlowActivity) record(n int, addr net.Addr, now time.Time) {
	// Skip non-game/background protocols per packet so a single DNS/QUIC probe does not permanently taint the socket.
	if ua, ok := addr.(*net.UDPAddr); ok && (ua.Port == 53 || ua.Port == 123 || ua.Port == 443 || ua.Port == 5353) {
		return
	}
	u.mu.Lock()
	// Start a fresh activity window on the first valid packet or after >3s of idle time.
	if u.started.IsZero() || now.Sub(u.lastActive) > 3*time.Second {
		u.started = now
		u.packets = 0
		u.smallPkts = 0
		u.bytes = 0
	}
	if ua, ok := addr.(*net.UDPAddr); ok {
		if u.app == nil {
			u.app = &appPeers{}
		}
		u.app.add(ua.AddrPort(), now)
	}
	if now.After(u.lastActive) {
		u.lastActive = now
	}
	u.packets++
	u.bytes += int64(n)
	if n < 600 {
		u.smallPkts++
	}
	u.mu.Unlock()
}

// isGameLike reports whether the current active window sustains a low-bandwidth, steady stream of small UDP packets (>=5s, >=5 pkts/s, >=50% <600B, <1Mbps).
func (u *udpFlowActivity) isGameLike(now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started.IsZero() || now.Sub(u.lastActive) > 3*time.Second {
		return false
	}
	if u.app != nil && u.app.count() > gamePeers {
		return false
	}
	dur := now.Sub(u.started).Seconds()
	if dur < 5 {
		return false
	}
	pkts, small := float64(u.packets), float64(u.smallPkts)
	return pkts/dur >= 5 && small >= 0.5*pkts && float64(u.bytes)*8/dur < 1_000_000
}

type slotPacketConn struct {
	net.PacketConn
	sl   *Slot
	fam  Family
	s    *Scheduler
	once sync.Once
	udp  *udpFlowActivity
}

func (c *slotPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, a, err := c.PacketConn.ReadFrom(p)
	if n > 0 {
		c.sl.bytesIn[c.fam].Add(int64(n))
		if c.udp != nil {
			c.udp.record(n, a, c.s.now())
		}
		c.sl.throttlePacket(true, n)
	}
	return n, a, err
}

func (c *slotPacketConn) WriteTo(p []byte, a net.Addr) (int, error) {
	c.sl.throttlePacket(false, len(p))
	n, err := c.PacketConn.WriteTo(p, a)
	if n > 0 {
		c.sl.bytesOut[c.fam].Add(int64(n))
		if c.udp != nil {
			c.udp.record(n, a, c.s.now())
		}
	}
	return n, err
}

// ListenUDP opens a UDP socket pinned to one NIC (a UDP "flow" = the socket, so every datagram
// of that flow leaves the same way). UDP flows are short and numerous (DNS), so they don't hold
// a balancing slot: the pick still spreads them via the scheduler's tie-breaking.
func (b *Balancer) ListenUDP(ctx context.Context, f Family, appPort uint16) (net.PacketConn, error) {
	network := "udp4"
	if f == Fam6 {
		network = "udp6"
	}
	tried := map[string]struct{}{}
	lastErr := errors.New("no usable network adapter")
	for {
		sl := b.S.AcquireUDP(f, tried, appPort)
		if sl == nil {
			return nil, lastErr
		}
		tried[sl.key] = struct{}{}
		lc := net.ListenConfig{Control: nics.ForceEgress(sl.Index, f == Fam6)}
		pc, err := lc.ListenPacket(ctx, network, net.JoinHostPort(sl.ip(f).String(), "0"))
		b.S.ReleaseFor(sl, f)
		if err != nil {
			if classify(err).linkDown {
				b.S.Fail(sl, f, b.FailCooldown)
			}
			lastErr = err
			continue
		}
		return &slotPacketConn{PacketConn: pc, sl: sl, fam: f, s: b.S, udp: b.S.trackUDP(sl, f, appPort)}, nil
	}
}

func (c *slotPacketConn) Close() error {
	c.once.Do(func() {
		if c.s != nil && c.udp != nil {
			c.s.untrackUDP(c.sl, c.fam, c.udp)
		}
	})
	return c.PacketConn.Close()
}
