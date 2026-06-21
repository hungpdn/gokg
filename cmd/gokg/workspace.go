package main

import (
	"fmt"
	"path/filepath"

	"github.com/hungpdn/gokg/internal/workspace"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage multi-repo workspaces",
	Long:  `Create, list, and manage workspaces that aggregate multiple Go repositories into a unified knowledge graph.`,
}

var workspaceInitCmd = &cobra.Command{
	Use:   "init <name>",
	Short: "Create a new workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		out := cmd.OutOrStdout()
		ws, err := workspace.Init(name)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "Workspace %q created at %s\n", name, ws.Dir)
		return err
	},
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add [repo-path]",
	Short: "Add a Go repository to the current workspace",
	Long:  `Add a Go repository directory to the workspace. If no path is given, the current directory is used.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		wsName, _ := cmd.Flags().GetString("workspace")
		if wsName == "" {
			return fmt.Errorf("--workspace is required")
		}

		ws, err := workspace.Load(wsName)
		if err != nil {
			return err
		}

		repoPath := "."
		if len(args) > 0 {
			repoPath = args[0]
		}

		absPath, err := filepath.Abs(repoPath)
		if err != nil {
			return fmt.Errorf("failed to resolve path: %w", err)
		}

		analysisRoot, err := resolveGoAnalysisRoot(absPath)
		if err != nil {
			return err
		}

		// Detect module prefix from go.mod.
		repoID := analysisRoot.ModulePrefix
		if repoID == "" {
			repoID = filepath.Base(analysisRoot.Dir)
		}

		if err := ws.AddRepo(repoID, analysisRoot.Dir); err != nil {
			return err
		}

		_, err = fmt.Fprintf(out, "Added repo %q (%s) to workspace %q\n", repoID, analysisRoot.Dir, wsName)
		return err
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		names, err := workspace.List()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			_, err = fmt.Fprintln(out, "No workspaces found.")
			return err
		}
		if _, err := fmt.Fprintln(out, "Workspaces:"); err != nil {
			return err
		}
		for _, name := range names {
			ws, err := workspace.Load(name)
			if err != nil {
				if _, writeErr := fmt.Fprintf(out, "  - %s (error: %v)\n", name, err); writeErr != nil {
					return writeErr
				}
				continue
			}
			if _, err := fmt.Fprintf(out, "  - %s (%d repos)\n", name, len(ws.Config.Repos)); err != nil {
				return err
			}
		}
		return nil
	},
}

var workspaceShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show details of a workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		out := cmd.OutOrStdout()
		ws, err := workspace.Load(name)
		if err != nil {
			return err
		}

		if _, err := fmt.Fprintf(out, "Workspace: %s\n", ws.Name); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Directory: %s\n", ws.Dir); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Repos (%d):\n", len(ws.Config.Repos)); err != nil {
			return err
		}
		for _, repo := range sortedWorkspaceRepos(ws) {
			if _, err := fmt.Fprintf(out, "  - %s -> %s\n", repo.ID, repo.Path); err != nil {
				return err
			}
		}
		return nil
	},
}

var workspaceRemoveCmd = &cobra.Command{
	Use:   "remove <workspace> <repo-id>",
	Short: "Remove a repo from a workspace",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsName := args[0]
		repoID := args[1]
		out := cmd.OutOrStdout()

		ws, err := workspace.Load(wsName)
		if err != nil {
			return err
		}

		if err := ws.RemoveRepo(repoID); err != nil {
			return err
		}

		_, err = fmt.Fprintf(out, "Removed repo %q from workspace %q\n", repoID, wsName)
		return err
	},
}

func init() {
	workspaceAddCmd.Flags().String("workspace", "", "Workspace name to add the repo to")
	workspaceCmd.AddCommand(workspaceInitCmd)
	workspaceCmd.AddCommand(workspaceAddCmd)
	workspaceCmd.AddCommand(workspaceListCmd)
	workspaceCmd.AddCommand(workspaceShowCmd)
	workspaceCmd.AddCommand(workspaceRemoveCmd)
}
