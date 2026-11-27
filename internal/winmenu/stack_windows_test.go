package winmenu

import (
	"runtime"
	"sync"
	"testing"
)

// The COM calls hand Direct2D and DirectWrite the addresses of Go locals as uintptr. Unless the call
// wrapper is marked //go:uintptrescapes, those locals can sit on the goroutine's stack, and if the stack
// is moved between taking the address and the call, COM reads or writes the old copy: a measured width
// comes back 0 and a text layout comes back nil. Whether the stack moves there depends on how deep the
// caller already is, so this measures from fresh goroutines at many depths.

var stackSink byte

//go:noinline
func deeper(n int, f func()) {
	var pad [16]byte
	pad[n%len(pad)] = byte(n)
	if n > 0 {
		deeper(n-1, f)
	} else {
		f()
	}
	stackSink += pad[0]
}

func TestTextMeasuringSurvivesStackGrowth(t *testing.T) {
	runtime.LockOSThread() // the Direct2D factory is made on this thread, as the menu does on its own
	defer runtime.UnlockOSThread()
	measure := textWidthAt(96)
	const sample = "Wi-Fi  ↓79 ↑7.0 Mbps  (31 flows via net-merge)  +IPv6"
	want := measure(sample)
	if want <= 0 {
		t.Fatalf("no width for %q", sample)
	}
	bad := 0
	for depth := 0; depth < 4500; depth++ {
		var got int
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { // a new goroutine each time, so its stack starts small and has to grow to depth
			defer wg.Done()
			deeper(depth, func() { got = measure(sample) })
		}()
		wg.Wait()
		if got != want {
			bad++
			if bad <= 5 {
				t.Errorf("at depth %d the width is %d, want %d", depth, got, want)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of 4500 depths measured wrongly", bad)
	}
}
