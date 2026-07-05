package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
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

func attachAnalysisMetadata(ctx context.Context, store storage.Storage, repo *impact.Repo) error {
	meta, ok, err := graph.LoadAnalysisMetadata(ctx, store)
	if err != nil {
		return err
	}
	if ok {
		repo.AnalysisMetadata = &meta
	}
	return nil
}

func attachAnalysisMetadataLoader(repo *impact.Repo, dbPath string) {
	repo.AnalysisMetadataLoader = func(ctx context.Context) (meta *graph.AnalysisMetadata, err error) {
		store, err := storage.NewBadgerStorageReadOnly(dbPath)
		if err != nil {
			return nil, err
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}()

		loaded, ok, err := graph.LoadAnalysisMetadata(ctx, store)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return &loaded, nil
	}
}

func workspaceImpactReposWithMetadata(ctx context.Context, ws *workspace.Workspace) ([]impact.Repo, error) {
	repos := make([]impact.Repo, 0, len(ws.Config.Repos))
	for _, repo := range sortedWorkspaceRepos(ws) {
		impactRepo := impact.Repo{ID: repo.ID, Root: repo.Path}
		dbPath := ws.GetRepoDBPath(repo.ID)
		store, err := storage.NewBadgerStorageReadOnly(dbPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open database for repo %q metadata: %w", repo.ID, err)
		}
		if err := attachAnalysisMetadata(ctx, store, &impactRepo); err != nil {
			closeErr := store.Close()
			if closeErr != nil {
				return nil, fmt.Errorf("failed to load metadata for repo %q: %w; additionally failed to close storage: %v", repo.ID, err, closeErr)
			}
			return nil, fmt.Errorf("failed to load metadata for repo %q: %w", repo.ID, err)
		}
		if err := store.Close(); err != nil {
			return nil, fmt.Errorf("failed to close metadata storage for repo %q: %w", repo.ID, err)
		}
		repos = append(repos, impactRepo)
	}
	return repos, nil
}

func impactRepoPointersByID(repos []impact.Repo) map[string]*impact.Repo {
	byID := make(map[string]*impact.Repo, len(repos))
	for i := range repos {
		byID[repos[i].ID] = &repos[i]
	}
	return byID
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
