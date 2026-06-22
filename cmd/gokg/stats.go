package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
)

type statsReport struct {
	Source                string      `json:"source"`
	DBPaths               []string    `json:"db_paths"`
	DBSizeBytes           int64       `json:"db_size_bytes"`
	ProcessHeapAllocBytes uint64      `json:"process_heap_alloc_bytes"`
	Graph                 graph.Stats `json:"graph"`
}

var statsCmd = newStatsCommand()

func newStatsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show graph statistics (nodes, edges, files, DB size, RAM estimate)",
		RunE:  runStats,
	}
	cmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	cmd.Flags().String("workspace", "", "Workspace name to inspect by merging per-repo databases")
	cmd.Flags().Bool("json", false, "Print machine-readable JSON")
	return cmd
}

func runStats(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dbPath, _ := cmd.Flags().GetString("db")
	workspaceName, _ := cmd.Flags().GetString("workspace")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	g, dbPaths, source, err := loadStatsGraph(ctx, cmd, dbPath, workspaceName)
	if err != nil {
		return err
	}

	var dbSize int64
	for _, path := range dbPaths {
		size, err := dirSize(path)
		if err != nil {
			return fmt.Errorf("calculate db size for %s: %w", path, err)
		}
		dbSize += size
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	report := statsReport{
		Source:                source,
		DBPaths:               dbPaths,
		DBSizeBytes:           dbSize,
		ProcessHeapAllocBytes: mem.Alloc,
		Graph:                 g.Stats(),
	}

	out := cmd.OutOrStdout()
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	return printStatsReport(out, report)
}

func loadStatsGraph(ctx context.Context, cmd *cobra.Command, dbPath string, workspaceName string) (g *graph.Graph, dbPaths []string, source string, err error) {
	if workspaceName != "" {
		if cmd.Flags().Changed("db") {
			return nil, nil, "", fmt.Errorf("--db cannot be used with --workspace; workspace mode loads per-repo databases")
		}

		ws, err := workspace.Load(workspaceName)
		if err != nil {
			return nil, nil, "", err
		}
		g, err := loadWorkspaceGraph(ctx, workspaceName)
		if err != nil {
			return nil, nil, "", err
		}

		repos := sortedWorkspaceRepos(ws)
		dbPaths := make([]string, 0, len(repos))
		for _, repo := range repos {
			dbPaths = append(dbPaths, ws.GetRepoDBPath(repo.ID))
		}
		return g, dbPaths, statsSource("workspace", workspaceName), nil
	}

	store, err := storage.NewBadgerStorageReadOnly(dbPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to open storage: %w", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	g = graph.NewGraph(store)
	if err := g.LoadFromStorage(ctx); err != nil {
		return nil, nil, "", fmt.Errorf("failed to load graph: %w", err)
	}
	return g, []string{dbPath}, statsSource("database", dbPath), nil
}

func statsSource(kind string, value string) string {
	return fmt.Sprintf("%s \"%s\"", kind, value)
}

func printStatsReport(out io.Writer, report statsReport) error {
	if _, err := fmt.Fprintln(out, "GoKG Graph Statistics"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Source: %s\n", report.Source); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "DB Size: %s (%d bytes)\n", formatBytes(report.DBSizeBytes), report.DBSizeBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Graph RAM Estimate: %s (%d bytes)\n", formatBytes(report.Graph.RAMEstimateBytes), report.Graph.RAMEstimateBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Process Heap Alloc: %s (%d bytes)\n\n", formatBytes(int64(report.ProcessHeapAllocBytes)), report.ProcessHeapAllocBytes); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "Nodes: %d\n", report.Graph.NodeCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Edges: %d\n", report.Graph.EdgeCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "File Nodes: %d\n", report.Graph.FileNodeCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Source Files: %d\n\n", report.Graph.SourceFileCount); err != nil {
		return err
	}

	if err := printCountTable(out, "Nodes by Kind", report.Graph.NodesByKind); err != nil {
		return err
	}
	if err := printCountTable(out, "Edges by Kind", report.Graph.EdgesByKind); err != nil {
		return err
	}
	if err := printCountTable(out, "Nodes by Repo", report.Graph.NodesByRepo); err != nil {
		return err
	}
	if err := printCountTable(out, "Edges by Repo", report.Graph.EdgesByRepo); err != nil {
		return err
	}
	return printPackageTable(out, "Top Packages by Nodes", report.Graph.TopPackagesByNodes)
}

func printCountTable(out io.Writer, title string, counts map[string]int) error {
	if len(counts) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s:\n", title); err != nil {
		return err
	}
	for _, item := range sortedCounts(counts) {
		if _, err := fmt.Fprintf(out, "  %-18s %d\n", item.Name, item.Count); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out)
	return err
}

func printPackageTable(out io.Writer, title string, packages []graph.PackageStat) error {
	if len(packages) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s:\n", title); err != nil {
		return err
	}
	for _, pkg := range packages {
		if _, err := fmt.Fprintf(out, "  %-6d %s\n", pkg.Nodes, pkg.PkgPath); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out)
	return err
}

type countItem struct {
	Name  string
	Count int
}

func sortedCounts(counts map[string]int) []countItem {
	items := make([]countItem, 0, len(counts))
	for name, count := range counts {
		items = append(items, countItem{Name: name, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})
	return items
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
