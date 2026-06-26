package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
	"github.com/hungpdn/gokg/internal/mcp"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	"github.com/hungpdn/gokg/internal/watcher"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
)

const (
	watchStorageOpenTimeout = 5 * time.Second
	watchStorageOpenDelay   = 250 * time.Millisecond
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start the MCP server",
	Long:  `Start the gokg MCP (Model Context Protocol) server for AI agents. By default it communicates over stdio; pass --http to serve JSON-RPC over HTTP.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		dbPath, _ := cmd.Flags().GetString("db")
		enableWatch, _ := cmd.Flags().GetBool("watch")
		modulePrefix, _ := cmd.Flags().GetString("module")
		workspaceName, _ := cmd.Flags().GetString("workspace")
		httpMode, _ := cmd.Flags().GetBool("http")
		httpAddr, _ := cmd.Flags().GetString("addr")
		httpPath, _ := cmd.Flags().GetString("path")

		if workspaceName != "" {
			if cmd.Flags().Changed("db") {
				return fmt.Errorf("--db cannot be used with --workspace; workspace mode loads per-repo databases")
			}
			if cmd.Flags().Changed("module") {
				return fmt.Errorf("--module cannot be used with --workspace; workspace mode detects each repo module from go.mod")
			}

			ws, err := workspace.Load(workspaceName)
			if err != nil {
				return err
			}
			repos := sortedWorkspaceRepos(ws)
			g, err := loadWorkspaceGraph(ctx, workspaceName)
			if err != nil {
				return err
			}
			impactRepos := make([]impact.Repo, 0, len(repos))
			for _, repo := range repos {
				impactRepos = append(impactRepos, impact.Repo{ID: repo.ID, Root: repo.Path})
			}

			if enableWatch {
				for _, repo := range repos {
					analysisRoot, err := resolveGoAnalysisRoot(repo.Path)
					if err != nil {
						log.Printf("Warning: Failed to resolve Go root for repo %q: %v", repo.ID, err)
						continue
					}
					repoModule := analysisRoot.ModulePrefix
					if repoModule == "" {
						repoModule = repo.ID
					}
					p := parser.NewWorkspaceParser(repoModule, repo.ID, workspaceName)
					w, err := watcher.NewWatcher(g, p, analysisRoot.Dir)
					if err != nil {
						log.Printf("Warning: Failed to initialize watcher for repo %q: %v", repo.ID, err)
						continue
					}
					repoID := repo.ID
					dbPath := ws.GetRepoDBPath(repoID)
					w.SetUpdateRunner(func(ctx context.Context, update func(context.Context) error) (err error) {
						store, err := openWatchStorage(ctx, dbPath)
						if err != nil {
							return fmt.Errorf("open watch storage for repo %q: %w", repoID, err)
						}
						g.SetRepoStore(repoID, store)
						defer g.SetRepoStore(repoID, nil)
						defer func() {
							if closeErr := store.Close(); closeErr != nil && err == nil {
								err = closeErr
							}
						}()

						return update(ctx)
					})
					if err := w.Start(ctx); err != nil {
						log.Printf("Warning: Failed to start watcher for repo %q: %v", repo.ID, err)
						continue
					}
					log.Printf("File watcher started for repo %q (%s)", repo.ID, repo.Path)
				}
			}

			server := mcp.NewServer(g, mcp.WithImpactRepos(impactRepos))
			return startMCPTransport(ctx, cmd, server, httpMode, httpAddr, httpPath)
		}

		analysisRoot, err := resolveGoAnalysisRoot(".")
		if err != nil {
			return err
		}
		// Detect module automatically if not provided.
		if modulePrefix == "" {
			modulePrefix = analysisRoot.ModulePrefix
			if modulePrefix == "" {
				modulePrefix = "gokg"
			}
		}

		// Init Storage
		store, err := storage.NewBadgerStorage(dbPath)
		if err != nil {
			return fmt.Errorf("failed to open storage: %w", err)
		}

		g := graph.NewGraph(store)
		if err := g.LoadFromStorage(ctx); err != nil {
			if closeErr := store.Close(); closeErr != nil {
				return fmt.Errorf("failed to load graph from storage: %w; additionally failed to close storage: %v", err, closeErr)
			}
			return fmt.Errorf("failed to load graph from storage: %w", err)
		}
		if err := store.Close(); err != nil {
			return err
		}
		g.SetStore(nil)

		if enableWatch {
			p := parser.NewParser(modulePrefix, modulePrefix)
			w, err := watcher.NewWatcher(g, p, analysisRoot.Dir)
			if err != nil {
				log.Printf("Warning: Failed to initialize file watcher: %v", err)
			} else {
				w.SetUpdateRunner(func(ctx context.Context, update func(context.Context) error) (err error) {
					store, err := openWatchStorage(ctx, dbPath)
					if err != nil {
						return fmt.Errorf("open watch storage: %w", err)
					}
					g.SetStore(store)
					defer g.SetStore(nil)
					defer func() {
						if closeErr := store.Close(); closeErr != nil && err == nil {
							err = closeErr
						}
					}()

					return update(ctx)
				})
				if err := w.Start(ctx); err != nil {
					log.Printf("Warning: Failed to start file watcher: %v", err)
				} else {
					log.Println("File watcher started successfully for incremental updates.")
				}
			}
		}

		server := mcp.NewServer(g, mcp.WithImpactRepos([]impact.Repo{{ID: modulePrefix, Root: analysisRoot.Dir}}))
		return startMCPTransport(ctx, cmd, server, httpMode, httpAddr, httpPath)
	},
}

func init() {
	mcpCmd.Flags().String("db", defaultDBPath, "Path to BadgerDB directory")
	mcpCmd.Flags().String("workspace", "", "Workspace name to serve by merging per-repo databases")
	mcpCmd.Flags().Bool("watch", true, "Enable real-time incremental analysis on file change")
	mcpCmd.Flags().String("module", "", "Module prefix for internal packages")
	mcpCmd.Flags().Bool("http", false, "Serve MCP over HTTP instead of stdio")
	mcpCmd.Flags().String("addr", "127.0.0.1:8080", "HTTP MCP listen address")
	mcpCmd.Flags().String("path", "/mcp", "HTTP MCP endpoint path")
}

func startMCPTransport(ctx context.Context, cmd *cobra.Command, server *mcp.Server, httpMode bool, httpAddr string, httpPath string) error {
	if !httpMode {
		return server.Start(ctx)
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "HTTP MCP server listening at %s\n", mcpHTTPURL(httpAddr, httpPath)); err != nil {
		return err
	}
	return server.StartHTTP(ctx, httpAddr, httpPath)
}

func mcpHTTPURL(addr string, path string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/mcp"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "http://" + addr + path
}

type badgerStorageOpener func(string) (storage.Storage, error)

func openWatchStorage(ctx context.Context, dbPath string) (storage.Storage, error) {
	return openWatchStorageWithRetry(ctx, dbPath, storage.NewBadgerStorage, watchStorageOpenTimeout, watchStorageOpenDelay)
}

func openWatchStorageWithRetry(
	ctx context.Context,
	dbPath string,
	open badgerStorageOpener,
	timeout time.Duration,
	delay time.Duration,
) (storage.Storage, error) {
	if delay <= 0 {
		delay = watchStorageOpenDelay
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		store, err := open(dbPath)
		if err == nil {
			return store, nil
		}
		if !isLikelyBadgerLockError(err) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("%w; Badger database %q is still locked after %s. Stop other gokg processes using this DB or use --db to select a different database", err, dbPath, timeout)
		case <-time.After(delay):
		}
	}
}

func isLikelyBadgerLockError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot acquire directory lock") ||
		strings.Contains(msg, "resource temporarily unavailable")
}
