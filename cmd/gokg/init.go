package main

import (
	"fmt"

	"github.com/hungpdn/gokg/internal/storage"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize gokg in the current directory",
	Long:  `Initialize the local gokg database (.gokg/) and prepare the workspace for analysis.`,
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		out := cmd.OutOrStdout()
		if _, err := fmt.Fprintln(out, "Initializing gokg workspace..."); err != nil {
			return err
		}

		store, err := storage.NewBadgerStorage(defaultDBPath)
		if err != nil {
			return fmt.Errorf("failed to initialize local storage: %w", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}()

		_, err = fmt.Fprintf(out, "gokg initialized successfully in %s\n", defaultDBPath)
		return err
	},
}
