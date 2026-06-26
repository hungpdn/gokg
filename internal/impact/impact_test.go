package impact

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDiffHunksAndWholeFiles(t *testing.T) {
	repo := Repo{ID: "repo-a", Root: "/repo"}
	diff := `diff --git a/app.go b/app.go
index 111..222 100644
--- a/app.go
+++ b/app.go
@@ -10,0 +11,2 @@
+line
+line
diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
@@ -1,2 +0,0 @@
-old
-old
`

	files, err := ParseDiff(repo, diff)
	require.NoError(t, err)
	require.Len(t, files, 2)

	assert.Equal(t, "app.go", files[0].Path)
	assert.Equal(t, "M", files[0].Status)
	assert.Equal(t, []LineRange{{Start: 11, End: 12}}, files[0].Ranges)
	assert.False(t, files[0].WholeFile)

	assert.Equal(t, "deleted.go", files[1].Path)
	assert.Equal(t, "D", files[1].Status)
	assert.True(t, files[1].WholeFile)
	assert.Equal(t, filepath.Clean("/repo/deleted.go"), files[1].AbsolutePath)
}

func TestAnalyzeMapsChangesAndBlastRadiusDepth(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "app.go")
	g := graph.NewGraph(nil)

	for _, node := range []*parser.Node{
		{ID: "pkg.Changed", Type: parser.NodeTypeFunc, Name: "Changed", PkgPath: "pkg", FilePath: filePath, Lines: [2]int{10, 20}, RepoID: "repo-a"},
		{ID: "pkg.Direct", Type: parser.NodeTypeFunc, Name: "Direct", PkgPath: "pkg", FilePath: filePath, Lines: [2]int{30, 40}, RepoID: "repo-a"},
		{ID: "pkg.Second", Type: parser.NodeTypeFunc, Name: "Second", PkgPath: "pkg", FilePath: filePath, Lines: [2]int{50, 60}, RepoID: "repo-a"},
	} {
		_, err := g.AddNode(ctx, node)
		require.NoError(t, err)
	}
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Direct", To: "pkg.Changed", Type: parser.EdgeTypeCalls, RepoID: "repo-a"}))
	require.NoError(t, g.AddEdge(ctx, &parser.Edge{From: "pkg.Second", To: "pkg.Direct", Type: parser.EdgeTypeReferences, RepoID: "repo-a"}))

	diff := `diff --git a/app.go b/app.go
--- a/app.go
+++ b/app.go
@@ -12 +12 @@
+changed
`
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", "HEAD"): diff,
		},
	}

	report, err := AnalyzeWithRunner(ctx, g, []Repo{{ID: "repo-a", Root: root}}, Options{MaxDepth: 2, MaxNodes: 100}, runner)
	require.NoError(t, err)
	require.Len(t, report.ChangedNodes, 1)
	assert.Equal(t, "pkg.Changed", report.ChangedNodes[0].ID)
	require.Len(t, report.ImpactedNodes, 2)
	assert.Equal(t, "pkg.Direct", report.ImpactedNodes[0].ID)
	assert.Equal(t, 1, report.ImpactedNodes[0].Distance)
	assert.Equal(t, "pkg.Second", report.ImpactedNodes[1].ID)
	assert.Equal(t, 2, report.ImpactedNodes[1].Distance)
}

func TestAnalyzeWholeFileAndUntrackedWarnings(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "deleted.go")
	g := graph.NewGraph(nil)
	_, err := g.AddNode(ctx, &parser.Node{
		ID:       "pkg.Deleted",
		Type:     parser.NodeTypeFunc,
		Name:     "Deleted",
		PkgPath:  "pkg",
		FilePath: filePath,
		Lines:    [2]int{1, 5},
		RepoID:   "repo-a",
	})
	require.NoError(t, err)

	diff := `diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
`
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", "HEAD"): diff,
			fakeCommandKey(root, "git", "ls-files", "--others", "--exclude-standard"):                 "new.go\n",
		},
	}

	report, err := AnalyzeWithRunner(
		ctx,
		g,
		[]Repo{{ID: "repo-a", Root: root}},
		Options{MaxDepth: 1, MaxNodes: 100, IncludeUntracked: true},
		runner,
	)
	require.NoError(t, err)
	require.Len(t, report.ChangedFiles, 2)
	require.Len(t, report.ChangedNodes, 1)
	assert.Equal(t, "pkg.Deleted", report.ChangedNodes[0].ID)
	assert.Contains(t, strings.Join(report.Warnings, "\n"), "new.go")
}

func TestValidateOptionsRejectsUnsafeBaseRef(t *testing.T) {
	for _, baseRef := range []string{
		"--cached",
		"-HEAD",
		"main\nnext",
		"main\x7f",
	} {
		t.Run(baseRef, func(t *testing.T) {
			opts := NormalizeOptions(Options{BaseRef: baseRef, MaxDepth: 1, MaxNodes: 1})
			err := ValidateOptions(opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "base ref")
		})
	}

	opts := NormalizeOptions(Options{BaseRef: "origin/main", MaxDepth: 1, MaxNodes: 1})
	require.NoError(t, ValidateOptions(opts))
}

type fakeRunner struct {
	responses map[string]string
	errors    map[string]error
}

func (f fakeRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	key := fakeCommandKey(dir, name, args...)
	if err := f.errors[key]; err != nil {
		return nil, err
	}
	if response, ok := f.responses[key]; ok {
		return []byte(response), nil
	}
	return nil, fmt.Errorf("unexpected command: %s", key)
}

func fakeCommandKey(dir string, name string, args ...string) string {
	return filepath.Clean(dir) + "\x00" + name + "\x00" + strings.Join(args, "\x00")
}
