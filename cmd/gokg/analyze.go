package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
	"golang.org/x/sync/errgroup"
)

const defaultValueLogGCDiscardRatio = 0.5

var analyzeCmd = newAnalyzeCommand()

func newAnalyzeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Analyze the current Go workspace",
		Long:  `Parse the current Go project, build the semantic knowledge graph, and save it to the local storage.`,
		RunE:  runAnalyze,
	}
	cmd.Flags().StringP("module", "m", "", "Module prefix for internal packages")
	cmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	cmd.Flags().String("workspace", "", "Workspace name to analyze using per-repo databases")
	cmd.Flags().Bool("rebuild", false, "Delete and rebuild the local database before analysis")
	cmd.Flags().Bool("gc", true, "Run Badger value-log garbage collection after analysis")
	return cmd
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	fmt.Println("Starting analysis...")
	ctx := context.Background()

	workspaceName, _ := cmd.Flags().GetString("workspace")
	if workspaceName != "" {
		return runAnalyzeWorkspace(cmd, ctx, workspaceName)
	}

	dbPath, _ := cmd.Flags().GetString("db")
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	runGC, _ := cmd.Flags().GetBool("gc")

	if rebuild {
		if err := rebuildBadgerDBPath(dbPath, cmd.Flags().Changed("db")); err != nil {
			return err
		}
		fmt.Printf("Rebuilding local database at %s...\n", dbPath)
	}

	// Init Storage
	store, err := storage.NewBadgerStorage(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer store.Close()

	modulePrefix, _ := cmd.Flags().GetString("module")
	if modulePrefix == "" {
		data, err := os.ReadFile("go.mod")
		if err == nil {
			f, err := modfile.Parse("go.mod", data, nil)
			if err == nil && f.Module != nil {
				modulePrefix = f.Module.Mod.Path
			}
		}
		if modulePrefix == "" {
			modulePrefix = "gokg"
		}
	}

	// Parse Workspace
	p := parser.NewParser(modulePrefix, modulePrefix)
	dir, _ := os.Getwd()
	result, err := p.ParseWorkspace(ctx, dir)
	if err != nil {
		return fmt.Errorf("parse workspace failed: %w", err)
	}

	fmt.Printf("Parsed %d nodes and %d edges\n", len(result.Nodes), len(result.Edges))

	// Build Graph
	g := graph.NewGraph(store)
	if err := g.BuildFromParseResult(ctx, result); err != nil {
		return fmt.Errorf("graph construction failed: %w", err)
	}

	if runGC {
		gcStore, ok := store.(storage.ValueLogGCer)
		if ok {
			if err := gcStore.RunValueLogGC(ctx, defaultValueLogGCDiscardRatio); err != nil {
				return fmt.Errorf("badger value-log GC failed: %w", err)
			}
		}
	}

	fmt.Printf("Analysis complete and saved to %s\n", dbPath)
	return nil
}

type workspaceAnalysisResult struct {
	RepoID string
	DBPath string
	Nodes  int
	Edges  int
}

func runAnalyzeWorkspace(cmd *cobra.Command, ctx context.Context, workspaceName string) error {
	if cmd.Flags().Changed("db") {
		return fmt.Errorf("--db cannot be used with --workspace; workspace mode stores each repo in its own database")
	}
	if cmd.Flags().Changed("module") {
		return fmt.Errorf("--module cannot be used with --workspace; workspace mode detects each repo module from go.mod")
	}

	ws, err := workspace.Load(workspaceName)
	if err != nil {
		return err
	}

	repos := sortedWorkspaceRepos(ws)
	if len(repos) == 0 {
		return fmt.Errorf("workspace %q has no repositories", workspaceName)
	}

	rebuild, _ := cmd.Flags().GetBool("rebuild")
	runGC, _ := cmd.Flags().GetBool("gc")

	fmt.Printf("Starting workspace analysis for %q (%d repos)...\n", ws.Name, len(repos))

	var mu sync.Mutex
	results := make([]workspaceAnalysisResult, 0, len(repos))
	group, groupCtx := errgroup.WithContext(ctx)

	for _, repo := range repos {
		repo := repo
		group.Go(func() error {
			result, err := analyzeWorkspaceRepo(groupCtx, ws, repo, rebuild, runGC)
			if err != nil {
				return err
			}
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return err
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].RepoID < results[j].RepoID
	})
	for _, result := range results {
		fmt.Printf("Repo %q: parsed %d nodes and %d edges, saved to %s\n", result.RepoID, result.Nodes, result.Edges, result.DBPath)
	}
	fmt.Printf("Workspace analysis complete for %q\n", ws.Name)
	return nil
}

func analyzeWorkspaceRepo(
	ctx context.Context,
	ws *workspace.Workspace,
	repo workspaceRepo,
	rebuild bool,
	runGC bool,
) (workspaceAnalysisResult, error) {
	stat, err := os.Stat(repo.Path)
	if err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("repo %q path is not accessible: %w", repo.ID, err)
	}
	if !stat.IsDir() {
		return workspaceAnalysisResult{}, fmt.Errorf("repo %q path is not a directory: %s", repo.ID, repo.Path)
	}

	dbPath := ws.GetRepoDBPath(repo.ID)
	if rebuild {
		if err := os.RemoveAll(dbPath); err != nil {
			return workspaceAnalysisResult{}, fmt.Errorf("failed to rebuild database for repo %q: %w", repo.ID, err)
		}
	}

	store, err := storage.NewBadgerStorage(dbPath)
	if err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("failed to open storage for repo %q: %w", repo.ID, err)
	}
	defer store.Close()

	modulePrefix := detectModulePrefix(repo.Path)
	if modulePrefix == "" {
		modulePrefix = repo.ID
	}

	p := parser.NewWorkspaceParser(modulePrefix, repo.ID, ws.Name)
	result, err := p.ParseWorkspace(ctx, repo.Path)
	if err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("parse repo %q failed: %w", repo.ID, err)
	}

	g := graph.NewGraph(store)
	if err := g.BuildFromParseResult(ctx, result); err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("graph construction for repo %q failed: %w", repo.ID, err)
	}

	if runGC {
		gcStore, ok := store.(storage.ValueLogGCer)
		if ok {
			if err := gcStore.RunValueLogGC(ctx, defaultValueLogGCDiscardRatio); err != nil {
				return workspaceAnalysisResult{}, fmt.Errorf("badger value-log GC for repo %q failed: %w", repo.ID, err)
			}
		}
	}

	return workspaceAnalysisResult{
		RepoID: repo.ID,
		DBPath: dbPath,
		Nodes:  len(result.Nodes),
		Edges:  len(result.Edges),
	}, nil
}

func rebuildBadgerDBPath(dbPath string, explicitDB bool) error {
	if err := validateRebuildDBPath(dbPath, explicitDB); err != nil {
		return err
	}
	rebuildPath := filepath.Clean(strings.TrimSpace(dbPath))

	info, err := os.Stat(rebuildPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect db path %q before rebuild: %w", dbPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to rebuild non-directory db path %q", dbPath)
	}
	if err := os.RemoveAll(rebuildPath); err != nil {
		return fmt.Errorf("remove db path %q before rebuild: %w", dbPath, err)
	}
	return nil
}

func validateRebuildDBPath(dbPath string, explicitDB bool) error {
	path := strings.TrimSpace(dbPath)
	if path == "" {
		return fmt.Errorf("refusing to rebuild empty db path")
	}

	clean := filepath.Clean(path)
	if clean == "." {
		return fmt.Errorf("refusing to rebuild current directory")
	}
	if clean == string(filepath.Separator) {
		return fmt.Errorf("refusing to rebuild filesystem root")
	}

	base := filepath.Base(clean)
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return fmt.Errorf("refusing to rebuild unsafe db path %q", dbPath)
	}

	abs, err := filepath.Abs(clean)
	if err != nil {
		return fmt.Errorf("resolve db path %q: %w", dbPath, err)
	}
	if filepath.Clean(abs) == string(filepath.Separator) {
		return fmt.Errorf("refusing to rebuild filesystem root")
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		homeAbs, err := filepath.Abs(home)
		if err == nil && filepath.Clean(abs) == filepath.Clean(homeAbs) {
			return fmt.Errorf("refusing to rebuild home directory")
		}
	}

	if !explicitDB && base != ".gokg" {
		return fmt.Errorf("refusing to rebuild %q without explicit --db; default rebuild paths must end in .gokg", dbPath)
	}

	return nil
}
