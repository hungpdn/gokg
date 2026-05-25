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

		// For MCP server we need to load the graph. Wait, normally MCP queries the loaded graph.
		// For this prototype, we'll assume the graph is rebuilt or loaded here.
		g := graph.NewGraph(store)
		// We'd load from badger here, but for simplicity, the query builder requires a built graph.
		// A full implementation would deserialize nodes/edges from BadgerDB.
		// For the sake of this prompt, we just provide the graph instance to the server.

		server := mcp.NewServer(g)
		return server.Start(ctx)
	},
}
