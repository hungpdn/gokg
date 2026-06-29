package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
)

var impactCmd = newImpactCommand()

func newImpactCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact",
		Short: "Analyze dependency impact from Git changes",
		Long:  "Analyze Git changes, map changed lines to GoKG graph nodes, and report dependency impact.",
		RunE:  runImpact,
	}
	cmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	cmd.Flags().String("workspace", "", "Workspace name to inspect by merging per-repo databases")
	cmd.Flags().String("base", impact.DefaultBaseRef, "Git base ref for diff analysis")
	cmd.Flags().Int("depth", impact.DefaultMaxDepth, "Maximum inbound dependency depth")
	cmd.Flags().Int("max-nodes", impact.DefaultMaxNodes, "Maximum impacted nodes to return")
	cmd.Flags().Bool("include-untracked", true, "Include untracked Git files")
	cmd.Flags().Bool("tracked-only", false, "Analyze only tracked Git changes")
	cmd.Flags().Bool("json", false, "Print machine-readable JSON")
	return cmd
}

func runImpact(cmd *cobra.Command, args []string) (err error) {
	ctx := cmd.Context()
	dbPath, _ := cmd.Flags().GetString("db")
	workspaceName, _ := cmd.Flags().GetString("workspace")
	baseRef, _ := cmd.Flags().GetString("base")
	maxDepth, _ := cmd.Flags().GetInt("depth")
	maxNodes, _ := cmd.Flags().GetInt("max-nodes")
	includeUntracked, _ := cmd.Flags().GetBool("include-untracked")
	trackedOnly, _ := cmd.Flags().GetBool("tracked-only")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if trackedOnly {
		if cmd.Flags().Changed("include-untracked") && includeUntracked {
			return fmt.Errorf("--tracked-only cannot be used with --include-untracked=true")
		}
		includeUntracked = false
	}

	opts := impact.NormalizeOptions(impact.Options{
		BaseRef:          baseRef,
		MaxDepth:         maxDepth,
		MaxNodes:         maxNodes,
		IncludeUntracked: includeUntracked,
	})
	if err := impact.ValidateOptions(opts); err != nil {
		return err
	}

	logOut := cmd.ErrOrStderr()
	if _, err := fmt.Fprintln(logOut, "Loading graph..."); err != nil {
		return err
	}
	g, repos, err := loadImpactGraph(ctx, cmd, dbPath, workspaceName)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(logOut, "Analyzing change impact..."); err != nil {
		return err
	}
	report, err := impact.Analyze(ctx, g, repos, opts)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	_, err = fmt.Fprint(out, impact.FormatMarkdown(report))
	return err
}

func loadImpactGraph(ctx context.Context, cmd *cobra.Command, dbPath string, workspaceName string) (g *graph.Graph, repos []impact.Repo, err error) {
	if workspaceName != "" {
		if cmd.Flags().Changed("db") {
			return nil, nil, fmt.Errorf("--db cannot be used with --workspace; workspace mode loads per-repo databases")
		}
		ws, err := workspace.Load(workspaceName)
		if err != nil {
			return nil, nil, err
		}
		g, err := loadWorkspaceGraph(ctx, workspaceName)
		if err != nil {
			return nil, nil, err
		}
		repos := make([]impact.Repo, 0, len(ws.Config.Repos))
		for _, repo := range sortedWorkspaceRepos(ws) {
			repos = append(repos, impact.Repo{ID: repo.ID, Root: repo.Path})
		}
		return g, repos, nil
	}

	store, err := storage.NewBadgerStorageReadOnly(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open storage: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	g = graph.NewGraph(store)
	if err := g.LoadFromStorage(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to load graph: %w", err)
	}
	_, repo, err := resolveSingleGraphImpactRepo(g, ".", "")
	if err != nil {
		return nil, nil, err
	}
	return g, []impact.Repo{repo}, nil
}
