package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	telemetrypkg "github.com/hungpdn/gokg/internal/telemetry"
	"github.com/spf13/cobra"
)

var telemetryCmd = newTelemetryCommand()

func newTelemetryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Inspect local MCP telemetry",
	}
	cmd.AddCommand(newTelemetryStatsCommand())
	return cmd
}

func newTelemetryStatsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Summarize MCP tool-call telemetry",
		RunE:  runTelemetryStats,
	}
	cmd.Flags().String("file", telemetrypkg.DefaultFile, "Path to MCP telemetry JSONL file")
	cmd.Flags().Bool("json", false, "Print machine-readable JSON")
	return cmd
}

func runTelemetryStats(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("file")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	events, err := telemetrypkg.ReadJSONL(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read telemetry: %w", err)
	}
	report := telemetrypkg.BuildReport(events, path)

	out := cmd.OutOrStdout()
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return printTelemetryStatsReport(out, report)
}

func printTelemetryStatsReport(out io.Writer, report telemetrypkg.Report) error {
	if _, err := fmt.Fprintln(out, "GoKG MCP Telemetry"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Source: %s\n", report.Source); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Calls: %d successful, %d failed, %d total (error rate %.1f%%)\n",
		report.SuccessfulCalls,
		report.FailedCalls,
		report.TotalCalls,
		report.ErrorRate*100,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Estimated Tokens: input %d, output %d\n",
		report.EstimatedInputTokens,
		report.EstimatedOutputTokens,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Payload Bytes: request %s (%d bytes), response %s (%d bytes)\n",
		formatBytes(int64(report.RequestBytes)),
		report.RequestBytes,
		formatBytes(int64(report.ResponseBytes)),
		report.ResponseBytes,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Latency: p50 %s, p95 %s, max %s\n\n",
		formatDurationMS(report.LatencyMS.P50),
		formatDurationMS(report.LatencyMS.P95),
		formatDurationMS(report.LatencyMS.Max),
	); err != nil {
		return err
	}

	if report.TotalCalls == 0 {
		_, err := fmt.Fprintln(out, "No telemetry events found.")
		return err
	}
	if err := printTelemetryGroup(out, "Tools", report.Tools); err != nil {
		return err
	}
	if err := printTelemetryGroup(out, "Agents", report.Clients); err != nil {
		return err
	}
	if err := printTelemetryGroup(out, "Sessions", report.Sessions); err != nil {
		return err
	}
	return printTelemetryGroup(out, "Transports", report.Transports)
}

func printTelemetryGroup(out io.Writer, title string, groups []telemetrypkg.GroupStats) error {
	if len(groups) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "%s:\n", title); err != nil {
		return err
	}
	for _, group := range groups {
		if _, err := fmt.Fprintf(out, "  %-28s calls=%d failed=%d error_rate=%.1f%% p50=%s p95=%s tokens_in=%d tokens_out=%d\n",
			group.Name,
			group.Calls,
			group.FailedCalls,
			group.ErrorRate*100,
			formatDurationMS(group.LatencyMS.P50),
			formatDurationMS(group.LatencyMS.P95),
			group.EstimatedInputTokens,
			group.EstimatedOutputTokens,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out)
	return err
}

func formatDurationMS(ms int64) string {
	return fmt.Sprintf("%dms", ms)
}
