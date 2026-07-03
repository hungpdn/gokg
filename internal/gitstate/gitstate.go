package gitstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	statusOut, err := runner.Run(ctx, dir, "git", "status", "--porcelain=v1", "-z")
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
		Dirty:             len(statusOut) > 0,
		StatusFingerprint: statusFingerprint(statusOut),
	}, nil
}

func statusFingerprint(status []byte) string {
	if len(status) == 0 {
		return ""
	}
	sum := sha256.Sum256(status)
	return hex.EncodeToString(sum[:])
}
