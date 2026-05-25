package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze the current Go workspace",
	Long:  `Parse the current Go project, build the semantic knowledge graph, and save it to the local storage.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Starting analysis...")
		ctx := context.Background()

		// Init Storage
		store, err := storage.NewBadgerStorage(".gokg/")
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

		fmt.Println("Analysis complete and saved to .gokg/")
		return nil
	},
}

func init() {
	analyzeCmd.Flags().StringP("module", "m", "", "Module prefix for internal packages")
}
