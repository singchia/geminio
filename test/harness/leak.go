package harness

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// GoroutineSnapshot records the goroutine count and stack summary at a point
// in time. Use it together with AssertNoLeak.
type GoroutineSnapshot struct {
	count int
	stacks string
}

// TakeSnapshot captures the current goroutine count and abbreviated stacks.
func TakeSnapshot() GoroutineSnapshot {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return GoroutineSnapshot{
		count:  runtime.NumGoroutine(),
		stacks: string(buf[:n]),
	}
}

// AssertNoLeak verifies that the goroutine count after the test has not grown
// by more than tolerance relative to the snapshot taken before the test.
//
// It retries for up to 500 ms to give the runtime time to reap goroutines that
// are shutting down concurrently.
//
//	before := harness.TakeSnapshot()
//	// ... run test ...
//	harness.AssertNoLeak(t, before, 5)
func AssertNoLeak(t testing.TB, before GoroutineSnapshot, tolerance int) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		after := runtime.NumGoroutine()
		if after <= before.count+tolerance {
			return
		}
		if time.Now().After(deadline) {
			afterBuf := make([]byte, 1<<20)
			n := runtime.Stack(afterBuf, true)
			afterStacks := string(afterBuf[:n])
			leaked := diffStacks(before.stacks, afterStacks)
			t.Errorf("goroutine leak: before=%d after=%d tolerance=%d\n%s",
				before.count, after, tolerance, leaked)
			return
		}
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
	}
}

// CheckLeak is a convenience wrapper that takes a snapshot before f() runs
// and calls AssertNoLeak afterwards. tolerance=5 is a sensible default for
// most tests.
//
//	harness.CheckLeak(t, 5, func() {
//	    sEnd, cEnd := harness.NewEndPair(t)
//	    // ...
//	})
func CheckLeak(t *testing.T, tolerance int, f func()) {
	t.Helper()
	before := TakeSnapshot()
	f()
	// Allow t.Cleanup to run (Close calls) before measuring.
	// Since we are inside the test body, cleanups haven't fired yet;
	// callers should call this after their own cleanup if precise counts matter.
	AssertNoLeak(t, before, tolerance)
}

// diffStacks returns goroutine entries present in after but not in before,
// keyed by the first non-runtime line of the stack frame.
func diffStacks(before, after string) string {
	beforeSet := parseGoroutines(before)
	var leaked []string
	for _, g := range parseGoroutines(after) {
		key := goroutineKey(g)
		if _, seen := beforeSet[key]; !seen {
			leaked = append(leaked, g)
		}
	}
	if len(leaked) == 0 {
		return "(could not identify specific leaked goroutines)"
	}
	return fmt.Sprintf("leaked goroutines:\n%s", strings.Join(leaked, "\n---\n"))
}

// parseGoroutines splits a full goroutine dump into individual goroutine blocks
// and returns them keyed by goroutineKey.
func parseGoroutines(dump string) map[string]string {
	m := make(map[string]string)
	blocks := strings.Split(dump, "\n\n")
	for _, b := range blocks {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		m[goroutineKey(b)] = b
	}
	return m
}

// goroutineKey extracts a stable identity from a goroutine block: the first
// non-runtime, non-testing line of the stack (i.e. the topmost application
// frame). Falls back to the entire first line (goroutine header).
func goroutineKey(block string) string {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return block
	}
	header := lines[0] // "goroutine 42 [chan receive]:"
	for _, l := range lines[1:] {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "runtime.") ||
			strings.HasPrefix(l, "testing.") ||
			strings.HasPrefix(l, "created by testing.") {
			continue
		}
		return l
	}
	return header
}
