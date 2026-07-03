package gitstate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureReadsGitState(t *testing.T) {
	root := filepath.Clean("/repo")
	runner := fakeRunner{
		responses: map[string]string{
			fakeKey(root, "git", "rev-parse", "--show-toplevel"):               root + "\n",
			fakeKey(root, "git", "rev-parse", "--verify", "HEAD^{commit}"):     "abcdef\n",
			fakeKey(root, "git", "symbolic-ref", "--quiet", "--short", "HEAD"): "main\n",
			fakeKey(root, "git", "status", "--porcelain=v1", "-z"):             " M app.go\x00",
		},
	}

	snapshot, err := Capture(context.Background(), root, runner)
	require.NoError(t, err)
	assert.Equal(t, root, snapshot.Root)
	assert.Equal(t, "abcdef", snapshot.Head)
	assert.Equal(t, "main", snapshot.Branch)
	assert.True(t, snapshot.Dirty)
	assert.NotEmpty(t, snapshot.StatusFingerprint)
}

type fakeRunner struct {
	responses map[string]string
}

func (f fakeRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	key := fakeKey(dir, name, args...)
	if response, ok := f.responses[key]; ok {
		return []byte(response), nil
	}
	return nil, fmt.Errorf("unexpected command: %s", key)
}

func fakeKey(dir string, name string, args ...string) string {
	return filepath.Clean(dir) + "\x00" + name + "\x00" + strings.Join(args, "\x00")
}
