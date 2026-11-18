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
	if !func() bool { ok, _ := probe(s.slots[0], Fam4); return ok }() {
		t.Fatal("probe should pass against a reachable target")
	}
	ln.Close()
	if func() bool { ok, _ := probe(s.slots[0], Fam4); return ok }() {
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
	if got := sb.weight[Fam4] / sa.weight[Fam4]; got > 0.06 {
		t.Fatalf("slow v4 path should be down-weighted to ~0.05, got ratio %.2f", got)
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
	if w := sb.weight[Fam4] / sa.weight[Fam4]; w > 0.06 {
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

