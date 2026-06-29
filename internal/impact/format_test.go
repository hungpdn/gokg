package impact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatMarkdownGroupsImpactByDistanceRepoPackageAndFile(t *testing.T) {
	report := &Report{
		BaseRef: "HEAD",
		Repos: []RepoReport{
			{ID: "repo-a", Root: "/repo", Scanned: true},
		},
		ChangedFiles: []ChangedFile{
			{RepoID: "repo-a", Path: "app.go", AbsolutePath: "/repo/app.go", Status: "M", Ranges: []LineRange{{Start: 10, End: 10}}},
		},
		ChangedNodes: []NodeSummary{
			{ID: "pkg.Changed", Name: "Changed", Type: "FUNC", PkgPath: "pkg", FilePath: "/repo/app.go", LineStart: 10, LineEnd: 12, RepoID: "repo-a"},
		},
		ImpactedNodes: []ImpactNode{
			{NodeSummary: NodeSummary{ID: "pkg.Caller", Name: "Caller", Type: "FUNC", PkgPath: "pkg", FilePath: "/repo/app.go", LineStart: 20, LineEnd: 22, RepoID: "repo-a"}, Distance: 1},
			{NodeSummary: NodeSummary{ID: "pkg.Second", Name: "Second", Type: "FUNC", PkgPath: "pkg", FilePath: "/repo/app.go", LineStart: 30, LineEnd: 32, RepoID: "repo-a"}, Distance: 2},
		},
		Warnings: []string{"one warning"},
	}

	markdown := FormatMarkdown(report)

	assert.Contains(t, markdown, "- Warnings: **1**")
	assert.Contains(t, markdown, "Distance 1:")
	assert.Contains(t, markdown, "Distance 2:")
	assert.Contains(t, markdown, "Repo `repo-a`:")
	assert.Contains(t, markdown, "Package `pkg`:")
	assert.Contains(t, markdown, "File `/repo/app.go`:")
	assert.True(t, strings.Index(markdown, "Distance 1:") < strings.Index(markdown, "Distance 2:"))
}

func TestFormatMarkdownEscapesInlineValues(t *testing.T) {
	report := &Report{
		BaseRef: "feature`branch",
		ChangedFiles: []ChangedFile{
			{RepoID: "repo\none", Path: "path`with`tick.go", Status: "M", WholeFile: true},
		},
		ChangedNodes: []NodeSummary{
			{ID: "pkg.`Node`", Name: "Name`Tick", Type: "FUNC", PkgPath: "pkg\npath"},
		},
		Warnings: []string{"warning\nnext"},
	}

	markdown := FormatMarkdown(report)

	assert.Contains(t, markdown, "``feature`branch``")
	assert.Contains(t, markdown, "``path`with`tick.go``")
	assert.Contains(t, markdown, "repo one")
	assert.Contains(t, markdown, "``Name`Tick``")
	assert.Contains(t, markdown, "warning next")
	assert.NotContains(t, markdown, "warning\nnext")
}
