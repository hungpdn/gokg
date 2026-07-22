package main

import (
	"github.com/spf13/cobra"
)

const defaultDBPath = ".gokg/"

var rootCmd = &cobra.Command{
	Use:           "gokg",
	Short:         "Golang Knowledge Graph (gokg) is a local MCP server for Go semantic analysis",
	SilenceUsage:  true,
	SilenceErrors: true,
	Long: `gokg (Golang Knowledge Graph) transforms Go source code into a Semantic Knowledge Graph.
It acts as a Local MCP Server providing ultra-deep Go architectural context to AI Coding Agents.
It performs deep indexing of internal packages, boundary nodes, and concurrency flows (Goroutines, Channels).`,
}

func init() {
	// Add subcommands here
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(impactCmd)
	rootCmd.AddCommand(telemetryCmd)
	rootCmd.AddCommand(versionCmd)
}
