package main

import (
	"encoding/json"
	"fmt"

	"github.com/hungpdn/gokg/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = newVersionCommand()

func newVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print gokg version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()
			jsonOutput, _ := cmd.Flags().GetBool("json")
			if jsonOutput {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(info)
			}

			_, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"gokg %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s\n",
				info.Version,
				info.Commit,
				info.Date,
				info.GoVersion,
				info.Platform,
			)
			return err
		},
	}
	cmd.Flags().Bool("json", false, "Print version information as JSON")
	return cmd
}
