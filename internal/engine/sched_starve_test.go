package engine

import (
	"testing"
	"time"

	"net-merge/internal/nics"
)

// Two equal links. A happens to get a fast download first while B only carries light traffic, so B
// looks 160x slower. B must not be written off: a download manager's new connections still reach
// it. (With the old 0.05 floor B got none of 8, and kept getting none until merging was paused.)
func TestLightlyUsedLinkIsNotStarved(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	A, B := s.slots[0], s.slots[1]
	open := func(sl *Slot) *flowActivity { sl.actF[Fam4].Add(1); sl.active.Add(1); return s.track(sl, Fam4) }
	dlA, webB := open(A), open(B)
	for i := 0; i < 5; i++ {
		A.bytesIn[Fam4].Add(10_000_000) // 10 MB/s on A's download
		dlA.bytes.Add(10_000_000)
		B.bytesIn[Fam4].Add(60_000) // 60 KB/s of web pages on B
		webB.bytes.Add(60_000)
		s.Tick(time.Second)
	}
	got := map[string]int{}
	for i := 0; i < 8; i++ {
		sl := s.AcquireFor(Fam4, nil)
		got[sl.Name]++
		s.track(sl, Fam4) // AcquireFor already counted it
	}
	if got["B"] < 2 {
		t.Fatalf("B got %d of 8 new connections (weights A=%.2f B=%.2f)", got["B"], A.weight[Fam4], B.weight[Fam4])
	}
}

// Traffic net-merge can't tie to a busy flow (UDP: QUIC, games, DNS) must not lower a link's
// estimate while the other link is moving data on more flows.
func TestFewerFlowsDoNotLowerEstimate(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	A, B := s.slots[0], s.slots[1]
	A.peak[Fam4], B.peak[Fam4] = 10e6, 10e6
	var flows []*flowActivity
	for range 4 {
		A.actF[Fam4].Add(1)
		flows = append(flows, s.track(A, Fam4))
	}
	for range 30 {
		for _, a := range flows {
			a.bytes.Add(2_500_000)
		}
		A.bytesIn[Fam4].Add(10_000_000)
		B.bytesIn[Fam4].Add(1_000_000) // UDP only, no busy TCP flow
		s.Tick(time.Second)
	}
	if B.peak[Fam4] < 9e6 {
		t.Fatalf("B's estimate fell to %.0f from traffic on fewer flows than A", B.peak[Fam4])
	}
}
