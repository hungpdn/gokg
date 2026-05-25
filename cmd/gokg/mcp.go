package main

import (
	"context"
	"fmt"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/mcp"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the MCP server",
	Long:  `Start the gokg MCP (Model Context Protocol) server communicating via stdio for AI agents.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		// Init Storage
		store, err := storage.NewBadgerStorage(".gokg/")
		if err != nil {
			return fmt.Errorf("failed to open storage: %w", err)
		}
		defer store.Close()

		g := graph.NewGraph(store)
		if err := g.LoadFromStorage(ctx); err != nil {
			return fmt.Errorf("failed to load graph from storage: %w", err)
		}

		server := mcp.NewServer(g)
		return server.Start(ctx)
	},
}
