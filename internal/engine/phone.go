package engine

import (
	"math"
	"time"
)

// Phone saver. To the scheduler a phone sharing its mobile data is a link like any other, so it
// gets its share of every flow, its radio never goes quiet, and during a long download it runs
// flat out for as long as the download lasts. A phone has no fan: that is what makes it hot.
// With the saver on, a phone link is treated differently in four ways:
//
//   - Standby. While the other links have room, new flows stay off the phone (see standby), so
//     a mail check or a chat message does not wake the mobile radio. As soon as several flows
//     run at once, which is when a second link adds speed, the phone gets its normal share.
//   - Cooling. After coolBurst of heavy use the phone is held to the cool rate until it has
//     had a rest (see cool). Short downloads still get its full speed.
//   - Probes. A phone that is up is probed every probeEveryPhone instead of every 15 s, and not
//     at all while traffic keeps arriving over it, which proves the same thing.
//   - Keep-alives. Idle connections pinned to the phone are kept alive every phoneKeepAlive
//     instead of every 30 s.
//
// None of this needs anything from the phone: it cannot report its temperature to the computer,
// so the saver limits what causes the heat instead of measuring it.
const (
	// standbyFlows and standbyRate say when the other links count as busy: this many flows
	// moving data or connecting, or this much throughput (bytes/s, 1 Mbit/s). The rate is there
	// for UDP (QUIC), whose flows hold no slot and so are not counted.
	standbyFlows = 3
	standbyRate  = 125_000

	// DefaultCoolMbps is what a phone is held to once it has worked for coolBurst, in Mbit/s.
	DefaultCoolMbps = 20
	// coolBurst is how long a phone may run above the cool rate before it is held back. A phone
	// takes several minutes to heat up, so short bursts are free.
	coolBurst = 3 * time.Minute
	// coolRest is how many seconds of rest pay back one second of work.
	coolRest = 2
	// coolWorking is the share of the cool rate from which a second counts as work. Below 1 so
	// that a phone held at the cool rate, and still wanted at full speed, stays held.
	coolWorking = 0.8

	probeEveryPhone = 2 * time.Minute
	phoneKeepAlive  = 2 * time.Minute
	linkKeepAlive   = 30 * time.Second
)

// SetPhoneSaver switches the phone saver on or off. Off, a phone is a link like any other.
func (s *Scheduler) SetPhoneSaver(on bool) { s.saver.Store(on) }
func (s *Scheduler) PhoneSaver() bool      { return s.saver.Load() }

// SetCoolRate sets what a phone is held to after coolBurst of heavy use, in Mbit/s. 0 never
// holds it back.
func (s *Scheduler) SetCoolRate(mbitPerSec float64) {
	s.mu.Lock()
	s.coolMbps = math.Max(0, mbitPerSec)
	s.mu.Unlock()
}

// saving reports whether the saver applies to this link. Lock-free, for the data path.
func (s *Scheduler) saving(sl *Slot) bool { return sl.phone.Load() && s.saver.Load() }

// keepAlive is the TCP keep-alive period for an upstream socket on this link.
func (s *Scheduler) keepAlive(sl *Slot) time.Duration {
	if s.saving(sl) {
		return phoneKeepAlive
	}
	return linkKeepAlive
}

// standby takes the phones out of a pool while the other links in it have room. With only
// phones in the pool, or once the others are busy, the pool is returned as it is: weighted
// least-busy then evens the flows out, so parallel downloads still use every link in full.
// The caller holds s.mu.
func (s *Scheduler) standby(pool []*Slot) []*Slot {
	if !s.saver.Load() {
		return pool
	}
	var others []*Slot
	var busy int64
	var rate float64
	for _, sl := range pool {
		if sl.phone.Load() {
			continue
		}
		others = append(others, sl)
		busy += sl.busy(Fam4) + sl.busy(Fam6)
		rate += sl.rate()
	}
	if len(others) == 0 || len(others) == len(pool) || busy >= standbyFlows || rate >= standbyRate {
		return pool
	}
	return others
}

// rate is what net-merge relayed over this link in the last tick, both families, in bytes/s.
// The caller holds Scheduler.mu.
func (sl *Slot) rate() float64 {
	return sl.rateIn[Fam4] + sl.rateOut[Fam4] + sl.rateIn[Fam6] + sl.rateOut[Fam6]
}

// cool keeps a phone from working flat out for long. Each tick above the cool rate adds to the
// link's heat and each tick below it takes half as much away. At coolBurst the link is capped
// at the cool rate, and the cap goes once half of the heat is gone. A long download therefore
// gets the phone's full speed for the first minutes and the cool rate after that, and a phone
// that had a rest gets its burst back. The caller holds s.mu.
func (s *Scheduler) cool(sl *Slot, dt time.Duration) {
	if !sl.present || !s.saving(sl) || s.coolMbps <= 0 {
		if sl.heat != 0 || sl.cooling {
			sl.heat, sl.cooling = 0, false
			s.applyLimit(sl)
		}
		return
	}
	// The OS's counters include apps that bind to the phone directly and bypass net-merge.
	rate := math.Max(sl.rate(), sl.linkRateIn+sl.linkRateOut)
	coolRate := s.coolMbps * bytesPerMbit
	if rate >= coolWorking*coolRate {
		sl.heat = min(coolBurst, sl.heat+dt)
	} else {
		sl.heat = max(0, sl.heat-dt/coolRest)
	}
	switch {
	case !sl.cooling && sl.heat >= coolBurst:
		sl.cooling = true
		// The estimate would take a minute to follow the cap down. Until then the phone would
		// keep its old share of the new flows, and each of them would crawl.
		for f := range sl.peak {
			sl.peak[f] = math.Min(sl.peak[f], coolRate)
		}
	case sl.cooling && sl.heat <= coolBurst/2:
		sl.cooling = false
	}
	s.applyLimit(sl)
}

// applyLimit installs the lower of the cap the user set and the cool rate of a phone that is
// cooling. The buckets are only replaced when the rate changes. The caller holds s.mu.
func (s *Scheduler) applyLimit(sl *Slot) {
	want := s.limits[sl.key]
	if sl.cooling && (want <= 0 || s.coolMbps < want) {
		want = s.coolMbps
	}
	if want != sl.limitMbps() {
		sl.setLimit(want)
	}
}

// Suspect asks for a fresh probe of a link on which a connection just timed out. A phone on
// standby is probed rarely, and without this a phone that lost its signal would keep making
// new connections wait for the connect timeout until its next probe.
func (s *Scheduler) Suspect(sl *Slot, f Family) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sl.state[f] == stateUp && s.now().Sub(sl.lastProbe[f]) >= probeEveryUp {
		sl.suspect[f] = true
	}
}
