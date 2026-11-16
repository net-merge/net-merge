package engine

import (
	"testing"
	"time"

	"net-merge/internal/nics"
)

// wifiAndPhone is a scheduler with the saver on, a Wi-Fi link and an iPhone hotspot.
func wifiAndPhone() (s *Scheduler, wifi, phone *Slot) {
	p := nic("iPhone", "172.20.10.2", 1)
	p.Phone = true
	s = NewScheduler([]nics.Nic{nic("Wi-Fi", "10.0.0.1", 1), p})
	s.SetPhoneSaver(true)
	return s, s.slots[0], s.slots[1]
}

func TestLightTrafficStaysOffThePhone(t *testing.T) {
	s, wifi, _ := wifiAndPhone()
	for i := range 20 { // one flow at a time: a mail check, a chat message, a DNS lookup
		if got := s.Acquire(nil); got != wifi {
			t.Fatalf("flow %d went to %s while Wi-Fi was idle", i, got.Name)
		}
		s.Release(wifi)
	}
}

func TestParallelFlowsStillUseThePhoneInFull(t *testing.T) {
	s, _, _ := wifiAndPhone()
	p := acquireN(s, 8) // a download manager: 8 connections at once
	if w, ph := countBy(p, "Wi-Fi"), countBy(p, "iPhone"); w != 4 || ph != 4 {
		t.Fatalf("want 4/4 once the links are busy, got Wi-Fi=%d iPhone=%d", w, ph)
	}
	for i, sl := range p[:standbyFlows] {
		if sl.Name != "Wi-Fi" {
			t.Fatalf("flow %d went to the phone before Wi-Fi was busy", i)
		}
	}
}

func TestThroughputOnTheOtherLinksBringsThePhoneIn(t *testing.T) {
	s, wifi, _ := wifiAndPhone()
	wifi.rateIn[Fam4] = standbyRate // a QUIC download: data moves but no TCP flow is counted
	p := acquireN(s, 2)
	if countBy(p, "iPhone") != 1 {
		t.Fatalf("want one of two flows on the phone while Wi-Fi is busy, got %d", countBy(p, "iPhone"))
	}
}

func TestPhoneIsUsedWhenItIsTheOnlyWay(t *testing.T) {
	s, wifi, phone := wifiAndPhone()
	if got := s.Acquire(map[string]struct{}{"Wi-Fi": {}}); got != phone {
		t.Fatal("a retry that excludes Wi-Fi must fall over to the phone")
	}
	s.Release(phone)
	s.SetState(phone, Fam4, true)
	s.SetState(wifi, Fam4, false)
	if got := s.Acquire(nil); got != phone {
		t.Fatal("with Wi-Fi down the phone must carry the flow")
	}
}

func TestSaverOffTreatsThePhoneLikeAnyLink(t *testing.T) {
	s, _, _ := wifiAndPhone()
	s.SetPhoneSaver(false)
	p := make([]*Slot, 20)
	for i := range p {
		p[i] = s.Acquire(nil)
		s.Release(p[i])
	}
	if countBy(p, "iPhone") == 0 {
		t.Fatal("with the saver off single flows must reach the phone too")
	}
}

// run feeds mbit Mbit/s through the slot for d, one tick per second.
func run(s *Scheduler, sl *Slot, mbit float64, d time.Duration) {
	for range int(d / time.Second) {
		sl.bytesIn[Fam4].Add(int64(mbit * bytesPerMbit))
		s.Tick(time.Second)
	}
}

func coolOf(s *Scheduler, name string) float64 {
	for _, st := range s.Snapshot() {
		if st.Name == name {
			return st.CoolMbps
		}
	}
	return -1
}

func TestLongHeavyUseCoolsThePhone(t *testing.T) {
	s, wifi, phone := wifiAndPhone()

	run(s, phone, 100, coolBurst-time.Second)
	if phone.limitMbps() != 0 {
		t.Fatal("the phone was held back before its burst was used up")
	}
	run(s, phone, 100, time.Second)
	if got := phone.limitMbps(); got != DefaultCoolMbps {
		t.Fatalf("after %s of heavy use the phone has limit %v, want %v", coolBurst, got, float64(DefaultCoolMbps))
	}
	if got := coolOf(s, "iPhone"); got != DefaultCoolMbps {
		t.Fatalf("snapshot says cooling at %v, want %v", got, float64(DefaultCoolMbps))
	}
	if phone.peak[Fam4] > DefaultCoolMbps*bytesPerMbit {
		t.Fatalf("the speed estimate (%v) must come down with the cap, or new flows pile onto a slow link", phone.peak[Fam4])
	}

	// Still wanted at full speed: it runs at the cool rate and stays there.
	run(s, phone, DefaultCoolMbps, 10*time.Minute)
	if phone.limitMbps() != DefaultCoolMbps {
		t.Fatal("the phone got its full speed back without a rest")
	}

	// A rest of half the burst's cost gives it back.
	run(s, phone, 0, coolBurst*coolRest/2-time.Second)
	if phone.limitMbps() != DefaultCoolMbps {
		t.Fatal("the phone was released before it had rested")
	}
	run(s, phone, 0, time.Second)
	if phone.limitMbps() != 0 || coolOf(s, "iPhone") != 0 {
		t.Fatal("a rested phone must get its full speed back")
	}

	run(s, wifi, 500, 2*coolBurst)
	if wifi.limitMbps() != 0 {
		t.Fatal("only phones are held back")
	}
}

func TestShortBurstsNeverCoolThePhone(t *testing.T) {
	s, _, phone := wifiAndPhone()
	for range 30 { // a minute of work, two of rest, for an hour and a half
		run(s, phone, 100, time.Minute)
		run(s, phone, 0, 2*time.Minute)
		if phone.limitMbps() != 0 {
			t.Fatal("bursts with enough rest in between must keep the phone's full speed")
		}
	}
}

func TestCoolingKeepsTheLowerOfTheTwoCaps(t *testing.T) {
	s, _, phone := wifiAndPhone()
	s.SetLimit("iPhone", 50)
	run(s, phone, 50, coolBurst)
	if got := phone.limitMbps(); got != DefaultCoolMbps {
		t.Fatalf("cooling with a 50 Mbit/s cap: limit %v, want %v", got, float64(DefaultCoolMbps))
	}
	if got := s.Limits()["iPhone"]; got != 50 {
		t.Fatalf("cooling must not change the saved cap, got %v", got)
	}
	s.SetLimit("iPhone", 5)
	if got := phone.limitMbps(); got != 5 {
		t.Fatalf("a cap below the cool rate must win, got %v", got)
	}
	if got := coolOf(s, "iPhone"); got != 0 {
		t.Fatalf("with the user's cap in charge the snapshot must not say cooling, got %v", got)
	}
	s.SetLimit("iPhone", 50)
	run(s, phone, 0, coolBurst*coolRest/2)
	if got := phone.limitMbps(); got != 50 {
		t.Fatalf("after the rest the user's cap must be back, got %v", got)
	}
}

func TestSaverOffOrZeroRateEndsCooling(t *testing.T) {
	for name, off := range map[string]func(*Scheduler){
		"saver off": func(s *Scheduler) { s.SetPhoneSaver(false) },
		"rate 0":    func(s *Scheduler) { s.SetCoolRate(0) },
	} {
		t.Run(name, func(t *testing.T) {
			s, _, phone := wifiAndPhone()
			run(s, phone, 100, coolBurst)
			if phone.limitMbps() == 0 {
				t.Fatal("setup: the phone should be cooling")
			}
			off(s)
			run(s, phone, 100, time.Second)
			if phone.limitMbps() != 0 {
				t.Fatal("the cap must go at once")
			}
			run(s, phone, 100, 2*coolBurst)
			if phone.limitMbps() != 0 {
				t.Fatal("the phone must not be held back again")
			}
		})
	}
}

func TestIdlePhoneIsProbedRarely(t *testing.T) {
	s, wifi, phone := wifiAndPhone()
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	due := func() map[*Slot]bool {
		got := map[*Slot]bool{}
		for _, j := range s.probeDue() {
			got[j.sl] = true
			j.sl.probing[j.f].Store(false)
		}
		return got
	}
	if got := due(); !got[wifi] || !got[phone] {
		t.Fatal("new adapters are probed at once, phones too")
	}
	s.SetState(wifi, Fam4, true)
	s.SetState(phone, Fam4, true)

	clock = clock.Add(probeEveryUp)
	if got := due(); !got[wifi] || got[phone] {
		t.Fatalf("after %s: Wi-Fi probed %v (want true), phone probed %v (want false)", probeEveryUp, got[wifi], got[phone])
	}
	clock = clock.Add(probeEveryPhone - probeEveryUp)
	if !due()[phone] {
		t.Fatalf("an idle phone must still be probed every %s", probeEveryPhone)
	}

	// Traffic that arrives over the phone proves what a probe would.
	phone.bytesIn[Fam4].Add(1000)
	clock = clock.Add(probeEveryPhone)
	if due()[phone] {
		t.Fatal("a phone that is carrying traffic must not be probed")
	}
	clock = clock.Add(probeEveryPhone)
	if !due()[phone] {
		t.Fatal("once the traffic stops the probes must come back")
	}

	// A connection that timed out brings the probe forward, but not more often than any link's.
	clock = clock.Add(probeEveryUp - time.Second)
	s.Suspect(phone, Fam4)
	if due()[phone] {
		t.Fatal("a phone probed moments ago must not be probed again")
	}
	clock = clock.Add(time.Second)
	s.Suspect(phone, Fam4)
	if !due()[phone] {
		t.Fatal("a timed-out connection must bring the phone's probe forward")
	}
	clock = clock.Add(probeEveryUp)
	if due()[phone] {
		t.Fatal("one suspicion buys one probe")
	}

	// A phone with no internet is probed as often as any dead link, so it comes back quickly.
	s.SetState(phone, Fam4, false)
	clock = clock.Add(probeEveryDown)
	if !due()[phone] {
		t.Fatal("a dead phone must be probed often")
	}

	s.SetState(phone, Fam4, true)
	s.SetPhoneSaver(false)
	clock = clock.Add(probeEveryUp)
	if !due()[phone] {
		t.Fatal("with the saver off the phone is probed like any link")
	}
}

func TestPhoneKeepAliveIsLonger(t *testing.T) {
	s, wifi, phone := wifiAndPhone()
	if got := s.keepAlive(wifi); got != linkKeepAlive {
		t.Fatalf("Wi-Fi keep-alive %s, want %s", got, linkKeepAlive)
	}
	if got := s.keepAlive(phone); got != phoneKeepAlive {
		t.Fatalf("phone keep-alive %s, want %s", got, phoneKeepAlive)
	}
	s.SetPhoneSaver(false)
	if got := s.keepAlive(phone); got != linkKeepAlive {
		t.Fatalf("saver off: phone keep-alive %s, want %s", got, linkKeepAlive)
	}
}

func TestUpdateFollowsThePhoneMark(t *testing.T) {
	s, _, phone := wifiAndPhone()
	// The same adapter name on another network: no longer a phone.
	s.Update([]nics.Nic{nic("Wi-Fi", "10.0.0.1", 1), nic("iPhone", "192.168.1.7", 1)})
	if phone.phone.Load() {
		t.Fatal("the phone mark must follow the re-scan")
	}
	if st := s.Snapshot()[1]; st.Phone {
		t.Fatal("snapshot still says phone")
	}
}

// The same through real sockets: the SOCKS5 front-end, the balancer and the relay.
func TestPhoneStandbyThroughTheProxy(t *testing.T) {
	port := startTarget(t)
	phone := nic("iPhone", "127.0.0.3", 1)
	phone.Phone = true
	sched, proxy := startProxy(t, []nics.Nic{nic("Wi-Fi", "127.0.0.2", 1), phone})
	sched.SetPhoneSaver(true)

	idle := func() bool {
		for _, st := range sched.Snapshot() {
			if st.Active != 0 {
				return false
			}
		}
		return true
	}
	for i := range 6 { // one connection at a time
		c := socksConnect(t, proxy, port)
		if c.rep != 0 {
			t.Fatalf("connect failed rep=%d", c.rep)
		}
		if got := c.line(t); got != "127.0.0.2" {
			t.Fatalf("lone flow %d left through %s, want Wi-Fi (127.0.0.2)", i, got)
		}
		c.Close()
		for deadline := time.Now().Add(2 * time.Second); !idle(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the closed flow was never released")
			}
		}
	}
	seen := map[string]int{}
	for range 8 { // eight open at once
		c := socksConnect(t, proxy, port)
		if c.rep != 0 {
			t.Fatalf("connect failed rep=%d", c.rep)
		}
		seen[c.line(t)]++
	}
	if seen["127.0.0.2"] != 4 || seen["127.0.0.3"] != 4 {
		t.Fatalf("want 4/4 with eight flows open, got %v", seen)
	}
}
