package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the parsed graph into various formats (json, mermaid, dot)",
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		logOut := cmd.ErrOrStderr()
		dataOut := cmd.OutOrStdout()
		dbPath, _ := cmd.Flags().GetString("db")
		format, _ := cmd.Flags().GetString("format")
		outFile, _ := cmd.Flags().GetString("out")
		workspaceName, _ := cmd.Flags().GetString("workspace")

		ctx := cmd.Context()
		var g *graph.Graph

		if workspaceName != "" {
			if cmd.Flags().Changed("db") {
				return fmt.Errorf("--db cannot be used with --workspace; workspace mode loads per-repo databases")
			}

			if _, err := fmt.Fprintf(logOut, "Loading workspace graph from %q...\n", workspaceName); err != nil {
				return err
			}
			g, err = loadWorkspaceGraph(ctx, workspaceName)
			if err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(logOut, "Loading graph from %s...\n", dbPath); err != nil {
				return err
			}
			store, err := storage.NewBadgerStorageReadOnly(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open storage: %w", err)
			}
			defer func() {
				if closeErr := store.Close(); closeErr != nil && err == nil {
					err = closeErr
				}
			}()

			g = graph.NewGraph(store)
			if err := g.LoadFromStorage(ctx); err != nil {
				return fmt.Errorf("failed to load graph: %w", err)
			}
		}

		output := dataOut
		var outputFile *os.File
		if outFile != "" {
			outputFile, err = os.Create(outFile)
			if err != nil {
				return fmt.Errorf("failed to open output file: %w", err)
			}
			defer func() {
				if outputFile != nil {
					if closeErr := outputFile.Close(); closeErr != nil && err == nil {
						err = fmt.Errorf("failed to close output file: %w", closeErr)
					}
				}
			}()
			output = outputFile
		}

		bufferedOutput := bufio.NewWriter(output)
		switch format {
		case "json":
			err = g.ExportJSONTo(bufferedOutput)
		case "mermaid":
			err = g.ExportMermaidTo(bufferedOutput)
		case "dot":
			err = g.ExportDotTo(bufferedOutput)
		default:
			return fmt.Errorf("unknown format: %s", format)
		}
		if err != nil {
			return err
		}
		if err := bufferedOutput.Flush(); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}

		if outFile != "" {
			if err := outputFile.Close(); err != nil {
				return fmt.Errorf("failed to close output file: %w", err)
			}
			outputFile = nil
			if _, err := fmt.Fprintf(dataOut, "Exported successfully to %s\n", outFile); err != nil {
				return err
			}
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
