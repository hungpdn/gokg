package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBenchmarks(t *testing.T) {
	input := `goos: darwin
goarch: arm64
BenchmarkReportBuilderHighCardinality-12        100  9900000 ns/op  2289213 B/op  10295 allocs/op
BenchmarkReportBuilderHighCardinality-12        100 10100000 ns/op  2290000 B/op  10297 allocs/op
BenchmarkBuildReportFromReader/high_cardinality-12 50 12000000 ns/op 100.25 MB/s 3000000 B/op 20000 allocs/op
PASS
`

	result, passed, err := parseBenchmarks(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseBenchmarks() error = %v", err)
	}
	if !passed {
		t.Fatal("parseBenchmarks() passed = false, want true")
	}
	values := result["BenchmarkReportBuilderHighCardinality-12"]["ns/op"]
	if len(values) != 2 || values[0] != 9_900_000 || values[1] != 10_100_000 {
		t.Fatalf("unexpected ns/op values: %#v", values)
	}
	throughput := result["BenchmarkBuildReportFromReader/high_cardinality-12"]["MB/s"]
	if len(throughput) != 1 || throughput[0] != 100.25 {
		t.Fatalf("unexpected MB/s values: %#v", throughput)
	}
}

func TestMedianDoesNotMutateInput(t *testing.T) {
	values := []float64{9, 1, 5, 3}
	if got := median(values); got != 4 {
		t.Fatalf("median() = %v, want 4", got)
	}
	if values[0] != 9 || values[1] != 1 {
		t.Fatalf("median mutated input: %#v", values)
	}
}

func TestFormatRelativeReportsZeroThroughputAsRegression(t *testing.T) {
	if got := formatRelative("MB/s", 100, 0); got != "regression to zero" {
		t.Fatalf("formatRelative() = %q, want regression to zero", got)
	}
}

func TestCompareFilesReportsQuotedPerformanceMath(t *testing.T) {
	tempDir := t.TempDir()
	baselinePath := filepath.Join(tempDir, "baseline.txt")
	currentPath := filepath.Join(tempDir, "current.txt")
	baseline := "BenchmarkReportBuilderHighCardinality-12 10 31200000 ns/op 3317700 B/op 102950 allocs/op\nPASS\n"
	current := "BenchmarkReportBuilderHighCardinality-12 30 9900000 ns/op 2289213 B/op 10295 allocs/op\nPASS\n"
	if err := os.WriteFile(baselinePath, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := compareFiles(&output, baselinePath, currentPath); err != nil {
		t.Fatalf("compareFiles() error = %v", err)
	}
	for _, expected := range []string{
		"31.200 ms/op",
		"9.900 ms/op",
		"-68.27%",
		"3.15x better",
		"2.289 MB/op",
		"10295 allocs/op",
		"-90.00%",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestCompareFilesRejectsDisjointBenchmarks(t *testing.T) {
	tempDir := t.TempDir()
	baselinePath := filepath.Join(tempDir, "baseline.txt")
	currentPath := filepath.Join(tempDir, "current.txt")
	if err := os.WriteFile(baselinePath, []byte("BenchmarkBefore-1 1 10 ns/op\nPASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, []byte("BenchmarkAfter-1 1 5 ns/op\nPASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := compareFiles(&bytes.Buffer{}, baselinePath, currentPath); err == nil {
		t.Fatal("compareFiles() error = nil, want disjoint benchmark error")
	}
}

func TestCompareFilesRejectsDifferentCPUSuffixes(t *testing.T) {
	tempDir := t.TempDir()
	baselinePath := filepath.Join(tempDir, "baseline.txt")
	currentPath := filepath.Join(tempDir, "current.txt")
	if err := os.WriteFile(baselinePath, []byte("BenchmarkSame-12 1 10 ns/op\nPASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, []byte("BenchmarkSame-8 1 5 ns/op\nPASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := compareFiles(&bytes.Buffer{}, baselinePath, currentPath)
	if err == nil || !strings.Contains(err.Error(), "CPU suffixes differ") {
		t.Fatalf("compareFiles() error = %v, want CPU suffix mismatch", err)
	}
}

func TestCompareFilesRejectsIncompleteRun(t *testing.T) {
	tempDir := t.TempDir()
	baselinePath := filepath.Join(tempDir, "baseline.txt")
	currentPath := filepath.Join(tempDir, "current.txt")
	if err := os.WriteFile(baselinePath, []byte("BenchmarkSame-1 1 10 ns/op\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, []byte("BenchmarkSame-1 1 5 ns/op\nPASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := compareFiles(&bytes.Buffer{}, baselinePath, currentPath)
	if err == nil || !strings.Contains(err.Error(), "did not finish with PASS") {
		t.Fatalf("compareFiles() error = %v, want incomplete-run error", err)
	}
}

func TestCompareFilesRejectsLockedResult(t *testing.T) {
	tempDir := t.TempDir()
	baselinePath := filepath.Join(tempDir, "baseline.txt")
	currentPath := filepath.Join(tempDir, "current.txt")
	if err := os.Mkdir(baselinePath+".lock", 0o700); err != nil {
		t.Fatal(err)
	}

	err := compareFiles(&bytes.Buffer{}, baselinePath, currentPath)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("compareFiles() error = %v, want locked-result error", err)
	}
}

func TestMetadataWarningsReportIncompatibleRuns(t *testing.T) {
	tempDir := t.TempDir()
	baselinePath := filepath.Join(tempDir, "baseline.txt")
	currentPath := filepath.Join(tempDir, "current.txt")
	baseline := "go_version=go version go1.25\nbenchmark_harness_hash=abc\nbench_cpu=1\ngit_dirty=false\n"
	current := "go_version=go version go1.26\nbenchmark_harness_hash=def\nbench_cpu=2\ngit_dirty=true\n"
	if err := os.WriteFile(baselinePath+".meta", []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath+".meta", []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}

	warnings := strings.Join(metadataWarnings(baselinePath, currentPath), "\n")
	for _, expected := range []string{"go_version differs", "benchmark_harness_hash differs", "bench_cpu differs", "dirty worktree"} {
		if !strings.Contains(warnings, expected) {
			t.Errorf("warnings do not contain %q:\n%s", expected, warnings)
		}
	}
}
