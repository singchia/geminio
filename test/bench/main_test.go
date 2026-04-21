package bench

import (
	"os"
	"testing"

	"github.com/jumboframes/armorigo/log"
)

func TestMain(m *testing.M) {
	// Suppress INFO/WARN log noise so that only genuine errors surface in test output.
	log.SetLevel(log.LevelError)
	os.Exit(m.Run())
}

// reportOpsPerSec adds an "ops/sec" metric to the benchmark output. Call at
// the top of a benchmark as `defer reportOpsPerSec(b)`; the deferred call
// fires at function exit when b.Elapsed() holds the final accumulated
// runtime for the last benchmark phase.
func reportOpsPerSec(b *testing.B) {
	b.Helper()
	if elapsed := b.Elapsed().Seconds(); elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed, "ops/sec")
	}
}
