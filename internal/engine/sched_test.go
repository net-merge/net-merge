package engine

import (
	"math"
	"net"
	"testing"
	"time"

	"net-merge/internal/nics"
)

func nic(name, ip string, w float64) nics.Nic {
	return nics.Nic{Name: name, IP: net.ParseIP(ip), Weight: w}
}

func countBy(picks []*Slot, name string) int {
	n := 0
	for _, p := range picks {
		if p.Name == name {
			n++
		}
	}
	return n
}

func acquireN(s *Scheduler, n int) []*Slot {
	out := make([]*Slot, n)
	for i := range out {
		out[i] = s.Acquire(nil)
	}
	return out
}

func TestEqualWeightsSpreadEvenly(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	p := acquireN(s, 6)
	if countBy(p, "A") != 3 || countBy(p, "B") != 3 {
		t.Fatalf("want 3/3, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

func TestAutoIgnoresIdleSockets(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	a, b := s.slots[0], s.slots[1]
	for range 20 {
		a.actF[Fam4].Add(1)
		s.track(a, Fam4)
	}
	s.Tick(time.Second)
	if got := a.busy(Fam4); got != 20 {
		t.Fatalf("sockets became idle before two seconds: busy=%d", got)
	}
	s.Tick(time.Second)
	if got := a.busy(Fam4); got != 0 {
		t.Fatalf("idle sockets counted as busy: %d", got)
	}
	// A connecting socket still counts; an idle socket becomes busy on data.
	b.actF[Fam4].Add(1)
	if got := s.Acquire(nil); got != a {
		t.Fatal("idle sockets steered new flow away from available link")
	}
	s.Release(a)
	for activity := range a.flows[Fam4] {
		activity.bytes.Add(1000)
		break
	}
	s.Tick(time.Second)
	if a.busy(Fam4) != 1 || b.busy(Fam4) != 1 {
		t.Fatal("recent data and connecting sockets must count")
	}
}

func TestAutoDoesNotForceSlowLinkAfterIdle(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a, b := s.slots[0], s.slots[1]
	b.weight[Fam4] = 0.05
	for range 24 {
		clock = clock.Add(5 * time.Second)
		if got := s.Acquire(nil); got != a {
			t.Fatal("elapsed time overrode weighted scheduling")
		}
		s.Release(a)
	}
}

func TestStaticSchedulingCountsIdleSockets(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 0.05)})
	a, b := s.slots[0], s.slots[1]
	for range 20 {
		a.actF[Fam4].Add(1)
		s.track(a, Fam4)
	}
	s.Tick(flowIdleAfter)
	if got := s.Acquire(nil); got != b {
		t.Fatal("static scheduling discounted idle sockets")
	}
}

func TestAddressChangeResetsLearnedFamily(t *testing.T) {
	for _, f := range []Family{Fam4, Fam6} {
		t.Run(map[Family]string{Fam4: "IPv4", Fam6: "IPv6"}[f], func(t *testing.T) {
			a := nic("A", "10.0.0.1", 1)
			a.IP6 = net.ParseIP("2001:db8::1")
			s := NewScheduler([]nics.Nic{a})
			s.SetAuto(true)
			sl := s.slots[0]
			sl.peak = [2]float64{1e6, 2e6}
			sl.weight = [2]float64{0.1, 0.2}
			now := time.Now()
			sl.state = [2]linkState{stateDown, stateDown}
			sl.lastProbe = [2]time.Time{now, now}
			sl.downUntil = [2]time.Time{now.Add(time.Hour), now.Add(time.Hour)}
			if f == Fam4 {
				a.IP = net.ParseIP("10.1.0.1")
			} else {
				a.IP6 = net.ParseIP("2001:db8:1::1")
			}
			other := Fam6 - f
			peak, weight, cooldown := sl.peak[other], sl.weight[other], sl.downUntil[other]
			s.Update([]nics.Nic{a})
			if sl.peak[f] != 0 || sl.weight[f] != 1 || !sl.downUntil[f].IsZero() || !sl.lastProbe[f].IsZero() || sl.state[f] != stateUnknown {
				t.Fatal("new address retained stale estimate or probe state")
			}
			if sl.peak[other] != peak || sl.weight[other] != weight || sl.downUntil[other] != cooldown || sl.lastProbe[other] != now || sl.state[other] != stateDown {
				t.Fatal("unchanged address family's estimate or probe state reset")
			}
		})
	}
}

func TestThroughputEstimateSmoothsBursts(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	s.SetAuto(true)
	a := s.slots[0]
	a.bytesIn[Fam4].Add(1e6)
	s.Tick(time.Second)
	a.bytesIn[Fam4].Add(10e6)
	s.Tick(time.Second)
	want := 1e6 + (10e6-1e6)*(1-math.Exp(-1.0/2))
	if math.Abs(a.peak[Fam4]-want) > 1e-6 {
		t.Fatalf("rising estimate = %g, want %g", a.peak[Fam4], want)
	}
	peak := a.peak[Fam4]
	a.bytesIn[Fam4].Add(1e6)
	s.Tick(time.Second)
	want = peak + (1e6-peak)*(1-math.Exp(-1.0/30))
	if math.Abs(a.peak[Fam4]-want) > 1e-6 {
		t.Fatalf("falling estimate = %g, want %g", a.peak[Fam4], want)
	}
}

func TestTickIgnoresNonPositiveDuration(t *testing.T) {
	for _, dt := range []time.Duration{0, -time.Second} {
		t.Run(dt.String(), func(t *testing.T) {
			s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
			s.SetAuto(true)
			a := s.slots[0]
			a.actF[Fam4].Add(1)
			activity := s.track(a, Fam4)
			a.bytesIn[Fam4].Add(1e6)
			activity.bytes.Add(1e6)
			before := s.Snapshot()[0]
			s.Tick(dt)
			if got := s.Snapshot()[0]; got != before || a.lastIn[Fam4] != 0 || activity.last != 0 || activity.idle != 0 {
				t.Fatal("non-positive duration changed sampled state")
			}
			s.Tick(time.Second)
			if a.peak[Fam4] != 1e6 {
				t.Fatal("invalid tick consumed pending traffic sample")
			}
		})
	}
}

func TestWeightsSkew(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 3), nic("B", "10.0.0.2", 1)})
	p := acquireN(s, 8)
	if countBy(p, "A") != 6 || countBy(p, "B") != 2 {
		t.Fatalf("want 6/2, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

func TestReleaseFreesNic(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	first := s.Acquire(nil)
	s.Acquire(nil)
	s.Release(first)
	if got := s.Acquire(nil); got != first {
		t.Fatalf("want the released NIC %s, got %s", first.Name, got.Name)
	}
}

func TestFailCooldown(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	bad := s.Snapshot()[0]
	var badSlot *Slot
	for _, sl := range s.slots {
		if sl.Name == bad.Name {
			badSlot = sl
		}
	}
	s.Fail(badSlot, Fam4, 5*time.Second)
	for i := 0; i < 4; i++ {
		if s.Acquire(nil) == badSlot {
			t.Fatal("cooling-down NIC was picked")
		}
	}
	clock = clock.Add(6 * time.Second)
	seen := false
	for i := 0; i < 6; i++ {
		if s.Acquire(nil) == badSlot {
			seen = true
		}
	}
	if !seen {
		t.Fatal("NIC never came back after cooldown")
	}
}

func TestAllDownStillServes(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	s.Fail(s.slots[0], Fam4, time.Hour)
	if s.Acquire(nil) == nil {
		t.Fatal("total outage: all-NICs-down must still hand one out")
	}
}

func TestProbeStateTiers(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1)})
	a, b, c := s.slots[0], s.slots[1], s.slots[2]
	s.SetState(a, Fam4, true)
	s.SetState(c, Fam4, false)
	// A is up, B is unprobed, C is dead: only A gets flows while it is the best tier.
	for i := 0; i < 5; i++ {
		if got := s.Acquire(nil); got != a {
			t.Fatalf("flow %d went to %s, want the probed-up NIC", i, got.Name)
		}
	}
	s.SetState(a, Fam4, false) // now B (unknown) beats the two dead ones
	if got := s.Acquire(nil); got != b {
		t.Fatalf("want unprobed NIC over dead ones, got %s", got.Name)
	}
	s.SetState(b, Fam4, false) // everything dead: still serve, never an outage
	if s.Acquire(nil) == nil {
		t.Fatal("all-dead must still hand out a NIC")
	}
	s.SetState(c, Fam4, true) // a recovered NIC takes over immediately
	if got := s.Acquire(nil); got != c {
		t.Fatalf("recovered NIC should win, got %s", got.Name)
	}
}

func TestProbeAgainstLocalListener(t *testing.T) {
	needLoopbackAliases(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	old := probeTargets[Fam4]
	defer func() { probeTargets[Fam4] = old }()
	probeTargets[Fam4] = []string{ln.Addr().String()}

	s := NewScheduler([]nics.Nic{nic("A", "127.0.0.2", 1)})
	if !probe(s.slots[0], Fam4) {
		t.Fatal("probe should pass against a reachable target")
	}
	ln.Close()
	if probe(s.slots[0], Fam4) {
		t.Fatal("probe should fail once the target is gone")
	}
}

func TestIPv6OnlyGoesToNicsWithV6(t *testing.T) {
	n6 := nic("B", "10.0.0.2", 1)
	n6.IP6 = net.ParseIP("2001:db8::2")
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), n6})
	for i := 0; i < 4; i++ {
		if got := s.AcquireFor(Fam6, nil); got == nil || got.Name != "B" {
			t.Fatalf("v6 flow %d went to %v, want the only v6-capable NIC B", i, got)
		}
	}
	if got := s.AcquireFor(Fam4, nil); got == nil {
		t.Fatal("v4 flows must still work")
	}
	s2 := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	if s2.AcquireFor(Fam6, nil) != nil {
		t.Fatal("no v6-capable NIC: v6 flows must be refused so apps fall back to IPv4")
	}
}

func TestV6StateIsIndependentOfV4(t *testing.T) {
	n := nic("A", "10.0.0.1", 1)
	n.IP6 = net.ParseIP("2001:db8::1")
	m := nic("B", "10.0.0.2", 1)
	m.IP6 = net.ParseIP("2001:db8::2")
	s := NewScheduler([]nics.Nic{n, m})
	a, b := s.slots[0], s.slots[1]
	s.SetState(a, Fam4, true)
	s.SetState(b, Fam4, true)
	s.SetState(a, Fam6, false) // A has working v4 but its v6 is dead
	s.SetState(b, Fam6, true)
	for i := 0; i < 4; i++ {
		got := s.AcquireFor(Fam6, nil)
		if got != b {
			t.Fatalf("v6 flow %d went to %s, want B", i, got.Name)
		}
		s.ReleaseFor(got, Fam6)
	}
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		seen[s.AcquireFor(Fam4, nil).Name] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("v4 should still use both NICs, got %v", seen)
	}
}

// A hotspot that is fast over native IPv6 but slow over translated IPv4 must be weighted per
// family: nearly starved for v4 flows, fully used for v6 flows.
func TestAutoWeightsArePerFamily(t *testing.T) {
	a := nic("A", "10.0.0.1", 1)
	a.IP6 = net.ParseIP("2001:db8::1")
	b := nic("B", "10.0.0.2", 1)
	b.IP6 = net.ParseIP("2001:db8::2")
	s := NewScheduler([]nics.Nic{a, b})
	s.SetAuto(true)
	sa, sb := s.slots[0], s.slots[1]
	const mbit = 125_000 // bytes/s
	// v4: A 100 Mbit/s, B 5 Mbit/s.  v6: both 100 Mbit/s.
	for i := 0; i < 3; i++ {
		sa.bytesIn[Fam4].Add(100 * mbit)
		sb.bytesIn[Fam4].Add(5 * mbit)
		sa.bytesIn[Fam6].Add(100 * mbit)
		sb.bytesIn[Fam6].Add(100 * mbit)
		s.Tick(time.Second)
	}
	if got := sb.weight[Fam4] / sa.weight[Fam4]; got > minWeight+0.01 {
		t.Fatalf("slow v4 path should be down-weighted to the floor, got ratio %.2f", got)
	}
	if got := sb.weight[Fam6] / sa.weight[Fam6]; got < 0.95 {
		t.Fatalf("fast v6 path must keep full weight, got ratio %.2f", got)
	}
	// And the peak is remembered while idle (no traffic for a while).
	for i := 0; i < 20; i++ {
		s.Tick(time.Second)
	}
	if sb.peak[Fam6] < 90*mbit {
		t.Fatalf("idle NIC forgot its capacity: peak %.0f", sb.peak[Fam6])
	}
}

// A NIC whose estimate was poisoned by one bad moment must not stay starved forever.
func TestPessimisticEstimateRecoversWhileIdle(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	sa, sb := s.slots[0], s.slots[1]
	const mbit = 125_000
	for i := 0; i < 3; i++ { // B looks 20x slower than A
		sa.bytesIn[Fam4].Add(100 * mbit)
		sb.bytesIn[Fam4].Add(5 * mbit)
		s.Tick(time.Second)
	}
	if w := sb.weight[Fam4] / sa.weight[Fam4]; w > minWeight+0.01 {
		t.Fatalf("setup: B should be down-weighted, ratio %.2f", w)
	}
	for i := 0; i < 80; i++ { // then nobody uses B, and A keeps working
		sa.bytesIn[Fam4].Add(100 * mbit)
		s.Tick(time.Second)
	}
	if w := sb.weight[Fam4] / sa.weight[Fam4]; w < 0.45 {
		t.Fatalf("idle NIC should drift back up to ~0.5 so it is re-measured, ratio %.2f", w)
	}
}

func TestIdleRecoveryCappedAtHalfBestLink(t *testing.T) {
	for _, fastActive := range []bool{false, true} {
		name := "both idle"
		if fastActive {
			name = "fast link active"
		}
		t.Run(name, func(t *testing.T) {
			a := nic("A", "10.0.0.1", 1)
			a.IP6 = net.ParseIP("2001:db8::1")
			b := nic("B", "10.0.0.2", 1)
			b.IP6 = net.ParseIP("2001:db8::2")
			s := NewScheduler([]nics.Nic{a, b})
			s.SetAuto(true)
			fast, slow := s.slots[0], s.slots[1]
			for _, f := range []Family{Fam4, Fam6} {
				fast.bytesIn[f].Add(10e6)
				slow.bytesIn[f].Add(500_000)
			}
			s.Tick(time.Second)
			for range 600 {
				if fastActive {
					for _, f := range []Family{Fam4, Fam6} {
						fast.bytesIn[f].Add(10e6)
					}
				}
				s.Tick(time.Second)
				for _, f := range []Family{Fam4, Fam6} {
					if ratio := slow.weight[f] / fast.weight[f]; ratio > 0.5 {
						t.Fatalf("family %d: idle recovery exceeded half weight: %g", f, ratio)
					}
				}
			}
			for _, f := range []Family{Fam4, Fam6} {
				if ratio := slow.weight[f] / fast.weight[f]; math.Abs(ratio-0.5) > 1e-6 {
					t.Fatalf("family %d: idle estimate did not recover to half weight: %g", f, ratio)
				}
			}
		})
	}
}

func TestV6CoverageDecidesWhetherIPv6IsBalanced(t *testing.T) {
	a := nic("A", "10.0.0.1", 1)
	a.IP6 = net.ParseIP("2001:db8::1")
	b := nic("B", "10.0.0.2", 1) // IPv4 only
	s := NewScheduler([]nics.Nic{a, b})
	if s.V6CoversAll() {
		t.Fatal("B has no IPv6: IPv6 would funnel onto A, coverage must be partial")
	}
	if !s.V6Usable() {
		t.Fatal("A can carry IPv6")
	}
	b.IP6 = net.ParseIP("2001:db8::2")
	s.Update([]nics.Nic{a, b})
	if !s.V6CoversAll() {
		t.Fatal("every adapter has IPv6 now")
	}
	s.SetState(s.slots[1], Fam6, false) // B's IPv6 probe fails
	if s.V6CoversAll() {
		t.Fatal("a dead IPv6 path on B means partial coverage again")
	}
	only4 := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	if only4.V6CoversAll() || only4.V6Usable() {
		t.Fatal("no IPv6 anywhere: nothing to allow")
	}
}

func TestSkipSet(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	a, b := s.slots[0], s.slots[1]
	if got := s.Acquire(map[string]struct{}{a.key: {}}); got != b {
		t.Fatal("skip set not honoured")
	}
	if got := s.Acquire(map[string]struct{}{a.key: {}, b.key: {}}); got != nil {
		t.Fatal("want nil when everything is skipped")
	}
}

func TestUpdateAddsAndKeepsCounters(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	s.Acquire(nil)
	s.Update([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	snap := s.Snapshot()
	if len(snap) != 2 || snap[0].Total != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

func TestUpdateDropsVanishedWhenIdle(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	busy := s.Acquire(nil)
	s.Update(nil)
	if len(s.Snapshot()) != 1 {
		t.Fatalf("idle NIC should be dropped, busy one kept: %+v", s.Snapshot())
	}
	if s.Acquire(nil) != nil {
		t.Fatal("vanished NIC must not get new flows")
	}
	s.Release(busy)
	s.Update(nil)
	if len(s.Snapshot()) != 0 {
		t.Fatal("NIC should be dropped once idle")
	}
}

// A full mobile link answers a probe too late. While data is still arriving it is not offline.
func TestFailedProbeIgnoredWhileDataArrives(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	a := s.slots[0]
	s.SetState(a, Fam4, true)
	a.bytesIn[Fam4].Add(1_000_000)
	s.Tick(time.Second)
	s.SetState(a, Fam4, false)
	if a.state[Fam4] != stateUp {
		t.Fatalf("busy link marked %q", a.state[Fam4])
	}
	s.Tick(time.Second) // nothing arrived: now the failure counts
	s.SetState(a, Fam4, false)
	if a.state[Fam4] != stateDown {
		t.Fatalf("quiet link left %q", a.state[Fam4])
	}
}

func TestGameModeSteersNewFlows(t *testing.T) {
	now := time.Now()
	s := NewScheduler([]nics.Nic{{Name: "eth0", Index: 1, IP: net.ParseIP("10.0.0.1"), Weight: 1}, {Name: "eth1", Index: 2, IP: net.ParseIP("10.0.0.2"), Weight: 1}})
	s.now = func() time.Time { return now }
	s.SetGameMode(true)
	sl0 := s.slots[0]
	u := s.trackUDP(sl0, Fam4, 40000)
	for i := 0; i < 12; i++ {
		now = now.Add(time.Second)
		for p := 0; p < 20; p++ {
			u.record(120, &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 27015}, now)
		}
		s.Tick(time.Second)
	}
	if !sl0.gameActive {
		t.Fatal("expected eth0 gameActive")
	}
	for i := 0; i < 10; i++ {
		got := s.Acquire(nil)
		if got == nil || got.Name != "eth1" {
			t.Fatalf("got %v", got)
		}
		s.Release(got)
	}
}

func TestGameModeActivityWindowAndIgnoredPorts(t *testing.T) {
	now := time.Now()
	s := NewScheduler([]nics.Nic{{Name: "eth0", Index: 1, IP: net.ParseIP("10.0.0.1"), Weight: 1}, {Name: "eth1", Index: 2, IP: net.ParseIP("10.0.0.2"), Weight: 1}})
	s.now = func() time.Time { return now }
	s.SetGameMode(true)
	sl0 := s.slots[0]
	u := s.trackUDP(sl0, Fam4, 40000)

	// Background DNS/QUIC probes followed by a 60s idle gap must neither trigger nor taint the socket.
	u.record(64, &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}, now)
	u.record(120, &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 443}, now)
	now = now.Add(60 * time.Second)
	s.Tick(time.Second)
	if sl0.gameActive {
		t.Fatal("expected eth0 inactive after background probes and idle gap")
	}

	// Sustaining small UDP packets for 6s should start a fresh window and steer new TCP flows to eth1.
	for i := 0; i < 6; i++ {
		now = now.Add(time.Second)
		for p := 0; p < 10; p++ {
			u.record(120, &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 27015}, now)
		}
		s.Tick(time.Second)
	}
	if !sl0.gameActive {
		t.Fatal("expected eth0 gameActive after 6s active burst")
	}
	if got := s.Acquire(nil); got == nil || got.Name != "eth1" {
		t.Fatalf("expected steering to eth1, got %v", got)
	} else {
		s.Release(got)
	}

	// Idle >3s should clear gameActive.
	now = now.Add(4 * time.Second)
	s.Tick(time.Second)
	if sl0.gameActive {
		t.Fatal("expected eth0 gameActive cleared after >3s idle")
	}
}

// A torrent client's DHT also sends small packets steadily, but to far more peers than a game.
func TestManyPeersIsNotAGame(t *testing.T) {
	now := time.Now()
	u := &udpFlowActivity{}
	for i := range 300 {
		now = now.Add(100 * time.Millisecond)
		u.record(120, &net.UDPAddr{IP: net.IPv4(10, 1, byte(i>>8), byte(i)), Port: 6881}, now)
		if i > 60 && u.isGameLike(now) {
			t.Fatalf("taken for a game after %d packets to %d peers", i+1, i+1)
		}
	}
}

// A phone on standby must not push a new flow back onto the link that carries the game.
func TestGameModeWinsOverStandby(t *testing.T) {
	phone := nic("phone", "172.20.10.2", 1)
	phone.Phone = true
	s := NewScheduler([]nics.Nic{nic("wire", "10.0.0.1", 1), phone})
	s.SetGameMode(true)
	for _, sl := range s.slots {
		s.SetState(sl, Fam4, true)
	}
	s.slots[0].peak[Fam4], s.slots[1].peak[Fam4] = 10e6, 5e6
	s.slots[0].gameActive = true
	if got := s.Acquire(nil); got.Name != "phone" {
		t.Fatalf("new flow went to %s, the link with the game", got.Name)
	}
}

// The weight floor must not lift a link that is slow because it is capped.
func TestCappedLinkIsNotLiftedByTheFloor(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	s.SetLimit("B", 1)
	A, B := s.slots[0], s.slots[1]
	for _, sl := range s.slots {
		s.SetState(sl, Fam4, true)
	}
	A.peak[Fam4], B.peak[Fam4] = 12.5e6, 125e3
	s.Tick(time.Second)
	got := map[string]int{}
	for range 9 {
		sl := s.AcquireFor(Fam4, nil)
		got[sl.Name]++
		s.track(sl, Fam4)
	}
	if got["B"] > 1 {
		t.Fatalf("a link capped at 1 Mbit/s got %d of 9 flows (weight %.2f)", got["B"], B.weight[Fam4])
	}
}

// Full over IPv4 while the IPv6 probe times out is still a working link.
func TestFailedProbeIgnoredWhileOtherFamilyReceives(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	a := s.slots[0]
	s.SetState(a, Fam6, true)
	a.bytesIn[Fam4].Add(1_000_000)
	s.Tick(time.Second)
	s.SetState(a, Fam6, false)
	if a.state[Fam6] != stateUp {
		t.Fatalf("busy link's IPv6 marked %q", a.state[Fam6])
	}
}

// The tunnel opens one socket per destination, so the torrent guard counts per application port.
func TestManySocketsFromOneAppPortAreNotAGame(t *testing.T) {
	now := time.Now()
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	var socks []*udpFlowActivity
	for range 30 {
		socks = append(socks, s.trackUDP(s.slots[0], Fam4, 6881))
	}
	for range 60 {
		now = now.Add(100 * time.Millisecond)
		for i, u := range socks {
			u.record(120, &net.UDPAddr{IP: net.IPv4(10, 1, 0, byte(i)), Port: 6881}, now)
		}
	}
	for _, u := range socks {
		if u.isGameLike(now) {
			t.Fatal("one of 30 peer sockets of a torrent client was taken for a game")
		}
	}
	game := s.trackUDP(s.slots[0], Fam4, 50000)
	for range 60 {
		now = now.Add(100 * time.Millisecond)
		game.record(120, &net.UDPAddr{IP: net.IPv4(10, 2, 0, 1), Port: 27015}, now)
	}
	if !game.isGameLike(now) {
		t.Fatal("a single game socket must still count")
	}
}

// A link filled by an upload receives little more than acknowledgements, and still works.
func TestFailedProbeIgnoredWhileUploading(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	a := s.slots[0]
	s.SetState(a, Fam4, true)
	a.bytesOut[Fam4].Add(1_000_000)
	s.Tick(time.Second)
	a.linkRateIn = 25_000 // acknowledgements, from the OS's counters
	s.SetState(a, Fam4, false)
	if a.state[Fam4] != stateUp {
		t.Fatalf("uploading link marked %q", a.state[Fam4])
	}
	a.linkRateIn = 0 // sending into the void: nothing comes back
	s.SetState(a, Fam4, false)
	if a.state[Fam4] != stateDown {
		t.Fatalf("link that gets nothing back left %q", a.state[Fam4])
	}
}

// A capped link gets fewer busy flows than the others, so its falling samples are set aside. Its
// estimate must still stay at the cap, or the idle recovery lifts it to half the best link's and
// the capped link gets a third of the new flows again.
func TestCappedLinkStaysAtItsCapUnderLoad(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	s.SetLimit("B", 2)
	A, B := s.slots[0], s.slots[1]
	for _, sl := range s.slots {
		s.SetState(sl, Fam4, true)
	}
	open := func(sl *Slot) *flowActivity { sl.actF[Fam4].Add(1); sl.active.Add(1); return s.track(sl, Fam4) }
	var onA []*flowActivity
	for range 4 {
		onA = append(onA, open(A))
	}
	onB := open(B)
	for i := range 120 {
		for _, a := range onA {
			a.bytes.Add(3_125_000) // 100 Mbit/s over four flows
		}
		A.bytesIn[Fam4].Add(12_500_000)
		n := int64(250_000) // B at its 2 Mbit/s cap, give or take
		if i%2 == 1 {
			n = 230_000
		}
		onB.bytes.Add(n)
		B.bytesIn[Fam4].Add(n)
		s.Tick(time.Second)
	}
	if B.peak[Fam4] > 2*bytesPerMbit {
		t.Fatalf("estimate of a link capped at 2 Mbit/s rose to %.1f Mbit/s", B.peak[Fam4]/bytesPerMbit)
	}
	got := map[string]int{}
	for range 9 {
		sl := s.AcquireFor(Fam4, nil)
		got[sl.Name]++
		s.track(sl, Fam4)
	}
	if got["B"] > 1 {
		t.Fatalf("a link capped at 2 Mbit/s got %d of 9 flows (weight %.2f)", got["B"], B.weight[Fam4])
	}
}

// IPv4 filling the link vouches for IPv6 only until a real IPv6 connection on it times out too.
// Data arriving over IPv6 itself still vouches for it: then the timeout was one dead server.
func TestFailedProbeCountsOnceItsFamilyTimesOut(t *testing.T) {
	for _, c := range []struct {
		name     string
		busy     Family
		wantDown bool
	}{
		{"other family busy", Fam4, true},
		{"same family busy", Fam6, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
			a := s.slots[0]
			s.SetState(a, Fam6, true)
			a.bytesIn[c.busy].Add(1_000_000)
			s.Tick(time.Second)
			s.Suspect(a, Fam6) // an IPv6 connection timed out
			s.SetState(a, Fam6, false)
			if down := a.state[Fam6] == stateDown; down != c.wantDown {
				t.Fatalf("IPv6 state %q, want down=%v", a.state[Fam6], c.wantDown)
			}
		})
	}
}

// A game reaching a new peer, relay or voice server mid-match opens a new socket. That socket stays
// on the game's link; other applications' new flows still go elsewhere.
func TestGameModeKeepsTheGameOnItsLink(t *testing.T) {
	now := time.Now()
	s := NewScheduler([]nics.Nic{nic("eth0", "10.0.0.1", 1), nic("eth1", "10.0.0.2", 1)})
	s.now = func() time.Time { return now }
	s.SetGameMode(true)
	game := s.trackUDP(s.slots[0], Fam4, 40000)
	for range 12 {
		now = now.Add(time.Second)
		for range 20 {
			game.record(120, &net.UDPAddr{IP: net.ParseIP("1.1.1.1"), Port: 27015}, now)
		}
		s.Tick(time.Second)
	}
	for range 5 {
		got := s.AcquireUDP(Fam4, nil, 40000)
		if got.Name != "eth0" {
			t.Fatalf("the game's new socket went to %s, away from the game", got.Name)
		}
		s.ReleaseFor(got, Fam4)
	}
	if got := s.AcquireUDP(Fam4, nil, 50000); got.Name != "eth1" {
		t.Fatalf("another application's socket went to %s, the game's link", got.Name)
	} else {
		s.ReleaseFor(got, Fam4)
	}
	if got := s.AcquireUDP(Fam4, map[string]struct{}{"eth0": {}}, 40000); got == nil || got.Name != "eth1" {
		t.Fatalf("with the game's link failed the socket went to %v", got)
	} else {
		s.ReleaseFor(got, Fam4)
	}
	s.SetGameMode(false)
	seen := map[string]bool{}
	for range 40 {
		got := s.AcquireUDP(Fam4, nil, 40000)
		seen[got.Name] = true
		s.ReleaseFor(got, Fam4)
	}
	if !seen["eth1"] {
		t.Fatal("without Game Mode the sockets must spread as before")
	}
}
