package engine

import (
	"math"
	"time"
)

// Phones. To the scheduler a phone sharing its mobile data is a link like any other, so its
// radio is woken by every probe and keep-alive and by any flow that lands on it. A phone has no
// fan, and a radio that never rests is what makes it warm. For a link marked as a phone
// (nics.Nic.Phone), three things always apply, because none of them costs any speed:
//
//   - Probes. A phone that is up is probed every probeEveryPhone instead of every 15 s, and not
//     at all while traffic keeps arriving over it, which proves the same thing. A connection
//     that times out brings the probe forward (see Suspect).
//   - Keep-alives. Idle connections pinned to the phone are kept alive every phoneKeepAlive
//     instead of every 30 s.
//   - Standby. While the other links have room and are at least as fast as the phone, new flows
//     stay off it, so a mail check or a chat message does not wake its radio (see standby). A
//     phone that is faster, or not measured yet, gets its normal share.
//
// Cooling is opt-in (SetPhoneCooling, off by default): after coolBurst of heavy use a phone is
// held to the cool rate until it has had a rest (see cool). It costs speed, so it is the user's
// choice. The phone cannot report its temperature to the computer, so all of this limits what
// causes the heat instead of measuring it.
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

// SetPhoneCooling switches cooling on or off. Off (the default), a phone keeps its full speed.
func (s *Scheduler) SetPhoneCooling(on bool) { s.coolOn.Store(on) }
func (s *Scheduler) PhoneCooling() bool      { return s.coolOn.Load() }

// SetCoolRate sets what a phone is held to after coolBurst of heavy use, in Mbit/s. 0 never
// holds it back.
func (s *Scheduler) SetCoolRate(mbitPerSec float64) {
	s.mu.Lock()
	s.coolMbps = math.Max(0, mbitPerSec)
	s.mu.Unlock()
}

// CoolRate is the rate SetCoolRate set, in Mbit/s.
func (s *Scheduler) CoolRate() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.coolMbps
}

// keepAlive is the TCP keep-alive period for an upstream socket on this link.
func (s *Scheduler) keepAlive(sl *Slot) time.Duration {
	if sl.phone.Load() {
		return phoneKeepAlive
	}
	return linkKeepAlive
}

// standby takes the phones out of a pool for a new flow of family f, but only when that costs
// no speed: the phones have been measured, and other links in the pool are at least as fast as
// the fastest of them and have room. The flow then goes to one of those links. Otherwise the
// pool is returned as it is, so a phone that is faster than the line it is paired with (5G next
// to slow broadband) still gets single-connection downloads, and once the other links are busy
// weighted least-busy evens the flows out over every link. The caller holds s.mu.
func (s *Scheduler) standby(pool []*Slot, f Family) []*Slot {
	var phone float64
	phones := 0
	for _, sl := range pool {
		if sl.phone.Load() {
			phone = math.Max(phone, sl.peak[f])
			phones++
		}
	}
	if phones == 0 || phones == len(pool) || phone < minMeasuredPeak {
		return pool
	}
	var others []*Slot
	var busy int64
	var rate float64
	for _, sl := range pool {
		if sl.phone.Load() || sl.peak[f] < phone {
			continue
		}
		others = append(others, sl)
		busy += sl.busy(Fam4) + sl.busy(Fam6)
		rate += sl.rate()
	}
	if len(others) == 0 || busy >= standbyFlows || rate >= standbyRate {
		return pool
	}
	return others
}

// rate is what net-merge relayed over this link in the last tick, both families, in bytes/s.
// The caller holds Scheduler.mu.
func (sl *Slot) rate() float64 {
	return sl.rateIn[Fam4] + sl.rateOut[Fam4] + sl.rateIn[Fam6] + sl.rateOut[Fam6]
}

// cool keeps a phone from working flat out for long, when cooling is on. Each tick above the
// cool rate adds to the link's heat and each tick below it takes half as much away. At coolBurst
// the link is capped at the cool rate, and the cap goes once half of the heat is gone. A long
// download therefore gets the phone's full speed for the first minutes and the cool rate after
// that, and a phone that had a rest gets its burst back. The caller holds s.mu.
func (s *Scheduler) cool(sl *Slot, dt time.Duration) {
	if !sl.present || !sl.phone.Load() || !s.coolOn.Load() || s.coolMbps <= 0 {
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

// Suspect asks for a fresh probe of a link on which a connection just timed out. A phone is
// probed rarely, and without this a phone that lost its signal would keep making new
// connections wait for the connect timeout until its next probe. The timeout is also what lets
// that probe's failure count while the other family keeps the link busy (see SetState).
func (s *Scheduler) Suspect(sl *Slot, f Family) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl.timedOut[f] = s.now()
	if sl.state[f] == stateUp && s.now().Sub(sl.lastProbe[f]) >= probeEveryUp {
		sl.suspect[f] = true
	}
}
