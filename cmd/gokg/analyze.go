package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
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
	cmd.Flags().Bool("rebuild", false, "Delete and rebuild the local database before analysis")
	cmd.Flags().Bool("gc", true, "Run Badger value-log garbage collection after analysis")
	return cmd
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	fmt.Println("Starting analysis...")
	ctx := context.Background()

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
	p := parser.NewParser(modulePrefix)
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
