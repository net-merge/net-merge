package engine

import (
	"math"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"net-merge/internal/nics"
)

// Family selects which address of a NIC a flow is pinned to. Load, speed and health are tracked
// per family because one link can behave very differently over each (e.g. a carrier hotspot that
// is fast over native IPv6 and slow over translated IPv4).
type Family int

const (
	Fam4 Family = iota
	Fam6
)

// linkState is the result of the internet probe: new adapters are "unknown" (still preferred
// over dead ones, so a probe that can't run never causes an outage) until the first probe lands.
type linkState int

const (
	stateUnknown linkState = iota
	stateUp
	stateDown
)

func (l linkState) String() string {
	return [...]string{"checking", "up", "no internet"}[l]
}

type slotAddr struct{ ip4, ip6 net.IP }

// Slot is a NIC plus its live counters. Counters are atomic so the data path never takes a lock.
type Slot struct {
	Name  string
	Index int
	key   string
	addr  atomic.Pointer[slotAddr]

	active, total, failed atomic.Int64 // active is across families (idle check on removal)
	actF                  [2]atomic.Int64
	bytesIn, bytesOut     [2]atomic.Int64
	probing               [2]atomic.Bool
	limit                 atomic.Pointer[limitPair] // nil = unlimited; see limit.go
	phone                 atomic.Bool               // a phone sharing its mobile data; see phone.go

	// guarded by Scheduler.mu
	weight          [2]float64
	present         bool
	state           [2]linkState
	lastProbe       [2]time.Time
	probeIn         [2]int64 // bytesIn at the last probe or look, see probeDue
	suspect         [2]bool  // a connection timed out: probe now, see Suspect
	heat            time.Duration
	cooling         bool // held at the cool rate, see cool
	downUntil       [2]time.Time
	lastIn, lastOut [2]int64
	rateIn, rateOut [2]float64 // bytes/s over the last tick
	peak            [2]float64 // observed capacity: max recent rateIn+rateOut
	flows           [2]map[*flowActivity]struct{}

	// The adapter's total traffic from the OS's counters (all apps, not only net-merge's flows).
	linkIn, linkOut         uint64
	linkRateIn, linkRateOut float64
	linkPrimed              bool
}

// flowActivity separates recent data transfer from long-lived idle sockets. Byte
// accounting is lock-free; membership and sampled activity are guarded by mu.
type flowActivity struct {
	bytes atomic.Int64
	last  int64
	idle  time.Duration
}

const flowIdleAfter = 2 * time.Second

func (s *Scheduler) track(sl *Slot, f Family) *flowActivity {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := &flowActivity{}
	if sl.flows[f] == nil {
		sl.flows[f] = make(map[*flowActivity]struct{})
	}
	sl.flows[f][a] = struct{}{}
	return a
}

func (s *Scheduler) untrack(sl *Slot, f Family, a *flowActivity) {
	s.mu.Lock()
	delete(sl.flows[f], a)
	s.mu.Unlock()
}

func (sl *Slot) busy(f Family) int64 {
	// Acquired but not yet tracked flows are connecting, and must count too.
	n := max(int64(0), sl.actF[f].Load()-int64(len(sl.flows[f])))
	for a := range sl.flows[f] {
		if a.idle < flowIdleAfter {
			n++
		}
	}
	return n
}

func (sl *Slot) ip(f Family) net.IP {
	a := sl.addr.Load()
	if f == Fam6 {
		return a.ip6
	}
	return a.ip4
}

type SlotStats struct {
	Name              string
	IP                string
	IP6               string
	State             string // IPv4 probe state, "-" if the adapter has no IPv4
	State6            string
	Weight, Weight6   float64
	Present           bool
	Active, Total     int64
	Failed            int64
	BytesIn, BytesOut int64
	RateIn, RateOut   float64 // through net-merge, both families summed (bytes/s)
	LinkIn, LinkOut   float64 // through the adapter by any app, from the OS's counters (bytes/s)
	Peak, Peak6       float64
	LimitMbps         float64 // cap in Mbit/s, 0 = unlimited
	Phone             bool    // a phone sharing its mobile data
	CoolMbps          float64 // Mbit/s the phone saver holds it to right now, 0 = not held
}

// Scheduler uses weighted least-busy-flows per address family. Automatic mode
// discounts idle TCP sockets; static mode
// retains weighted least-open-flows. Data transfer accounting stays lock-free.
type Scheduler struct {
	mu     sync.Mutex
	slots  []*Slot
	auto   bool
	limits map[string]float64 // adapter name -> Mbit/s, 0 or absent = unlimited
	now    func() time.Time

	saver    atomic.Bool // phone saver, see phone.go
	coolMbps float64     // what a phone is held to after heavy use, 0 = never
}

func NewScheduler(nics []nics.Nic) *Scheduler {
	s := &Scheduler{now: time.Now, coolMbps: DefaultCoolMbps}
	s.Update(nics)
	return s
}

// SetAuto makes Tick derive weights from observed throughput instead of the static ones.
func (s *Scheduler) SetAuto(on bool) {
	s.mu.Lock()
	s.auto = on
	s.mu.Unlock()
}

// SetLimit caps an adapter at mbitPerSec Mbit/s across all of its flows and both address families;
// 0 removes the cap. Saved in the scheduler, so an adapter that is unplugged and comes back keeps it.
func (s *Scheduler) SetLimit(name string, mbitPerSec float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limits == nil {
		s.limits = map[string]float64{}
	}
	if mbitPerSec <= 0 {
		delete(s.limits, name)
	} else {
		s.limits[name] = mbitPerSec
	}
	for _, sl := range s.slots {
		if sl.key == name {
			s.applyLimit(sl)
		}
	}
}

// SetLimits replaces every cap, e.g. with what was saved on disk at startup.
func (s *Scheduler) SetLimits(limits map[string]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = make(map[string]float64, len(limits))
	for name, mbitPerSec := range limits {
		if name != "" && mbitPerSec > 0 {
			s.limits[name] = mbitPerSec
		}
	}
	for _, sl := range s.slots {
		s.applyLimit(sl)
	}
}

// Limits returns the caps by adapter name in Mbit/s, for saving and for `net-merge status`.
func (s *Scheduler) Limits() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]float64, len(s.limits))
	for name, mbitPerSec := range s.limits {
		out[name] = mbitPerSec
	}
	return out
}

func normAddr(n nics.Nic) *slotAddr {
	a := &slotAddr{}
	if n.IP != nil {
		a.ip4 = n.IP.To4()
	}
	if n.IP6 != nil && n.IP6.To4() == nil {
		a.ip6 = n.IP6
	}
	return a
}

// Update applies a re-scan: keeps counters of known NICs, picks up changed addresses (DHCP
// renewal, a new IPv6 prefix), adds new ones (a tether plugged in later), and drops vanished
// ones once their last flow ends. Adapters are keyed by name.
func (s *Scheduler) Update(nics []nics.Nic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sl := range s.slots {
		sl.present = false
	}
	for _, n := range nics {
		na := normAddr(n)
		var found *Slot
		for _, sl := range s.slots {
			if sl.key == n.Name {
				found = sl
				break
			}
		}
		if found == nil {
			sl := &Slot{Name: n.Name, Index: n.Index, key: n.Name, weight: [2]float64{n.Weight, n.Weight}, present: true}
			sl.addr.Store(na)
			sl.phone.Store(n.Phone)
			s.applyLimit(sl)
			s.slots = append(s.slots, sl)
			continue
		}
		found.present = true
		found.phone.Store(n.Phone)
		if !s.auto {
			found.weight = [2]float64{n.Weight, n.Weight}
		}
		old := found.addr.Load()
		for _, f := range []Family{Fam4, Fam6} {
			changed := !old.ip4.Equal(na.ip4)
			if f == Fam6 {
				changed = !old.ip6.Equal(na.ip6)
			}
			if changed {
				found.state[f] = stateUnknown
				found.lastProbe[f], found.downUntil[f] = time.Time{}, time.Time{}
				found.peak[f] = 0
				if s.auto {
					found.weight[f] = 1
				}
			}
		}
		if !old.ip4.Equal(na.ip4) || !old.ip6.Equal(na.ip6) {
			found.addr.Store(na)
		}
	}
	kept := s.slots[:0]
	for _, sl := range s.slots {
		if sl.present || sl.active.Load() > 0 {
			kept = append(kept, sl)
		}
	}
	s.slots = kept
}

// A NIC counts as measured once it has carried this much (bytes/s, about 0.4 Mbit/s).
const minMeasuredPeak = 50_000

// Tick samples flow activity and throughput per family. Automatic weights follow
// smoothed capacity estimates (floored at 0.05). Until all links are measured,
// weights stay equal. Quiet links retain capacity, while stale low estimates
// recover up to half the best link's capacity so they can be measured again.
func (s *Scheduler) Tick(dt time.Duration) {
	if dt <= 0 {
		return
	}
	// nics.Octets calls into the OS, so read the counters on a snapshot of the slots without s.mu
	// (it would stall AcquireFor and Update) and take the lock only to store the results.
	s.mu.Lock()
	slots := append([]*Slot(nil), s.slots...)
	s.mu.Unlock()

	type sample struct {
		in, out uint64
		ok      bool
	}

	samples := make([]sample, len(slots))
	for i, sl := range slots {
		in, out, ok := nics.Octets(sl.Index)
		samples[i] = sample{in, out, ok}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	sec := dt.Seconds()
	for i, sl := range slots {
		in, out, ok := samples[i].in, samples[i].out, samples[i].ok
		if !sl.present || !ok {
			sl.linkRateIn, sl.linkRateOut, sl.linkPrimed = 0, 0, false
			continue
		}
		if sl.linkPrimed && in >= sl.linkIn && out >= sl.linkOut { // counters can reset when an adapter reconnects
			sl.linkRateIn = float64(in-sl.linkIn) / sec
			sl.linkRateOut = float64(out-sl.linkOut) / sec
		}
		sl.linkIn, sl.linkOut, sl.linkPrimed = in, out, true
	}
	for _, f := range []Family{Fam4, Fam6} {
		var maxPeak float64
		live, measured := 0, true
		idle := map[*Slot]bool{}
		for _, sl := range s.slots {
			for a := range sl.flows[f] {
				n := a.bytes.Load()
				if n != a.last {
					a.idle = 0
				} else {
					a.idle += dt
				}
				a.last = n
			}
			in, out := sl.bytesIn[f].Load(), sl.bytesOut[f].Load()
			sl.rateIn[f] = float64(in-sl.lastIn[f]) / sec
			sl.rateOut[f] = float64(out-sl.lastOut[f]) / sec
			sl.lastIn[f], sl.lastOut[f] = in, out
			r := sl.rateIn[f] + sl.rateOut[f]
			if r >= minMeasuredPeak && r >= 0.05*sl.peak[f] {
				if sl.peak[f] == 0 {
					sl.peak[f] = r
				} else {
					// Fast but smoothed growth, slower decay. Neither a single burst
					// nor a quiet tick should abruptly invert link preference.
					tau := 2.0
					if r < sl.peak[f] {
						tau = 30
					}
					sl.peak[f] += (r - sl.peak[f]) * (1 - math.Exp(-sec/tau))
				}
			} else {
				idle[sl] = true
			}
			if !sl.present || sl.ip(f) == nil {
				continue
			}
			live++
			maxPeak = math.Max(maxPeak, sl.peak[f])
		}
		// An idle link may have a pessimistic estimate. Let it recover, but never
		// raise it above half the best link just because no traffic is flowing.
		for sl := range idle {
			if sl.present && sl.ip(f) != nil && sl.peak[f] > 0 {
				sl.peak[f] = math.Min(sl.peak[f]*1.03, math.Max(sl.peak[f], 0.5*maxPeak))
			}
		}
		for _, sl := range s.slots {
			if sl.present && sl.ip(f) != nil && sl.peak[f] < minMeasuredPeak {
				measured = false
			}
		}
		if !s.auto {
			continue
		}
		for _, sl := range s.slots {
			if !sl.present || sl.ip(f) == nil {
				continue
			}
			if live < 2 || !measured {
				sl.weight[f] = 1
			} else {
				sl.weight[f] = math.Max(0.05, sl.peak[f]/maxPeak)
			}
		}
	}
	for _, sl := range s.slots {
		s.cool(sl, dt)
	}
}

// Acquire picks an IPv4 NIC for a new flow, skipping the ones in skip. The caller must Release it.
func (s *Scheduler) Acquire(skip map[string]struct{}) *Slot { return s.AcquireFor(Fam4, skip) }
func (s *Scheduler) Release(sl *Slot)                       { s.ReleaseFor(sl, Fam4) }

// AcquireFor picks a NIC that has an address of the given family.
func (s *Scheduler) AcquireFor(f Family, skip map[string]struct{}) *Slot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	// Tiers: 0 = probed up, 1 = not probed yet, 2 = probe failed or link error cooldown.
	// Only the best non-empty tier is used, so a dead tether gets no flows but, if every NIC
	// looks dead, flows still go somewhere rather than nowhere.
	var tiers [3][]*Slot
	for _, sl := range s.slots {
		if !sl.present || sl.ip(f) == nil {
			continue
		}
		if _, no := skip[sl.key]; no {
			continue
		}
		t := 1
		switch {
		case now.Before(sl.downUntil[f]) || sl.state[f] == stateDown:
			t = 2
		case sl.state[f] == stateUp:
			t = 0
		}
		tiers[t] = append(tiers[t], sl)
	}
	var pool []*Slot
	for _, tier := range tiers {
		if len(tier) > 0 {
			pool = tier
			break
		}
	}
	if len(pool) == 0 {
		return nil
	}
	pool = s.standby(pool)
	// Random start so ties don't resonate with the arrival pattern (a rotating counter can
	// lock onto e.g. "every third flow is a short DNS-like one" and starve an adapter).
	start := rand.IntN(len(pool))
	var best *Slot
	bestScore := math.Inf(1)
	for i := range pool {
		sl := pool[(start+i)%len(pool)]
		load := sl.actF[f].Load()
		if s.auto {
			load = sl.busy(f)
		}
		if score := float64(load+1) / sl.weight[f]; score < bestScore {
			best, bestScore = sl, score
		}
	}
	best.active.Add(1)
	best.actF[f].Add(1)
	best.total.Add(1)
	return best
}

// V6Usable reports whether any adapter can currently carry IPv6.
func (s *Scheduler) V6Usable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sl := range s.slots {
		if sl.present && sl.ip(Fam6) != nil && sl.state[Fam6] != stateDown {
			return true
		}
	}
	return false
}

// V6CoversAll reports whether every adapter that can carry IPv4 can also carry IPv6, i.e. whether
// balancing IPv6 flows would use the same set of adapters as IPv4 does. If not, IPv6 would funnel
// everything onto the adapters that have it.
func (s *Scheduler) V6CoversAll() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	any6 := false
	for _, sl := range s.slots {
		if !sl.present {
			continue
		}
		has6 := sl.ip(Fam6) != nil && sl.state[Fam6] != stateDown
		if has6 {
			any6 = true
		}
		if sl.ip(Fam4) != nil && sl.state[Fam4] != stateDown && !has6 {
			return false
		}
	}
	return any6
}

func (s *Scheduler) ReleaseFor(sl *Slot, f Family) {
	sl.active.Add(-1)
	sl.actF[f].Add(-1)
}

// SetState records a probe result. A passing probe also clears any link-error cooldown.
func (s *Scheduler) SetState(sl *Slot, f Family, up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if up {
		sl.state[f] = stateUp
		sl.downUntil[f] = time.Time{}
	} else {
		sl.state[f] = stateDown
	}
}

// Fail sidelines a NIC whose link itself is broken (for one address family).
func (s *Scheduler) Fail(sl *Slot, f Family, cooldown time.Duration) {
	s.mu.Lock()
	sl.downUntil[f] = s.now().Add(cooldown)
	s.mu.Unlock()
	sl.failed.Add(1)
}

func (s *Scheduler) Snapshot() []SlotStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SlotStats, 0, len(s.slots))
	for _, sl := range s.slots {
		a := sl.addr.Load()
		st := SlotStats{
			Name: sl.Name, State: "-", State6: "-", Present: sl.present,
			Weight: sl.weight[Fam4], Weight6: sl.weight[Fam6],
			Active: sl.active.Load(), Total: sl.total.Load(), Failed: sl.failed.Load(),
			BytesIn:  sl.bytesIn[Fam4].Load() + sl.bytesIn[Fam6].Load(),
			BytesOut: sl.bytesOut[Fam4].Load() + sl.bytesOut[Fam6].Load(),
			RateIn:   sl.rateIn[Fam4] + sl.rateIn[Fam6], RateOut: sl.rateOut[Fam4] + sl.rateOut[Fam6],
			LinkIn: sl.linkRateIn, LinkOut: sl.linkRateOut,
			Peak: sl.peak[Fam4], Peak6: sl.peak[Fam6],
			LimitMbps: s.limits[sl.key], Phone: sl.phone.Load(),
		}
		if sl.cooling && (st.LimitMbps <= 0 || s.coolMbps < st.LimitMbps) {
			st.CoolMbps = s.coolMbps
		}
		if a.ip4 != nil {
			st.IP, st.State = a.ip4.String(), sl.state[Fam4].String()
		}
		if a.ip6 != nil {
			st.IP6, st.State6 = a.ip6.String(), sl.state[Fam6].String()
		}
		out = append(out, st)
	}
	return out
}
