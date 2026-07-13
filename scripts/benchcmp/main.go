// Command benchcmp summarizes two Go benchmark result files using medians.
// It intentionally uses only the standard library so local comparisons do not
// require downloading an additional benchmarking tool.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var cpuSuffixPattern = regexp.MustCompile(`-[0-9]+$`)

var metricOrder = []string{"ns/op", "B/op", "allocs/op", "MB/s"}

type benchmarkSamples map[string]map[string][]float64

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: benchcmp <baseline.txt> <current.txt>")
		os.Exit(2)
	}
	if err := compareFiles(os.Stdout, os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintf(os.Stderr, "benchcmp: %v\n", err)
		os.Exit(1)
	}
}

func compareFiles(w io.Writer, baselinePath, currentPath string) error {
	for _, path := range []string{baselinePath, currentPath} {
		if err := ensureUnlocked(path); err != nil {
			return err
		}
	}
	baseline, err := parseBenchmarkFile(baselinePath)
	if err != nil {
		return fmt.Errorf("parse baseline: %w", err)
	}
	current, err := parseBenchmarkFile(currentPath)
	if err != nil {
		return fmt.Errorf("parse current: %w", err)
	}

	names := commonBenchmarkNames(baseline, current)
	if len(names) == 0 {
		if hasCPUOnlyNameMismatch(baseline, current) {
			return errors.New("no exact benchmark names found: Go CPU suffixes differ; rerun both files with the same BENCH_CPU")
		}
		return errors.New("no common benchmark names found")
	}

	warnings := metadataWarnings(baselinePath, currentPath)
	for _, warning := range warnings {
		fmt.Fprintf(w, "> Warning: %s\n", warning)
	}
	if len(warnings) > 0 {
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "| Benchmark | Metric | Baseline (median) | Current (median) | Delta | Relative | Samples |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|")
	rows := 0
	for _, name := range names {
		for _, metric := range metricOrder {
			baselineValues := baseline[name][metric]
			currentValues := current[name][metric]
			if len(baselineValues) == 0 || len(currentValues) == 0 {
				continue
			}
			baselineMedian := median(baselineValues)
			currentMedian := median(currentValues)
			fmt.Fprintf(
				w,
				"| %s | %s | %s | %s | %s | %s | %d / %d |\n",
				name,
				metric,
				formatMetric(metric, baselineMedian),
				formatMetric(metric, currentMedian),
				formatDelta(baselineMedian, currentMedian),
				formatRelative(metric, baselineMedian, currentMedian),
				len(baselineValues),
				len(currentValues),
			)
			rows++
		}
	}
	if rows == 0 {
		return errors.New("common benchmarks do not contain comparable metrics")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Delta = (current - baseline) / baseline. Negative is better for ns/op, B/op, and allocs/op.")
	fmt.Fprintln(w, "Medians summarize repeated samples; use benchstat when you need confidence intervals and significance tests.")
	return nil
}

func ensureUnlocked(path string) error {
	_, err := os.Stat(path + ".lock")
	if err == nil {
		return fmt.Errorf("benchmark result is locked by a running or interrupted capture: %s.lock", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect benchmark lock: %w", err)
	}
	return nil
}

func parseBenchmarkFile(path string) (benchmarkSamples, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result, passed, err := parseBenchmarks(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !passed {
		return nil, fmt.Errorf("%s: benchmark run did not finish with PASS", path)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s: no benchmark samples found", path)
	}
	return result, nil
}

func parseBenchmarks(r io.Reader) (benchmarkSamples, bool, error) {
	result := make(benchmarkSamples)
	passed := false
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "PASS" {
			passed = true
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		if _, err := strconv.ParseUint(fields[1], 10, 64); err != nil {
			continue
		}

		name := fields[0]
		for index := 2; index+1 < len(fields); index += 2 {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil {
				continue
			}
			metric := fields[index+1]
			if !knownMetric(metric) {
				continue
			}
			if result[name] == nil {
				result[name] = make(map[string][]float64)
			}
			result[name][metric] = append(result[name][metric], value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return result, passed, nil
}

func normalizeBenchmarkName(name string) string {
	return cpuSuffixPattern.ReplaceAllString(name, "")
}

func knownMetric(metric string) bool {
	for _, candidate := range metricOrder {
		if metric == candidate {
			return true
		}
	}
	return false
}

func commonBenchmarkNames(baseline, current benchmarkSamples) []string {
	names := make([]string, 0, min(len(baseline), len(current)))
	for name := range baseline {
		if _, ok := current[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func hasCPUOnlyNameMismatch(baseline, current benchmarkSamples) bool {
	baselineNames := make(map[string]struct{}, len(baseline))
	for name := range baseline {
		baselineNames[normalizeBenchmarkName(name)] = struct{}{}
	}
	for name := range current {
		if _, ok := baselineNames[normalizeBenchmarkName(name)]; ok {
			return true
		}
	}
	return false
}

func metadataWarnings(baselinePath, currentPath string) []string {
	baseline, baselineFound, baselineErr := readMetadata(baselinePath + ".meta")
	current, currentFound, currentErr := readMetadata(currentPath + ".meta")
	warnings := make([]string, 0, 4)
	if baselineErr != nil {
		warnings = append(warnings, "cannot read baseline metadata: "+baselineErr.Error())
	}
	if currentErr != nil {
		warnings = append(warnings, "cannot read current metadata: "+currentErr.Error())
	}
	if baselineErr != nil || currentErr != nil {
		return warnings
	}
	if !baselineFound || !currentFound {
		warnings = append(warnings, "one or both .meta sidecars are missing; environment compatibility was not verified")
		return warnings
	}

	for _, key := range []string{
		"go_version", "goos", "goarch", "cpu_model", "uname",
		"benchmark_harness_version", "benchmark_harness_hash",
		"bench_count", "bench_time", "bench_cpu", "bench_regex",
	} {
		if baseline[key] == "" || current[key] == "" {
			warnings = append(warnings, fmt.Sprintf("metadata %s is missing from one or both runs; compatibility was not verified", key))
			continue
		}
		if baseline[key] != current[key] {
			warnings = append(warnings, fmt.Sprintf("metadata %s differs: baseline=%q current=%q", key, baseline[key], current[key]))
		}
	}
	if baseline["git_dirty"] == "true" {
		warnings = append(warnings, "baseline was captured from a dirty worktree")
	}
	if current["git_dirty"] == "true" {
		warnings = append(warnings, "current result was captured from a dirty worktree")
	}
	for _, run := range []struct {
		name     string
		metadata map[string]string
	}{
		{name: "baseline", metadata: baseline},
		{name: "current", metadata: current},
	} {
		if value := run.metadata["memprofile"]; value != "" && value != "disabled" {
			warnings = append(warnings, run.name+" was captured with a heap profile, which perturbs benchmark measurements")
		}
		if value := run.metadata["cpuprofile"]; value != "" && value != "disabled" {
			warnings = append(warnings, run.name+" was captured with a CPU profile, which perturbs benchmark measurements")
		}
	}
	return warnings
}

func readMetadata(path string) (map[string]string, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	metadata := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			metadata[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return metadata, true, nil
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func formatMetric(metric string, value float64) string {
	switch metric {
	case "ns/op":
		switch {
		case value >= 1_000_000:
			return fmt.Sprintf("%.3f ms/op", value/1_000_000)
		case value >= 1_000:
			return fmt.Sprintf("%.3f us/op", value/1_000)
		default:
			return fmt.Sprintf("%.3f ns/op", value)
		}
	case "B/op":
		switch {
		case value >= 1_000_000:
			return fmt.Sprintf("%.3f MB/op", value/1_000_000)
		case value >= 1_000:
			return fmt.Sprintf("%.3f kB/op", value/1_000)
		default:
			return fmt.Sprintf("%.0f B/op", value)
		}
	case "allocs/op":
		if value == float64(int64(value)) {
			return fmt.Sprintf("%.0f allocs/op", value)
		}
		return fmt.Sprintf("%.1f allocs/op", value)
	case "MB/s":
		return fmt.Sprintf("%.3f MB/s", value)
	default:
		return strconv.FormatFloat(value, 'g', 6, 64) + " " + metric
	}
}

func formatDelta(baseline, current float64) string {
	if baseline == 0 {
		if current == 0 {
			return "0.00%"
		}
		return "n/a"
	}
	return fmt.Sprintf("%+.2f%%", (current-baseline)/baseline*100)
}

func formatRelative(metric string, baseline, current float64) string {
	if baseline == current {
		return "unchanged"
	}
	lowerIsBetter := metric != "MB/s"
	if lowerIsBetter {
		if current == 0 {
			return "reduced to zero"
		}
		if current < baseline {
			return fmt.Sprintf("%.2fx better", baseline/current)
		}
		if baseline == 0 {
			return "regression from zero"
		}
		return fmt.Sprintf("%.2fx worse", current/baseline)
	}
	if baseline == 0 {
		return "improved from zero"
	}
	if current > baseline {
		return fmt.Sprintf("%.2fx better", current/baseline)
	}
	if current == 0 {
		return "regression to zero"
	}
	return fmt.Sprintf("%.2fx worse", baseline/current)
}
