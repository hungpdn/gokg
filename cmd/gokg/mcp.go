package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/mcp"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/watcher"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the MCP server",
	Long:  `Start the gokg MCP (Model Context Protocol) server communicating via stdio for AI agents.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		dbPath, _ := cmd.Flags().GetString("db")
		enableWatch, _ := cmd.Flags().GetBool("watch")
		modulePrefix, _ := cmd.Flags().GetString("module")
		workspaceName, _ := cmd.Flags().GetString("workspace")

		// Detect module automatically if not provided
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

		if workspaceName != "" {
			if cmd.Flags().Changed("db") {
				return fmt.Errorf("--db cannot be used with --workspace; workspace mode loads per-repo databases")
			}

			g, stores, err := loadWorkspaceGraph(ctx, workspaceName)
			if err != nil {
				return err
			}
			defer closeStores(stores)

			if enableWatch {
				ws, err := workspace.Load(workspaceName)
				if err != nil {
					log.Printf("Warning: Failed to load workspace for watch: %v", err)
				} else {
					for repoID, repoPath := range ws.Config.Repos {
						repoModule := detectModulePrefix(repoPath)
						if repoModule == "" {
							repoModule = repoID
						}
						p := parser.NewWorkspaceParser(repoModule, repoID, workspaceName)
						w, err := watcher.NewWatcher(g, p, repoPath)
						if err != nil {
							log.Printf("Warning: Failed to initialize watcher for repo %q: %v", repoID, err)
							continue
						}
						if err := w.Start(ctx); err != nil {
							log.Printf("Warning: Failed to start watcher for repo %q: %v", repoID, err)
							continue
						}
						log.Printf("File watcher started for repo %q (%s)", repoID, repoPath)
					}
				}
			}

			server := mcp.NewServer(g)
			return server.Start(ctx)
		}

		// Init Storage
		store, err := storage.NewBadgerStorage(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open storage: %w", err)
		}
		defer store.Close()

		g := graph.NewGraph(store)
		if err := g.LoadFromStorage(ctx); err != nil {
			return fmt.Errorf("failed to load graph from storage: %w", err)
		}

		if enableWatch {
			p := parser.NewParser(modulePrefix, modulePrefix)
			w, err := watcher.NewWatcher(g, p, ".")
			if err != nil {
				log.Printf("Warning: Failed to initialize file watcher: %v", err)
			} else {
				if err := w.Start(ctx); err != nil {
					log.Printf("Warning: Failed to start file watcher: %v", err)
				} else {
					log.Println("File watcher started successfully for incremental updates.")
				}
			}
		}

		server := mcp.NewServer(g)
		return server.Start(ctx)
	},
}

func init() {
	mcpCmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	mcpCmd.Flags().String("workspace", "", "Workspace name to serve by merging per-repo databases")
	mcpCmd.Flags().Bool("watch", true, "Enable real-time incremental analysis on file change")
	mcpCmd.Flags().String("module", "", "Module prefix for internal packages")
}
