//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package telemetry

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func defaultTelemetryLockDirectory() (string, error) {
	uid := os.Geteuid()
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return "", fmt.Errorf("resolve Unix account for telemetry locks (containers with arbitrary UIDs must provide a passwd entry): %w", err)
	}
	home := strings.TrimSpace(account.HomeDir)
	if home == "" {
		return "", fmt.Errorf("resolve Unix account home directory for telemetry locks: empty home")
	}
	info, err := os.Stat(home)
	if err != nil {
		return "", fmt.Errorf("inspect Unix account home directory for telemetry locks: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("unix account home for telemetry locks is not a directory")
	}
	if err := validateTelemetryLockDirectoryOwner(info); err != nil {
		return "", fmt.Errorf("validate Unix account home for telemetry locks: %w", err)
	}
	return filepath.Join(home, ".cache", "gokg", "locks"), nil
}

func validateTelemetryLockDirectoryOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect telemetry lock directory owner: unsupported file info")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("telemetry lock directory is owned by uid %d, expected effective uid %d", stat.Uid, os.Geteuid())
	}
	return nil
}

func lockTelemetryFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockTelemetryFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
