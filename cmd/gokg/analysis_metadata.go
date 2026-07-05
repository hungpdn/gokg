package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/hungpdn/gokg/internal/gitstate"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/version"
)

func newAnalysisMetadata(
	ctx context.Context,
	repoID string,
	repoRoot string,
	modulePrefix string,
	workspaceName string,
	includeTests bool,
) graph.AnalysisMetadata {
	root := repoRoot
	if abs, err := filepath.Abs(repoRoot); err == nil {
		root = filepath.Clean(abs)
	} else {
		root = filepath.Clean(repoRoot)
	}

	meta := graph.AnalysisMetadata{
		SchemaVersion: graph.AnalysisMetadataSchemaVersion,
		GoKGVersion:   version.Get().Version,
		AnalyzedAt:    time.Now().UTC(),
		RepoID:        repoID,
		RepoRoot:      root,
		ModulePrefix:  modulePrefix,
		WorkspaceName: workspaceName,
		IncludeTests:  includeTests,
	}

	snapshot, err := gitstate.Capture(ctx, root, gitstate.ExecRunner{})
	if err != nil {
		meta.GitError = err.Error()
		return meta
	}
	meta.GitAvailable = true
	meta.GitRoot = snapshot.Root
	meta.GitHead = snapshot.Head
	meta.GitBranch = snapshot.Branch
	meta.GitDirtyAtAnalyze = snapshot.Dirty
	meta.GitStatusFingerprint = snapshot.StatusFingerprint
	return meta
}

func saveAnalysisMetadata(
	ctx context.Context,
	store storage.Storage,
	repoID string,
	repoRoot string,
	modulePrefix string,
	workspaceName string,
	includeTests bool,
) error {
	return graph.SaveAnalysisMetadata(ctx, store, newAnalysisMetadata(ctx, repoID, repoRoot, modulePrefix, workspaceName, includeTests))
}
