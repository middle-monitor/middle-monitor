package workers

import (
	"os"
	"testing"
)

// Plan gating only applies when billing is on, which is what the retention and
// limit tests exercise.
func TestMain(m *testing.M) {
	os.Setenv("BILLING_ENABLED", "true")
	os.Exit(m.Run())
}
