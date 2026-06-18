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
		ws, err := workspace.Init(name)
		if err != nil {
			return err
		}
		fmt.Printf("Workspace %q created at %s\n", name, ws.Dir)
		return nil
	},
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add [repo-path]",
	Short: "Add a Go repository to the current workspace",
	Long:  `Add a Go repository directory to the workspace. If no path is given, the current directory is used.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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

		fmt.Printf("Added repo %q (%s) to workspace %q\n", repoID, analysisRoot.Dir, wsName)
		return nil
	},
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := workspace.List()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}
		fmt.Println("Workspaces:")
		for _, name := range names {
			ws, err := workspace.Load(name)
			if err != nil {
				fmt.Printf("  - %s (error: %v)\n", name, err)
				continue
			}
			fmt.Printf("  - %s (%d repos)\n", name, len(ws.Config.Repos))
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
		ws, err := workspace.Load(name)
		if err != nil {
			return err
		}

		fmt.Printf("Workspace: %s\n", ws.Name)
		fmt.Printf("Directory: %s\n", ws.Dir)
		fmt.Printf("Repos (%d):\n", len(ws.Config.Repos))
		for repoID, path := range ws.Config.Repos {
			fmt.Printf("  - %s → %s\n", repoID, path)
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

		ws, err := workspace.Load(wsName)
		if err != nil {
			return err
		}

		if err := ws.RemoveRepo(repoID); err != nil {
			return err
		}

		fmt.Printf("Removed repo %q from workspace %q\n", repoID, wsName)
		return nil
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
