package main

import (
	"fmt"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
)

func resolveSingleGraphImpactRepo(g *graph.Graph, fallbackStart string, fallbackRepoID string) (goAnalysisRoot, impact.Repo, error) {
	roots := g.Query().RepositoryRoots()
	if len(roots) > 1 {
		return goAnalysisRoot{}, impact.Repo{}, fmt.Errorf(
			"graph contains multiple repository roots (%s); use --workspace for multi-repo impact analysis",
			formatRepositoryRootList(roots),
		)
	}

	rootPath := fallbackStart
	repoID := strings.TrimSpace(fallbackRepoID)
	if len(roots) == 1 {
		rootPath = roots[0].Root
		if roots[0].RepoID != "" {
			repoID = roots[0].RepoID
		}
	}

	analysisRoot, err := resolveGoAnalysisRoot(rootPath)
	if err != nil {
		return goAnalysisRoot{}, impact.Repo{}, err
	}
	if repoID == "" {
		repoID = analysisRoot.ModulePrefix
	}
	if repoID == "" {
		repoID = "gokg"
	}
	return analysisRoot, impact.Repo{ID: repoID, Root: analysisRoot.Dir}, nil
}

func formatRepositoryRootList(roots []graph.RepositoryRoot) string {
	parts := make([]string, 0, len(roots))
	for _, root := range roots {
		label := root.Root
		if root.RepoID != "" {
			label = root.RepoID + "=" + root.Root
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
