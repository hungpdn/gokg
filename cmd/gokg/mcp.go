package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
	"github.com/hungpdn/gokg/internal/mcp"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
	telemetrypkg "github.com/hungpdn/gokg/internal/telemetry"
	"github.com/hungpdn/gokg/internal/watcher"
	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
)

const (
	watchStorageOpenTimeout = 5 * time.Second
	watchStorageOpenDelay   = 250 * time.Millisecond
)

var mcpCmd = &cobra.Command{
	Use:     "mcp",
	Short:   "Start the MCP server",
	Long:    `Start the gokg MCP (Model Context Protocol) server for AI agents. By default it communicates over stdio; pass --http to serve JSON-RPC over HTTP.`,
	Args:    cobra.NoArgs,
	PreRunE: validateMCPTelemetryFlags,
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
			impactRepos, err := workspaceImpactReposWithMetadata(ctx, ws)
			if err != nil {
				return err
			}
			for i := range impactRepos {
				attachAnalysisMetadataLoader(&impactRepos[i], ws.GetRepoDBPath(impactRepos[i].ID))
			}

			if enableWatch {
				impactReposByID := impactRepoPointersByID(impactRepos)
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
					includeTests := false
					if impactRepo := impactReposByID[repo.ID]; impactRepo != nil && impactRepo.AnalysisMetadata != nil {
						includeTests = impactRepo.AnalysisMetadata.IncludeTests
					}
					p := parser.NewWorkspaceParser(repoModule, repo.ID, workspaceName).WithTests(includeTests)
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

						if err := update(ctx); err != nil {
							return err
						}
						return saveAnalysisMetadata(ctx, store, repoID, analysisRoot.Dir, repoModule, workspaceName, includeTests)
					})
					if err := w.Start(ctx); err != nil {
						log.Printf("Warning: Failed to start watcher for repo %q: %v", repo.ID, err)
						continue
					}
					log.Printf("File watcher started for repo %q (%s)", repo.ID, repo.Path)
				}
			}

			opts, closeTelemetry, err := mcpServerOptionsFromFlags(cmd, impactRepos)
			if err != nil {
				return err
			}
			defer closeMcpTelemetry(closeTelemetry)
			server := mcp.NewServer(g, opts...)
			return startMCPTransport(ctx, cmd, server, httpMode, httpAddr, httpPath)
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
		analysisMeta, hasAnalysisMeta, err := graph.LoadAnalysisMetadata(ctx, store)
		if err != nil {
			if closeErr := store.Close(); closeErr != nil {
				return fmt.Errorf("failed to load graph metadata: %w; additionally failed to close storage: %v", err, closeErr)
			}
			return err
		}
		if err := store.Close(); err != nil {
			return err
		}
		g.SetStore(nil)

		analysisRoot, impactRepo, err := resolveSingleGraphImpactRepo(g, ".", modulePrefix)
		if err != nil {
			return err
		}
		if hasAnalysisMeta {
			impactRepo.AnalysisMetadata = &analysisMeta
		}
		attachAnalysisMetadataLoader(&impactRepo, dbPath)
		// Detect module automatically if not provided.
		if modulePrefix == "" {
			modulePrefix = analysisRoot.ModulePrefix
			if modulePrefix == "" {
				modulePrefix = impactRepo.ID
			}
			if modulePrefix == "" {
				modulePrefix = "gokg"
			}
		}

		if enableWatch {
			includeTests := false
			if impactRepo.AnalysisMetadata != nil {
				includeTests = impactRepo.AnalysisMetadata.IncludeTests
			}
			p := parser.NewParser(modulePrefix, modulePrefix).WithTests(includeTests)
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

					if err := update(ctx); err != nil {
						return err
					}
					return saveAnalysisMetadata(ctx, store, impactRepo.ID, analysisRoot.Dir, modulePrefix, "", includeTests)
				})
				if err := w.Start(ctx); err != nil {
					log.Printf("Warning: Failed to start file watcher: %v", err)
				} else {
					log.Println("File watcher started successfully for incremental updates.")
				}
			}
		}

		opts, closeTelemetry, err := mcpServerOptionsFromFlags(cmd, []impact.Repo{impactRepo})
		if err != nil {
			return err
		}
		defer closeMcpTelemetry(closeTelemetry)
		server := mcp.NewServer(g, opts...)
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
	addMCPTelemetryFlags(mcpCmd)
}

func addMCPTelemetryFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("telemetry", false, "Record MCP tool-call telemetry to a local JSONL file")
	cmd.Flags().String("telemetry-file", telemetrypkg.DefaultFile, "Path to MCP telemetry JSONL file (requires --telemetry; custom paths must be outside --db)")
	cmd.Flags().Int64("telemetry-max-bytes", telemetrypkg.DefaultMaxFileBytes, "Rotate MCP telemetry before the active JSONL file exceeds this size in bytes (requires --telemetry)")
	cmd.Flags().Int("telemetry-max-backups", telemetrypkg.DefaultMaxBackups, "Number of rotated MCP telemetry files to retain; 0 retains none (requires --telemetry)")
}

func validateMCPTelemetryFlags(cmd *cobra.Command, _ []string) error {
	enabled, _ := cmd.Flags().GetBool("telemetry")
	for _, flagName := range []string{"telemetry-file", "telemetry-max-bytes", "telemetry-max-backups"} {
		if !enabled && cmd.Flags().Changed(flagName) {
			return fmt.Errorf("--%s requires --telemetry", flagName)
		}
	}
	if !enabled {
		return nil
	}

	path, _ := cmd.Flags().GetString("telemetry-file")
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("--telemetry-file must not be empty")
	}
	maxFileBytes, _ := cmd.Flags().GetInt64("telemetry-max-bytes")
	if maxFileBytes < 0 {
		return fmt.Errorf("--telemetry-max-bytes must be greater than or equal to 0")
	}
	maxBackups, _ := cmd.Flags().GetInt("telemetry-max-backups")
	if maxBackups < 0 {
		return fmt.Errorf("--telemetry-max-backups must be greater than or equal to 0")
	}

	_, err := validatedMCPTelemetryFilePath(cmd, path)
	return err
}

func validatedMCPTelemetryFilePath(cmd *cobra.Command, path string) (string, error) {
	resolvedPath, err := resolvePathForContainment(path)
	if err != nil {
		return "", fmt.Errorf("resolve MCP telemetry path %q: %w", path, err)
	}
	workspaceName, _ := cmd.Flags().GetString("workspace")
	if strings.TrimSpace(workspaceName) != "" {
		ws, err := workspace.Load(workspaceName)
		if err != nil {
			return "", err
		}
		if err := validateMCPTelemetryWorkspacePaths(ws, resolvedPath); err != nil {
			return "", err
		}
		return resolvedPath, nil
	}
	dbPath, _ := cmd.Flags().GetString("db")
	if err := validateMCPTelemetryFilePath(dbPath, resolvedPath); err != nil {
		return "", err
	}
	return resolvedPath, nil
}

func validateMCPTelemetryFilePath(dbPath string, telemetryPath string) error {
	absDB, err := resolvePathForContainment(dbPath)
	if err != nil {
		return fmt.Errorf("resolve MCP database path %q: %w", dbPath, err)
	}
	absTelemetry, err := resolvePathForContainment(telemetryPath)
	if err != nil {
		return fmt.Errorf("resolve MCP telemetry path %q: %w", telemetryPath, err)
	}

	rel, insideDB, err := telemetryPathRelativeToDB(absDB, absTelemetry)
	if err != nil {
		return fmt.Errorf("compare MCP telemetry path %q with database path %q: %w", telemetryPath, dbPath, err)
	}
	if !insideDB {
		return nil
	}
	defaultName := filepath.Base(telemetrypkg.DefaultFile)
	if rel == defaultName {
		return nil
	}
	return fmt.Errorf("--telemetry-file %q must be outside --db %q; only the root %q file is preserved by analyze --rebuild", telemetryPath, dbPath, defaultName)
}

func telemetryPathRelativeToDB(absDB, absTelemetry string) (string, bool, error) {
	if dbInfo, err := os.Stat(absDB); err == nil {
		current := absTelemetry
		parts := make([]string, 0, 4)
		for {
			if info, statErr := os.Stat(current); statErr == nil {
				if os.SameFile(dbInfo, info) {
					for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
						parts[left], parts[right] = parts[right], parts[left]
					}
					if len(parts) == 0 {
						return ".", true, nil
					}
					return filepath.Join(parts...), true, nil
				}
			} else if !os.IsNotExist(statErr) {
				return "", false, statErr
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			parts = append(parts, filepath.Base(current))
			current = parent
		}
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	rel, err := filepath.Rel(absDB, absTelemetry)
	if err != nil {
		if !strings.EqualFold(filepath.VolumeName(absDB), filepath.VolumeName(absTelemetry)) {
			return "", false, nil
		}
		return "", false, err
	}
	inside := pathRelIsInside(rel)
	if !inside && filesystemIsCaseInsensitive(absDB) {
		foldedRel, foldErr := filepath.Rel(strings.ToLower(absDB), strings.ToLower(absTelemetry))
		if foldErr == nil && pathRelIsInside(foldedRel) {
			return rel, true, nil
		}
	}
	return rel, inside, nil
}

func pathRelIsInside(rel string) bool {
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func filesystemIsCaseInsensitive(path string) bool {
	current := path
	for {
		info, err := os.Stat(current)
		if err == nil {
			base := filepath.Base(current)
			for index, r := range base {
				var replacement rune
				switch {
				case r >= 'a' && r <= 'z':
					replacement = r - ('a' - 'A')
				case r >= 'A' && r <= 'Z':
					replacement = r + ('a' - 'A')
				default:
					continue
				}
				alternateBase := base[:index] + string(replacement) + base[index+len(string(r)):]
				alternateInfo, alternateErr := os.Stat(filepath.Join(filepath.Dir(current), alternateBase))
				return alternateErr == nil && os.SameFile(info, alternateInfo)
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func validateMCPTelemetryWorkspacePaths(ws *workspace.Workspace, telemetryPath string) error {
	if ws == nil {
		return fmt.Errorf("workspace is nil")
	}
	for _, repo := range sortedWorkspaceRepos(ws) {
		if err := validateMCPTelemetryFilePath(ws.GetRepoDBPath(repo.ID), telemetryPath); err != nil {
			return fmt.Errorf("workspace repo %q: %w", repo.ID, err)
		}
	}
	return nil
}

func resolvePathForContainment(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}

	current := absPath
	missing := make([]string, 0, 4)
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot find an existing ancestor of %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func mcpServerOptionsFromFlags(cmd *cobra.Command, repos []impact.Repo) ([]mcp.ServerOption, func() error, error) {
	opts := []mcp.ServerOption{mcp.WithImpactRepos(repos)}
	enabled, _ := cmd.Flags().GetBool("telemetry")
	if !enabled {
		return opts, func() error { return nil }, nil
	}
	path, _ := cmd.Flags().GetString("telemetry-file")
	path, err := validatedMCPTelemetryFilePath(cmd, path)
	if err != nil {
		return nil, nil, err
	}
	maxFileBytes, _ := cmd.Flags().GetInt64("telemetry-max-bytes")
	maxBackups, _ := cmd.Flags().GetInt("telemetry-max-backups")
	recorder, err := telemetrypkg.NewJSONLRecorderWithOptions(telemetrypkg.JSONLRecorderOptions{
		Path:         path,
		MaxFileBytes: maxFileBytes,
		MaxBackups:   maxBackups,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize MCP telemetry: %w", err)
	}
	asyncRecorder := telemetrypkg.NewAsyncRecorder(recorder, telemetrypkg.DefaultAsyncQueueSize)
	opts = append(opts, mcp.WithTelemetryRecorder(asyncRecorder))
	return opts, asyncRecorder.Close, nil
}

func closeMcpTelemetry(closeFn func() error) {
	if closeFn == nil {
		return
	}
	if err := closeFn(); err != nil {
		log.Printf("Warning: Failed to close MCP telemetry recorder: %v", err)
	}
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
