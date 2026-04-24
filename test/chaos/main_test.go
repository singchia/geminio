package chaos

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/jumboframes/armorigo/log"
)

func TestMain(m *testing.M) {
	// The chaos suite waits out real heartbeat windows — HalfOpenSendOnly
	// and WireBitFlip alone burn ~100s each — so even the "fast" subset is
	// minutes long. That's fine for the dedicated Chaos tests job, but
	// makes `go test -short ./...` unusable on slower runners (macOS). Opt
	// out of -short entirely; run the real suite via the Chaos tests job.
	testing.Init()
	flag.Parse()
	if testing.Short() {
		fmt.Println("skipping chaos suite in -short mode")
		os.Exit(0)
	}
	// Suppress INFO/WARN log noise so that only genuine errors surface in test output.
	log.SetLevel(log.LevelError)
	os.Exit(m.Run())
}
