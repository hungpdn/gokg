package main

import (
	"os"
	"testing"

	telemetrypkg "github.com/hungpdn/gokg/internal/telemetry"
)

func TestMain(m *testing.M) {
	lockDir, err := os.MkdirTemp("", "gokg-cli-test-locks-")
	if err != nil {
		panic(err)
	}
	restore := telemetrypkg.SetLockDirectoryForTesting(lockDir)
	code := m.Run()
	restore()
	_ = os.RemoveAll(lockDir)
	os.Exit(code)
}
