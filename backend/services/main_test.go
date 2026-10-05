package services

import (
	"os"
	"testing"

	"middle-monitor/backend/internal/credentialenc"
)

// TestMain seeds the credential encryption env var before any test runs.
// credentialenc.Global() uses sync.Once, so the first call (from any test in this
// binary) sees the env var and caches the key ring for the rest of the run.
func TestMain(m *testing.M) {
	os.Setenv(credentialenc.EnvSingleKey, testKeyHex)
	// Plan gating only applies when billing is on; self-hosted behavior is tested explicitly.
	os.Setenv("BILLING_ENABLED", "true")
	os.Exit(m.Run())
}
