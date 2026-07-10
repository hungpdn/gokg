//go:build windows

package telemetry

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func defaultTelemetryLockDirectory() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve Windows account for telemetry locks: %w", err)
	}
	home := strings.TrimSpace(currentUser.HomeDir)
	if home == "" {
		return "", fmt.Errorf("resolve Windows account home directory for telemetry locks: empty home")
	}
	return filepath.Join(home, ".cache", "gokg", "locks"), nil
}

func validateTelemetryLockDirectoryOwner(os.FileInfo) error { return nil }

func lockTelemetryFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		^uint32(0),
		^uint32(0),
		&windows.Overlapped{},
	)
}

func unlockTelemetryFile(file *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		^uint32(0),
		^uint32(0),
		&windows.Overlapped{},
	)
}
