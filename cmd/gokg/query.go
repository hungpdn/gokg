package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hungpdn/gokg/internal/cypher"
	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
)

var queryCmd = newQueryCommand()

func newQueryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <cypher-string>",
		Short: "Execute a Cypher query against the knowledge graph",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			queryString := args[0]
			ctx := context.Background()
			logOut := cmd.ErrOrStderr()

			if _, err := fmt.Fprintln(logOut, "Parsing query..."); err != nil {
				return err
			}
			q, err := cypher.NewParser(cypher.NewLexer(queryString)).ParseQuery()
			if err != nil {
				return fmt.Errorf("failed to parse cypher: %w", err)
			}

			dbPath, _ := cmd.Flags().GetString("db")
			workspaceName, _ := cmd.Flags().GetString("workspace")
			start := time.Now()

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
			if _, err := fmt.Fprintf(logOut, "Graph loaded in %v.\n", time.Since(start)); err != nil {
				return err
			}

			qb := g.Query()
			if _, err := fmt.Fprintln(logOut, "Executing query..."); err != nil {
				return err
			}
			start = time.Now()

			results, err := qb.ExecuteCypher(q)
			if err != nil {
				return fmt.Errorf("execution error: %w", err)
			}

			if _, err := fmt.Fprintf(logOut, "Query completed in %v. Found %d row(s).\n\n", time.Since(start), len(results)); err != nil {
				return err
			}

			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(results)
		},
	}

	cmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	cmd.Flags().String("workspace", "", "Workspace name to query by merging per-repo databases")
	return cmd
}
