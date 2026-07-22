package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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
		Args:  cobra.NoArgs,
		RunE:  runTelemetryStats,
	}
	cmd.Flags().String("file", telemetrypkg.DefaultFile, "Path to MCP telemetry JSONL file; an explicitly configured missing file is an error")
	cmd.Flags().Bool("json", false, "Print machine-readable JSON")
	cmd.Flags().Bool("strict", false, "Exit non-zero when telemetry reports delivery failures or data-quality diagnostics")
	return cmd
}

func runTelemetryStats(cmd *cobra.Command, args []string) error {
	path, _ := cmd.Flags().GetString("file")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	strict, _ := cmd.Flags().GetBool("strict")
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("telemetry file path must not be empty")
	}

	report, err := telemetrypkg.BuildReportFromJSONL(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read telemetry: %w", err)
	}
	if os.IsNotExist(err) {
		if cmd.Flags().Changed("file") {
			return fmt.Errorf("telemetry file %q does not exist", path)
		}
		report = telemetrypkg.BuildReport(nil, path)
	}

	out := cmd.OutOrStdout()
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if err := printTelemetryStatsReport(out, report); err != nil {
		return err
	}
	if strict && telemetryReportHasDataQualityIssues(report) {
		return fmt.Errorf(
			"telemetry strict mode failed: delivery_failures=%d invalid_lines=%d truncated_lines=%d truncated_labels=%d redacted_identity_fields=%d legacy_events=%d unsupported_versions=%d group_limit_overflows=%d overflowed_values=%d",
			report.DeliveryFailures,
			report.Diagnostics.InvalidLines,
			report.Diagnostics.TruncatedLines,
			report.Diagnostics.TruncatedLabels,
			report.Diagnostics.RedactedIdentityFields,
			report.Diagnostics.LegacyEvents,
			report.Diagnostics.UnsupportedVersions,
			report.Diagnostics.GroupLimitOverflows,
			report.Diagnostics.OverflowedValues,
		)
	}
	return nil
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
		formatTelemetryBytes(report.RequestBytes),
		report.RequestBytes,
		formatTelemetryBytes(report.ResponseBytes),
		report.ResponseBytes,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Latency (us, approximate p50/p95; max relative error <= %.2f%%): p50 %d, p95 %d, max %d\n",
		report.LatencyPercentileMaxRelativeError*100,
		report.LatencyUS.P50,
		report.LatencyUS.P95,
		report.LatencyUS.Max,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Delivery Failures: %d\n", report.DeliveryFailures); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Diagnostics: invalid_lines=%d truncated_lines=%d truncated_labels=%d redacted_identity_fields=%d legacy_events=%d unsupported_versions=%d group_limit_overflows=%d overflowed_values=%d\n\n",
		report.Diagnostics.InvalidLines,
		report.Diagnostics.TruncatedLines,
		report.Diagnostics.TruncatedLabels,
		report.Diagnostics.RedactedIdentityFields,
		report.Diagnostics.LegacyEvents,
		report.Diagnostics.UnsupportedVersions,
		report.Diagnostics.GroupLimitOverflows,
		report.Diagnostics.OverflowedValues,
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
		name := group.Name
		if group.Overflow {
			name += " (overflow)"
		}
		if _, err := fmt.Fprintf(out, "  %-28s calls=%d failed=%d delivery_failed=%d error_rate=%.1f%% p50_us=%d p95_us=%d tokens_in=%d tokens_out=%d\n",
			name,
			group.Calls,
			group.FailedCalls,
			group.DeliveryFailures,
			group.ErrorRate*100,
			group.LatencyUS.P50,
			group.LatencyUS.P95,
			group.EstimatedInputTokens,
			group.EstimatedOutputTokens,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out)
	return err
}

func formatTelemetryBytes(bytes uint64) string {
	const unit = uint64(1024)
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := unit, 0
	for n := bytes / unit; n >= unit && exp < len("KMGTPE")-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func telemetryReportHasDataQualityIssues(report telemetrypkg.Report) bool {
	return report.DeliveryFailures > 0 ||
		report.Diagnostics.InvalidLines > 0 ||
		report.Diagnostics.TruncatedLines > 0 ||
		report.Diagnostics.TruncatedLabels > 0 ||
		report.Diagnostics.RedactedIdentityFields > 0 ||
		report.Diagnostics.LegacyEvents > 0 ||
		report.Diagnostics.UnsupportedVersions > 0 ||
		report.Diagnostics.GroupLimitOverflows > 0 ||
		report.Diagnostics.OverflowedValues > 0
}
