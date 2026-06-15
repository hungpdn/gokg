package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the parsed graph into various formats (json, mermaid, dot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dbPath, _ := cmd.Flags().GetString("db")
		format, _ := cmd.Flags().GetString("format")
		outFile, _ := cmd.Flags().GetString("out")
		workspaceName, _ := cmd.Flags().GetString("workspace")

		ctx := context.Background()
		var g *graph.Graph
		var err error

		if workspaceName != "" {
			if cmd.Flags().Changed("db") {
				return fmt.Errorf("--db cannot be used with --workspace; workspace mode loads per-repo databases")
			}

			fmt.Printf("Loading workspace graph from %q...\n", workspaceName)
			g, err = loadWorkspaceGraph(ctx, workspaceName)
			if err != nil {
				return err
			}
		} else {
			fmt.Printf("Loading graph from %s...\n", dbPath)
			store, err := storage.NewBadgerStorage(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open storage: %w", err)
			}
			defer store.Close()

			g = graph.NewGraph(store)
			if err := g.LoadFromStorage(ctx); err != nil {
				return fmt.Errorf("failed to load graph: %w", err)
			}
		}

		var output string
		switch format {
		case "json":
			output, err = g.ExportJSON()
			if err != nil {
				return err
			}
		case "mermaid":
			output = g.ExportMermaid()
		case "dot":
			output = g.ExportDot()
		default:
			return fmt.Errorf("unknown format: %s", format)
		}

		if outFile != "" {
			err = os.WriteFile(outFile, []byte(output), 0644)
			if err != nil {
				return fmt.Errorf("failed to write output: %w", err)
			}
			fmt.Printf("Exported successfully to %s\n", outFile)
		} else {
			fmt.Println(output)
		}

		return nil
	},
}

func init() {
	exportCmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	exportCmd.Flags().String("workspace", "", "Workspace name to export by merging per-repo databases")
	exportCmd.Flags().String("format", "json", "Output format (json, mermaid, dot)")
	exportCmd.Flags().String("out", "", "Output file path (leave empty for stdout)")
}
