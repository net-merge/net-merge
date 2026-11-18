package engine

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
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

type udpFlowActivity struct {
	started                               time.Time
	lastActive, packets, smallPkts, bytes atomic.Int64
	ignoredPort                           atomic.Bool
}

func (u *udpFlowActivity) record(n int, addr net.Addr, now time.Time) {
	u.lastActive.Store(now.UnixNano())
	u.packets.Add(1)
	u.bytes.Add(int64(n))
	if n < 300 {
		u.smallPkts.Add(1)
	}
	if ua, ok := addr.(*net.UDPAddr); ok && (ua.Port == 53 || ua.Port == 123 || ua.Port == 443 || ua.Port == 5353) {
		u.ignoredPort.Store(true)
	}
}
func (u *udpFlowActivity) isGameLike(now time.Time) bool {
	if u.ignoredPort.Load() {
		return false
	}
	last := u.lastActive.Load()
	if last == 0 || now.Sub(time.Unix(0, last)) > 3*time.Second {
		return false
	}
	dur := now.Sub(u.started).Seconds()
	if dur < 10 {
		return false
	}
	pkts, small := float64(u.packets.Load()), float64(u.smallPkts.Load())
	return pkts/dur >= 10 && small >= 0.7*pkts && float64(u.bytes.Load())*8/dur < 1_000_000
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
func (b *Balancer) ListenUDP(ctx context.Context, f Family) (net.PacketConn, error) {
	network := "udp4"
	if f == Fam6 {
		network = "udp6"
	}
	tried := map[string]struct{}{}
	lastErr := errors.New("no usable network adapter")
	for {
		sl := b.S.AcquireFor(f, tried)
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
		return &slotPacketConn{PacketConn: pc, sl: sl, fam: f, s: b.S, udp: b.S.trackUDP(sl, f)}, nil
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
