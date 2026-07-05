package gitstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error)
}

type CommandRunnerFunc func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

func (f CommandRunnerFunc) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	return f(ctx, dir, name, args...)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, message)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

type Snapshot struct {
	Root              string
	Head              string
	Branch            string
	Dirty             bool
	StatusFingerprint string
}

func Capture(ctx context.Context, dir string, runner CommandRunner) (Snapshot, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	rootOut, err := runner.Run(ctx, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve git root: %w", err)
	}
	headOut, err := runner.Run(ctx, dir, "git", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve git HEAD: %w", err)
	}
	branchOut, _ := runner.Run(ctx, dir, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	dirty, fingerprint, err := captureStatusFingerprint(ctx, dir, runner)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read git status: %w", err)
	}

	root := strings.TrimSpace(string(rootOut))
	if root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			root = filepath.Clean(abs)
		} else {
			root = filepath.Clean(root)
		}
	}

	return Snapshot{
		Root:              root,
		Head:              strings.TrimSpace(string(headOut)),
		Branch:            strings.TrimSpace(string(branchOut)),
		Dirty:             dirty,
		StatusFingerprint: fingerprint,
	}, nil
}

func captureStatusFingerprint(ctx context.Context, dir string, runner CommandRunner) (bool, string, error) {
	if _, ok := runner.(ExecRunner); ok {
		return captureStatusFingerprintExec(ctx, dir)
	}
	statusOut, err := runner.Run(ctx, dir, "git", "status", "--porcelain=v1", "-z")
	if err != nil {
		return false, "", err
	}
	return len(statusOut) > 0, statusFingerprint(statusOut), nil
}

func captureStatusFingerprintExec(ctx context.Context, dir string) (bool, string, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-z")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, "", err
	}
	if err := cmd.Start(); err != nil {
		return false, "", err
	}

	hasher := sha256.New()
	written, copyErr := io.Copy(hasher, stdout)
	waitErr := cmd.Wait()
	if copyErr != nil {
		return false, "", copyErr
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return false, "", fmt.Errorf("git status --porcelain=v1 -z: %w: %s", waitErr, message)
		}
		return false, "", fmt.Errorf("git status --porcelain=v1 -z: %w", waitErr)
	}
	if written == 0 {
		return false, "", nil
	}
	return true, hex.EncodeToString(hasher.Sum(nil)), nil
}

func statusFingerprint(status []byte) string {
	if len(status) == 0 {
		return ""
	}
	sum := sha256.Sum256(status)
	return hex.EncodeToString(sum[:])
}
