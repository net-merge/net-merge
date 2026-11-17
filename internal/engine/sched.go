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

type Family int

const (
	Fam4 Family = iota
	Fam6
)

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

type Slot struct {
	Name  string
	Index int
	key   string
	addr  atomic.Pointer[slotAddr]

	active, total, failed atomic.Int64
	actF                  [2]atomic.Int64
	bytesIn, bytesOut     [2]atomic.Int64
	probing               [2]atomic.Bool
	limit                 atomic.Pointer[limitPair]

	// guarded by Scheduler.mu
	weight          [2]float64
	present         bool
	state           [2]linkState
	lastProbe       [2]time.Time
	downUntil       [2]time.Time
	lastIn, lastOut [2]int64
	rateIn, rateOut [2]float64
	peak            [2]float64
	flows           [2]map[*flowActivity]struct{}

	// Exponentially weighted moving average (EWMA) of physical RTT and jitter
	rttMs    [2]float64
	jitterMs [2]float64

	// Adaptive Anti-Bufferbloat Control (AIMD) state
	baseRTTMs      [2]float64
	dynamicCapMbps float64

	linkIn, linkOut         uint64
	linkRateIn, linkRateOut float64
	linkPrimed              bool
}

type flowActivity struct {
	bytes atomic.Int64
	last  int64
	idle  time.Duration
}

const (
	flowIdleAfter     = 2 * time.Second
	bulkFlowThreshold = 256 * 1024 // Flows transferring >= 256 KB are classified as bulk downloads

	// Adaptive rate limiter boundaries (Mbps)
	initialCapMbps = 8.0
	minCapMbps     = 10.0
	maxCapMbps     = 300.0
)

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
	n := max(int64(0), sl.actF[f].Load()-int64(len(sl.flows[f])))
	for a := range sl.flows[f] {
		if a.idle < flowIdleAfter {
			n++
		}
	}
	return n
}

func (sl *Slot) bulkCount(f Family) int64 {
	var n int64
	for a := range sl.flows[f] {
		if a.idle < flowIdleAfter && a.bytes.Load() >= bulkFlowThreshold {
			n++
		}
	}
	return n
}

// latencyScore computes an effective latency cost (RTT + 2*Jitter) to penalize unstable links.
func (sl *Slot) latencyScore(f Family) float64 {
	if sl.rttMs[f] <= 0 {
		return 999.0
	}
	return sl.rttMs[f] + 2.0*sl.jitterMs[f]
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
	State             string
	State6            string
	Weight, Weight6   float64
	Present           bool
	Active, Total     int64
	Failed            int64
	BytesIn, BytesOut int64
	RateIn, RateOut   float64
	LinkIn, LinkOut   float64
	Peak, Peak6       float64
	LimitMbps         float64
	RTTMs             float64
	JitterMs          float64
}

type Scheduler struct {
	mu        sync.Mutex
	slots     []*Slot
	auto      bool
	smartMode atomic.Bool
	limits    map[string]float64
	now       func() time.Time
}

func NewScheduler(nics []nics.Nic) *Scheduler {
	s := &Scheduler{now: time.Now}
	s.smartMode.Store(true) // Enable Low-Latency Gaming Mode by default
	s.Update(nics)
	return s
}

func (s *Scheduler) SetSmartMode(on bool) { s.smartMode.Store(on) }
func (s *Scheduler) IsSmartMode() bool    { return s.smartMode.Load() }

func (s *Scheduler) SetAuto(on bool) {
	s.mu.Lock()
	s.auto = on
	s.mu.Unlock()
}

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
			sl.setLimit(mbitPerSec)
		}
	}
}

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
		sl.setLimit(s.limits[sl.key])
	}
}

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
			sl := &Slot{
				Name:           n.Name,
				Index:          n.Index,
				key:            n.Name,
				weight:         [2]float64{n.Weight, n.Weight},
				present:        true,
				dynamicCapMbps: initialCapMbps,
			}
			sl.addr.Store(na)
			sl.setLimit(s.limits[n.Name])
			s.slots = append(s.slots, sl)
			continue
		}
		found.present = true
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
				found.rttMs[f], found.jitterMs[f] = 0, 0
				found.baseRTTMs[f] = 0
				found.dynamicCapMbps = initialCapMbps
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

const minMeasuredPeak = 50_000

func (s *Scheduler) Tick(dt time.Duration) {
	if dt <= 0 {
		return
	}
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
		if sl.linkPrimed && in >= sl.linkIn && out >= sl.linkOut {
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
				// Prevent a fast initial link from starving secondary adapters (floor at 0.01)
				sl.weight[f] = math.Max(0.01, sl.peak[f]/maxPeak)
			}
		}
	}

	// Adaptive Active Bandwidth Reservation (AIMD Anti-Bufferbloat Controller):
	// Dynamically adjusts the primary low-latency adapter's bulk download cap based on
	// real-time RTT deviation from its baseline RTT, adapting automatically to any link speed.
	var fastestNIC *Slot
	minLat := math.Inf(1)
	live4 := 0
	for _, sl := range s.slots {
		if sl.present && sl.ip(Fam4) != nil && sl.state[Fam4] != stateDown {
			live4++
			if sc := sl.latencyScore(Fam4); sc < minLat {
				fastestNIC, minLat = sl, sc
			}
		}
	}
	for _, sl := range s.slots {
		if s.smartMode.Load() && live4 >= 2 && sl == fastestNIC {
			if sl.dynamicCapMbps <= 0 {
				sl.dynamicCapMbps = initialCapMbps
			}
			rtt := sl.rttMs[Fam4]
			if rtt > 0 {
				if sl.baseRTTMs[Fam4] <= 0 || rtt < sl.baseRTTMs[Fam4] {
					sl.baseRTTMs[Fam4] = rtt
				} else {
					// Slow upward drift allows baseline to adapt if network path changes
					sl.baseRTTMs[Fam4] += 0.02 * sec
				}

				base := sl.baseRTTMs[Fam4]
				downMbps := (sl.rateIn[Fam4] * 8.0) / 1e6

				if rtt > base+15.0 || rtt > base*1.6 {
					// Multiplicative Decrease: RTT spike detected -> cut cap by 20% immediately
					sl.dynamicCapMbps = math.Max(minCapMbps, sl.dynamicCapMbps*0.80)
				} else if rtt <= base+8.0 && downMbps >= sl.dynamicCapMbps*0.75 {
					// Additive Increase: latency is clean and link is actively utilized -> probe +2.5 Mbps/s
					sl.dynamicCapMbps = math.Min(maxCapMbps, sl.dynamicCapMbps+2.5*sec)
				}
			}
			sl.setLimit(sl.dynamicCapMbps)
		} else {
			sl.dynamicCapMbps = initialCapMbps
			sl.setLimit(s.limits[sl.key])
		}
	}
}

func (s *Scheduler) BestLowLatencyName(f Family) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Slot
	bestScore := math.Inf(1)
	for _, sl := range s.slots {
		if !sl.present || sl.ip(f) == nil || sl.state[f] == stateDown {
			continue
		}
		if sc := sl.latencyScore(f); sc < bestScore {
			best, bestScore = sl, sc
		}
	}
	if best == nil {
		return ""
	}
	return best.Name
}

// AcquireLowLatency selects the adapter with the lowest RTT + 2*Jitter score,
// dedicated to latency-sensitive UDP traffic (games, VoIP).
func (s *Scheduler) AcquireLowLatency(f Family, skip map[string]struct{}) *Slot {
	if !s.smartMode.Load() {
		return s.AcquireFor(f, skip)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var best *Slot
	bestScore := math.Inf(1)
	for _, sl := range s.slots {
		if !sl.present || sl.ip(f) == nil {
			continue
		}
		if _, no := skip[sl.key]; no {
			continue
		}
		if now.Before(sl.downUntil[f]) || sl.state[f] == stateDown {
			continue
		}
		if sc := sl.latencyScore(f); sc < bestScore {
			best, bestScore = sl, sc
		}
	}
	if best != nil {
		best.active.Add(1)
		best.actF[f].Add(1)
		best.total.Add(1)
		return best
	}
	return nil
}

func (s *Scheduler) Acquire(skip map[string]struct{}) *Slot { return s.AcquireFor(Fam4, skip) }
func (s *Scheduler) Release(sl *Slot)                       { s.ReleaseFor(sl, Fam4) }

func (s *Scheduler) AcquireFor(f Family, skip map[string]struct{}) *Slot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
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

	var fastestNIC *Slot
	minLat := math.Inf(1)
	if s.smartMode.Load() && len(pool) >= 2 {
		for _, sl := range pool {
			if sc := sl.latencyScore(f); sc < minLat {
				fastestNIC, minLat = sl, sc
			}
		}
	}

	start := rand.IntN(len(pool))
	var best *Slot
	bestScore := math.Inf(1)
	for i := range pool {
		sl := pool[(start+i)%len(pool)]
		load := sl.actF[f].Load()
		if s.auto {
			load = sl.busy(f)
		}
		score := float64(load+1) / sl.weight[f]

		// Favor the protected low-latency primary lane for new short/interactive TCP connections
		// while offloading excess bulk flows to secondary adapters.
		if s.smartMode.Load() && fastestNIC != nil && sl == fastestNIC {
			if bulk := sl.bulkCount(f); bulk >= 4 {
				score += float64(bulk) * 1.5
			} else {
				score *= 0.5
			}
		}

		if score < bestScore {
			best, bestScore = sl, score
		}
	}
	best.active.Add(1)
	best.actF[f].Add(1)
	best.total.Add(1)
	return best
}

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

func (s *Scheduler) SetState(sl *Slot, f Family, up bool) {
	s.SetStateWithRTT(sl, f, up, 0)
}

func (s *Scheduler) SetStateWithRTT(sl *Slot, f Family, up bool, rttMs float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if up {
		sl.state[f] = stateUp
		sl.downUntil[f] = time.Time{}
		if rttMs > 0 {
			if sl.rttMs[f] <= 0 {
				sl.rttMs[f] = rttMs
				sl.jitterMs[f] = 0
			} else {
				diff := math.Abs(rttMs - sl.rttMs[f])
				sl.jitterMs[f] = sl.jitterMs[f]*0.7 + diff*0.3
				sl.rttMs[f] = sl.rttMs[f]*0.7 + rttMs*0.3
			}
		}
	} else {
		sl.state[f] = stateDown
		sl.rttMs[f] = 0
		sl.baseRTTMs[f] = 0
	}
}

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
			LimitMbps: sl.limitMbps(),
			RTTMs:     sl.rttMs[Fam4],
			JitterMs:  sl.jitterMs[Fam4],
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

