package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
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
	cmd.Flags().Bool("tests", false, "Include _test.go files in analysis")
	return cmd
}

func runAnalyze(cmd *cobra.Command, args []string) (err error) {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(out, "Starting analysis..."); err != nil {
		return err
	}
	startedAt := time.Now()
	ctx := cmd.Context()

	workspaceName, _ := cmd.Flags().GetString("workspace")
	if workspaceName != "" {
		return runAnalyzeWorkspace(cmd, ctx, workspaceName)
	}

	dbPath, _ := cmd.Flags().GetString("db")
	rebuild, _ := cmd.Flags().GetBool("rebuild")
	runGC, _ := cmd.Flags().GetBool("gc")
	includeTests, _ := cmd.Flags().GetBool("tests")

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	analysisRoot, err := resolveGoAnalysisRoot(dir)
	if err != nil {
		return err
	}

	if rebuild {
		if err := rebuildBadgerDBPath(dbPath, cmd.Flags().Changed("db")); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Rebuilding local database at %s...\n", dbPath); err != nil {
			return err
		}
	}

	// Init Storage
	store, err := storage.NewBadgerStorage(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	modulePrefix, _ := cmd.Flags().GetString("module")
	if modulePrefix == "" {
		modulePrefix = analysisRoot.ModulePrefix
		if modulePrefix == "" {
			modulePrefix = "gokg"
		}
	}

	// Parse Workspace
	p := parser.NewParser(modulePrefix, modulePrefix).WithTests(includeTests)
	if filepath.Clean(analysisRoot.Dir) != filepath.Clean(dir) {
		if _, err := fmt.Fprintf(out, "Analyzing Go module at %s\n", analysisRoot.Dir); err != nil {
			return err
		}
	}
	result, err := p.ParseWorkspace(ctx, analysisRoot.Dir)
	if err != nil {
		return fmt.Errorf("parse workspace failed: %w", err)
	}

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

	if err := printAnalyzeGraphSummary(out, "Graph Summary", g.Stats(), dbPath, time.Since(startedAt)); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Analysis complete.")
	return err
}

type workspaceAnalysisResult struct {
	RepoID string
	DBPath string
	Nodes  int
	Edges  int
	Time   time.Duration
}

func runAnalyzeWorkspace(cmd *cobra.Command, ctx context.Context, workspaceName string) error {
	out := cmd.OutOrStdout()
	startedAt := time.Now()

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
	includeTests, _ := cmd.Flags().GetBool("tests")

	if _, err := fmt.Fprintf(out, "Starting workspace analysis for %q (%d repos)...\n", ws.Name, len(repos)); err != nil {
		return err
	}

	var mu sync.Mutex
	results := make([]workspaceAnalysisResult, 0, len(repos))
	group, groupCtx := errgroup.WithContext(ctx)

	for _, repo := range repos {
		repo := repo
		group.Go(func() error {
			result, err := analyzeWorkspaceRepo(groupCtx, ws, repo, rebuild, runGC, includeTests)
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
	if err := printWorkspaceAnalyzeSummary(out, ws.Name, results, time.Since(startedAt)); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Workspace analysis complete.")
	return err
}

func analyzeWorkspaceRepo(
	ctx context.Context,
	ws *workspace.Workspace,
	repo workspaceRepo,
	rebuild bool,
	runGC bool,
	includeTests bool,
) (result workspaceAnalysisResult, err error) {
	startedAt := time.Now()

	stat, err := os.Stat(repo.Path)
	if err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("repo %q path is not accessible: %w", repo.ID, err)
	}
	if !stat.IsDir() {
		return workspaceAnalysisResult{}, fmt.Errorf("repo %q path is not a directory: %s", repo.ID, repo.Path)
	}
	analysisRoot, err := resolveGoAnalysisRoot(repo.Path)
	if err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("resolve repo %q Go root: %w", repo.ID, err)
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
	defer func() {
		if closeErr := store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	modulePrefix := analysisRoot.ModulePrefix
	if modulePrefix == "" {
		modulePrefix = repo.ID
	}

	p := parser.NewWorkspaceParser(modulePrefix, repo.ID, ws.Name).WithTests(includeTests)
	parseResult, err := p.ParseWorkspace(ctx, analysisRoot.Dir)
	if err != nil {
		return workspaceAnalysisResult{}, fmt.Errorf("parse repo %q failed: %w", repo.ID, err)
	}

	g := graph.NewGraph(store)
	if err := g.BuildFromParseResult(ctx, parseResult); err != nil {
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

	stats := g.Stats()
	return workspaceAnalysisResult{
		RepoID: repo.ID,
		DBPath: dbPath,
		Nodes:  stats.NodeCount,
		Edges:  stats.EdgeCount,
		Time:   time.Since(startedAt),
	}, nil
}

func printAnalyzeGraphSummary(out io.Writer, title string, stats graph.Stats, dbPath string, duration time.Duration) error {
	if _, err := fmt.Fprintf(out, "\n%s:\n", title); err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "Metric\tValue"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Nodes\t%d\n", stats.NodeCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Edges\t%d\n", stats.EdgeCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Source files\t%d\n", stats.SourceFileCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Analysis time\t%s\n", formatDuration(duration)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Database\t%s\n", dbPath); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if err := printCountTable(out, "Nodes by Kind", stats.NodesByKind); err != nil {
		return err
	}
	return printCountTable(out, "Edges by Kind", stats.EdgesByKind)
}

func printWorkspaceAnalyzeSummary(out io.Writer, workspaceName string, results []workspaceAnalysisResult, duration time.Duration) error {
	if _, err := fmt.Fprintf(out, "\nWorkspace Graph Summary: %s\n", workspaceName); err != nil {
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "Repo\tNodes\tEdges\tTime\tDatabase"); err != nil {
		return err
	}

	totalNodes := 0
	totalEdges := 0
	for _, result := range results {
		totalNodes += result.Nodes
		totalEdges += result.Edges
		if _, err := fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\n", result.RepoID, result.Nodes, result.Edges, formatDuration(result.Time), result.DBPath); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "TOTAL\t%d\t%d\t%s\t%d repos\n", totalNodes, totalEdges, formatDuration(duration), len(results)); err != nil {
		return err
	}
	return w.Flush()
}

func formatDuration(duration time.Duration) string {
	if duration < time.Second {
		return duration.Round(time.Millisecond).String()
	}
	return duration.Round(10 * time.Millisecond).String()
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
