package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
)

type workspaceRepo struct {
	ID   string
	Path string
}

func sortedWorkspaceRepos(ws *workspace.Workspace) []workspaceRepo {
	repos := make([]workspaceRepo, 0, len(ws.Config.Repos))
	for repoID, repoPath := range ws.Config.Repos {
		repos = append(repos, workspaceRepo{ID: repoID, Path: repoPath})
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].ID < repos[j].ID
	})
	return repos
}

func loadWorkspaceGraph(ctx context.Context, workspaceName string) (*graph.Graph, []storage.Storage, error) {
	ws, err := workspace.Load(workspaceName)
	if err != nil {
		return nil, nil, err
	}

	stores, err := openWorkspaceStores(ws)
	if err != nil {
		return nil, nil, err
	}

	g := graph.NewGraph(nil)
	if err := g.LoadFromStorages(ctx, stores...); err != nil {
		_ = closeStores(stores)
		return nil, nil, err
	}

	return g, stores, nil
}

func openWorkspaceStores(ws *workspace.Workspace) ([]storage.Storage, error) {
	repos := sortedWorkspaceRepos(ws)
	if len(repos) == 0 {
		return nil, fmt.Errorf("workspace %q has no repositories", ws.Name)
	}

	stores := make([]storage.Storage, 0, len(repos))
	for _, repo := range repos {
		dbPath := ws.GetRepoDBPath(repo.ID)
		if _, err := os.Stat(dbPath); err != nil {
			_ = closeStores(stores)
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("repo %q has no database at %s; run gokg analyze --workspace %s first", repo.ID, dbPath, ws.Name)
			}
			return nil, fmt.Errorf("failed to inspect database for repo %q: %w", repo.ID, err)
		}

		store, err := storage.NewBadgerStorage(dbPath)
		if err != nil {
			_ = closeStores(stores)
			return nil, fmt.Errorf("failed to open database for repo %q: %w", repo.ID, err)
		}
		stores = append(stores, store)
	}

	return stores, nil
}

func closeStores(stores []storage.Storage) error {
	var firstErr error
	for _, store := range stores {
		if store == nil {
			continue
		}
		if err := store.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
