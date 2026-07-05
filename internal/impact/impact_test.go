package impact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeBaseCommit = "abc123"

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

	files, err := ParseDiff(repo, strings.NewReader(diff))
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

func TestParseDiffTreatsNoHunkChangesAsWholeFile(t *testing.T) {
	repo := Repo{ID: "repo-a", Root: "/repo"}
	diff := `diff --git a/app.go b/app.go
old mode 100644
new mode 100755
diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
`

	files, err := ParseDiff(repo, strings.NewReader(diff))
	require.NoError(t, err)
	require.Len(t, files, 2)

	assert.Equal(t, "app.go", files[0].Path)
	assert.Equal(t, "M", files[0].Status)
	assert.True(t, files[0].WholeFile)
	assert.Empty(t, files[0].Ranges)

	assert.Equal(t, "new.go", files[1].Path)
	assert.Equal(t, "R", files[1].Status)
	assert.True(t, files[1].WholeFile)
	assert.Empty(t, files[1].Ranges)
}

func TestParseDiffHandlesLongLines(t *testing.T) {
	repo := Repo{ID: "repo-a", Root: "/repo"}
	diff := "diff --git a/generated.go b/generated.go\n" +
		"--- a/generated.go\n" +
		"+++ b/generated.go\n" +
		"@@ -1 +1 @@\n" +
		"+" + strings.Repeat("x", 70*1024) + "\n"

	files, err := ParseDiff(repo, strings.NewReader(diff))
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "generated.go", files[0].Path)
	assert.Equal(t, []LineRange{{Start: 1, End: 1}}, files[0].Ranges)
}

func TestParseUntrackedUsesNULAndPreservesWhitespace(t *testing.T) {
	repo := Repo{ID: "repo-a", Root: "/repo"}

	files, err := ParseUntracked(repo, strings.NewReader(" leading.go\x00trailing .go\x00dir/with\nnewline.go\x00"))
	require.NoError(t, err)
	require.Len(t, files, 3)
	assert.Equal(t, " leading.go", files[0].Path)
	assert.Equal(t, "trailing .go", files[1].Path)
	assert.Equal(t, "dir/with\nnewline.go", files[2].Path)
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
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):               fakeBaseCommit + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", fakeBaseCommit, "--"): diff,
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
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):               fakeBaseCommit + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", fakeBaseCommit, "--"): diff,
			fakeCommandKey(root, "git", "ls-files", "-z", "--others", "--exclude-standard"):                         "new.go\x00",
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

func TestAnalyzeTruncatesChangedFilesAtMaxFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := graph.NewGraph(nil)
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1 +1 @@
+a
diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -1 +1 @@
+b
`
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):               fakeBaseCommit + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", fakeBaseCommit, "--"): diff,
		},
	}

	report, err := AnalyzeWithRunner(
		ctx,
		g,
		[]Repo{{ID: "repo-a", Root: root}},
		Options{MaxDepth: 1, MaxNodes: 100, MaxFiles: 1},
		runner,
	)
	require.NoError(t, err)
	require.Len(t, report.ChangedFiles, 1)
	assert.Equal(t, "a.go", report.ChangedFiles[0].Path)
	assert.Contains(t, strings.Join(report.Warnings, "\n"), "changed files truncated at max_files=1")
}

func TestAnalyzeReportsStaleGraphFreshnessWhenHeadChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := graph.NewGraph(nil)
	analyzedHead := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	currentHead := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "rev-parse", "--show-toplevel"):                                          root + "\n",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "HEAD^{commit}"):                                currentHead + "\n",
			fakeCommandKey(root, "git", "symbolic-ref", "--quiet", "--short", "HEAD"):                            "main\n",
			fakeCommandKey(root, "git", "status", "--porcelain=v1", "-z"):                                        "",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):            currentHead + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", currentHead, "--"): "",
		},
	}

	report, err := AnalyzeWithRunner(ctx, g, []Repo{{
		ID:   "repo-a",
		Root: root,
		AnalysisMetadata: &graph.AnalysisMetadata{
			SchemaVersion: graph.AnalysisMetadataSchemaVersion,
			AnalyzedAt:    time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
			RepoID:        "repo-a",
			RepoRoot:      root,
			IncludeTests:  true,
			GitAvailable:  true,
			GitRoot:       root,
			GitHead:       analyzedHead,
			GitBranch:     "main",
			GoKGVersion:   "test",
			ModulePrefix:  "pkg",
			WorkspaceName: "",
		},
	}}, Options{MaxDepth: 1, MaxNodes: 100}, runner)
	require.NoError(t, err)
	require.Len(t, report.Repos, 1)
	require.NotNil(t, report.Repos[0].Freshness)
	assert.Equal(t, FreshnessStale, report.Repos[0].Freshness.Status)
	assert.True(t, report.HasStaleFreshness())
	assert.Contains(t, strings.Join(report.Repos[0].Freshness.Reasons, "\n"), "differs from current HEAD")
}

func TestAnalyzeReportsStaleGraphFreshnessWhenWorkingTreeStatusChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := graph.NewGraph(nil)
	head := "cccccccccccccccccccccccccccccccccccccccc"
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "rev-parse", "--show-toplevel"):                                   root + "\n",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "HEAD^{commit}"):                         head + "\n",
			fakeCommandKey(root, "git", "symbolic-ref", "--quiet", "--short", "HEAD"):                     "main\n",
			fakeCommandKey(root, "git", "status", "--porcelain=v1", "-z"):                                 " M app.go\x00",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):     head + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", head, "--"): "",
			fakeCommandKey(root, "git", "ls-files", "-z", "--others", "--exclude-standard"):               "",
		},
	}

	report, err := AnalyzeWithRunner(ctx, g, []Repo{{
		ID:   "repo-a",
		Root: root,
		AnalysisMetadata: &graph.AnalysisMetadata{
			SchemaVersion:        graph.AnalysisMetadataSchemaVersion,
			AnalyzedAt:           time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
			RepoID:               "repo-a",
			RepoRoot:             root,
			IncludeTests:         true,
			GitAvailable:         true,
			GitRoot:              root,
			GitHead:              head,
			GitDirtyAtAnalyze:    false,
			GitStatusFingerprint: "",
		},
	}}, Options{MaxDepth: 1, MaxNodes: 100, IncludeUntracked: true}, runner)
	require.NoError(t, err)
	require.NotNil(t, report.Repos[0].Freshness)
	assert.Equal(t, FreshnessStale, report.Repos[0].Freshness.Status)
	reasons := strings.Join(report.Repos[0].Freshness.Reasons, "\n")
	assert.Contains(t, reasons, "dirty state")
	assert.Contains(t, reasons, "working tree status")
	assert.True(t, report.Repos[0].Freshness.CurrentGitDirty)
}

func TestAnalyzeUsesFreshAnalysisMetadataLoader(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := graph.NewGraph(nil)
	staleHead := "dddddddddddddddddddddddddddddddddddddddd"
	currentHead := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "rev-parse", "--show-toplevel"):                                          root + "\n",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "HEAD^{commit}"):                                currentHead + "\n",
			fakeCommandKey(root, "git", "symbolic-ref", "--quiet", "--short", "HEAD"):                            "main\n",
			fakeCommandKey(root, "git", "status", "--porcelain=v1", "-z"):                                        "",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):            currentHead + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", currentHead, "--"): "",
		},
	}

	report, err := AnalyzeWithRunner(ctx, g, []Repo{{
		ID:   "repo-a",
		Root: root,
		AnalysisMetadata: &graph.AnalysisMetadata{
			SchemaVersion: graph.AnalysisMetadataSchemaVersion,
			AnalyzedAt:    time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
			RepoID:        "repo-a",
			RepoRoot:      root,
			GitAvailable:  true,
			GitRoot:       root,
			GitHead:       staleHead,
		},
		AnalysisMetadataLoader: func(context.Context) (*graph.AnalysisMetadata, error) {
			return &graph.AnalysisMetadata{
				SchemaVersion: graph.AnalysisMetadataSchemaVersion,
				AnalyzedAt:    time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
				RepoID:        "repo-a",
				RepoRoot:      root,
				IncludeTests:  true,
				GitAvailable:  true,
				GitRoot:       root,
				GitHead:       currentHead,
			}, nil
		},
	}}, Options{MaxDepth: 1, MaxNodes: 100}, runner)
	require.NoError(t, err)
	require.NotNil(t, report.Repos[0].Freshness)
	assert.Equal(t, FreshnessFresh, report.Repos[0].Freshness.Status)
}

func TestFreshnessPathCompareResolvesSymlinks(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	assert.True(t, samePath(target, link, make(map[string]string)))
}

func TestAnalyzeMarksTestFileChangesStaleWhenTestsWereExcluded(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	g := graph.NewGraph(nil)
	head := "cccccccccccccccccccccccccccccccccccccccc"
	diff := `diff --git a/app_test.go b/app_test.go
--- a/app_test.go
+++ b/app_test.go
@@ -1 +1 @@
+changed
`
	runner := fakeRunner{
		responses: map[string]string{
			fakeCommandKey(root, "git", "rev-parse", "--show-toplevel"):                                   root + "\n",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "HEAD^{commit}"):                         head + "\n",
			fakeCommandKey(root, "git", "symbolic-ref", "--quiet", "--short", "HEAD"):                     "main\n",
			fakeCommandKey(root, "git", "status", "--porcelain=v1", "-z"):                                 "",
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}"):     head + "\n",
			fakeCommandKey(root, "git", "diff", "--unified=0", "--no-ext-diff", "--no-color", head, "--"): diff,
		},
	}

	report, err := AnalyzeWithRunner(ctx, g, []Repo{{
		ID:   "repo-a",
		Root: root,
		AnalysisMetadata: &graph.AnalysisMetadata{
			SchemaVersion: graph.AnalysisMetadataSchemaVersion,
			AnalyzedAt:    time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC),
			RepoID:        "repo-a",
			RepoRoot:      root,
			GitAvailable:  true,
			GitRoot:       root,
			GitHead:       head,
		},
	}}, Options{MaxDepth: 1, MaxNodes: 100}, runner)
	require.NoError(t, err)
	require.NotNil(t, report.Repos[0].Freshness)
	assert.Equal(t, FreshnessStale, report.Repos[0].Freshness.Status)
	assert.Contains(t, strings.Join(report.Repos[0].Freshness.Reasons, "\n"), "--tests=false")
}

func TestChangedFilesForRepoVerifiesBaseRef(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runner := fakeRunner{
		errors: map[string]error{
			fakeCommandKey(root, "git", "rev-parse", "--verify", "--end-of-options", "missing^{commit}"): fmt.Errorf("unknown revision"),
		},
	}

	_, err := changedFilesForRepo(ctx, runner, Repo{ID: "repo-a", Root: root}, NormalizeOptions(Options{BaseRef: "missing"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid base ref "missing"`)
}

func TestValidateOptionsRejectsUnsafeBaseRef(t *testing.T) {
	for _, baseRef := range []string{
		"--cached",
		"-HEAD",
		"^HEAD",
		"main..next",
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
