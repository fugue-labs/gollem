package main

import (
	"os"
	"testing"

	"github.com/fugue-labs/gollem/appserver/catalog/catwalk"
)

// TestMain keeps the package hermetic: no live model-registry fetches and no
// writes to the per-user registry cache, including from helper subprocesses
// that inherit this environment.
func TestMain(m *testing.M) {
	_ = os.Setenv(registryRefreshEnv, "0")
	_ = os.Setenv(catwalk.CachePathEnv, "")
	os.Exit(m.Run())
}
