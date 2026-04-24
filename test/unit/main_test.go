package unit

import (
	"os"
	"testing"

	"github.com/jumboframes/armorigo/log"
)

func TestMain(m *testing.M) {
	// Suppress INFO/WARN log noise (e.g. "dialogue offline … not found") so
	// that only genuine errors surface in test output.
	log.SetLevel(log.LevelError)
	os.Exit(m.Run())
}
