package impact

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hungpdn/gokg/internal/gitstate"
	"github.com/hungpdn/gokg/internal/graph"
)

// FreshnessStatus represents the staleness status of the knowledge graph
// relative to the current Git working state.
type FreshnessStatus string

const (
	// FreshnessFresh means the graph matches the current Git state.
	FreshnessFresh FreshnessStatus = "fresh"
	// FreshnessStale means the graph is known to be out-of-date.
	FreshnessStale FreshnessStatus = "stale"
	// FreshnessUnknown means freshness could not be determined.
	FreshnessUnknown FreshnessStatus = "unknown"
)

// FreshnessReport holds the diagnostics result for a single repository.
// Reasons are prefixed with "[stale]" or "[unknown]" to aid debugging.
type FreshnessReport struct {
	RepoID               string          `json:"repo_id,omitempty"`
	RepoRoot             string          `json:"repo_root,omitempty"`
	Status               FreshnessStatus `json:"status"`
	Reasons              []string        `json:"reasons,omitempty"`
	AnalyzedAt           string          `json:"analyzed_at,omitempty"`
	AnalyzedHead         string          `json:"analyzed_head,omitempty"`
	CurrentHead          string          `json:"current_head,omitempty"`
	AnalyzedGitRoot      string          `json:"analyzed_git_root,omitempty"`
	CurrentGitRoot       string          `json:"current_git_root,omitempty"`
	AnalyzedBranch       string          `json:"analyzed_branch,omitempty"`
	CurrentBranch        string          `json:"current_branch,omitempty"`
	RepoRootMatch        bool            `json:"repo_root_match"`
	IncludeTests         bool            `json:"include_tests"`
	GitDirtyAtAnalyze    bool            `json:"git_dirty_at_analyze"`
	CurrentGitDirty      bool            `json:"current_git_dirty"`
	GitStatusFingerprint string          `json:"git_status_fingerprint,omitempty"`
	CurrentFingerprint   string          `json:"current_status_fingerprint,omitempty"`
}

// evaluateFreshness evaluates whether the stored analysis graph for a repository
// is still consistent with the current Git working state. It uses the analysis
// metadata stored in repo.AnalysisMetadata (or loaded via repo.AnalysisMetadataLoader)
// alongside a live git state snapshot to determine freshness.
func evaluateFreshness(ctx context.Context, runner CommandRunner, repo Repo, pathCache map[string]string) FreshnessReport {
	report := FreshnessReport{
		RepoID:        repo.ID,
		RepoRoot:      repo.Root,
		Status:        FreshnessUnknown,
		RepoRootMatch: true,
	}

	meta := repo.AnalysisMetadata
	if repo.AnalysisMetadataLoader != nil {
		loaded, err := repo.AnalysisMetadataLoader(ctx)
		if err != nil {
			report.Reasons = append(report.Reasons, "analysis metadata unavailable: "+err.Error())
			return report
		}
		meta = loaded
	}
	if meta == nil {
		report.Reasons = append(report.Reasons, "analysis metadata unavailable; run `gokg analyze --rebuild` to enable freshness diagnostics")
		return report
	}
	applyAnalysisMetadata(&report, meta)

	var unknownReasons []string
	var staleReasons []string
	if meta.SchemaVersion != graph.AnalysisMetadataSchemaVersion {
		unknownReasons = append(unknownReasons, fmt.Sprintf("analysis metadata schema version %d is not supported", meta.SchemaVersion))
	}
	if !meta.GitAvailable {
		if meta.GitError != "" {
			// Do not leak the full error message in the report reasons,
			// as it might contain absolute paths or sensitive stderr output.
			unknownReasons = append(unknownReasons, "git state was unavailable during analyze (error recorded in metadata)")
		} else {
			unknownReasons = append(unknownReasons, "git state was unavailable during analyze")
		}
	}
	if meta.RepoID != "" && repo.ID != "" && meta.RepoID != repo.ID {
		staleReasons = append(staleReasons, fmt.Sprintf("graph repo id %q differs from requested repo id %q", meta.RepoID, repo.ID))
	}
	if meta.RepoRoot != "" && repo.Root != "" && !samePath(meta.RepoRoot, repo.Root, pathCache) {
		report.RepoRootMatch = false
		staleReasons = append(staleReasons, fmt.Sprintf("graph repo root %q differs from current repo root %q", meta.RepoRoot, repo.Root))
	}

	snapshot, err := gitstate.Capture(ctx, repo.Root, runner)
	if err != nil {
		unknownReasons = append(unknownReasons, "current git state unavailable: "+err.Error())
		return finishFreshness(report, staleReasons, unknownReasons)
	}
	report.CurrentHead = snapshot.Head
	report.CurrentGitRoot = snapshot.Root
	report.CurrentBranch = snapshot.Branch
	report.CurrentGitDirty = snapshot.Dirty
	report.CurrentFingerprint = snapshot.StatusFingerprint

	if meta.GitRoot != "" && snapshot.Root != "" && !samePath(meta.GitRoot, snapshot.Root, pathCache) {
		staleReasons = append(staleReasons, fmt.Sprintf("graph git root %q differs from current git root %q", meta.GitRoot, snapshot.Root))
	}
	if meta.GitHead != "" && snapshot.Head != "" && meta.GitHead != snapshot.Head {
		staleReasons = append(staleReasons, fmt.Sprintf("graph HEAD %s differs from current HEAD %s", shortCommit(meta.GitHead), shortCommit(snapshot.Head)))
	}
	if meta.GitAvailable && meta.GitHead == "" {
		unknownReasons = append(unknownReasons, "graph metadata did not record a git HEAD")
	}
	if meta.GitAvailable && meta.GitDirtyAtAnalyze != snapshot.Dirty {
		staleReasons = append(staleReasons, fmt.Sprintf("graph dirty state %t differs from current dirty state %t", meta.GitDirtyAtAnalyze, snapshot.Dirty))
	}
	if meta.GitAvailable && meta.GitStatusFingerprint != snapshot.StatusFingerprint {
		staleReasons = append(staleReasons, "graph working tree status differs from current status")
	}
	if meta.GitAvailable && meta.GitDirtyAtAnalyze && snapshot.Dirty && meta.GitStatusFingerprint == "" {
		unknownReasons = append(unknownReasons, "graph metadata did not record a dirty working tree fingerprint")
	}
	if snapshot.Head == "" {
		unknownReasons = append(unknownReasons, "current git HEAD is empty")
	}

	return finishFreshness(report, staleReasons, unknownReasons)
}

// applyAnalysisMetadata copies the relevant fields from the stored analysis
// metadata into the freshness report for comparison and display.
func applyAnalysisMetadata(report *FreshnessReport, meta *graph.AnalysisMetadata) {
	report.AnalyzedAt = meta.AnalyzedAt.Format("2006-01-02T15:04:05Z07:00")
	report.AnalyzedHead = meta.GitHead
	report.AnalyzedGitRoot = meta.GitRoot
	report.AnalyzedBranch = meta.GitBranch
	report.IncludeTests = meta.IncludeTests
	report.GitDirtyAtAnalyze = meta.GitDirtyAtAnalyze
	report.GitStatusFingerprint = meta.GitStatusFingerprint
}

// applyChangedFileFreshness marks the freshness report stale for every changed
// _test.go file when the graph was analyzed without --include-tests. All
// affected test files are reported so the caller sees the full picture.
func applyChangedFileFreshness(report *FreshnessReport, files []ChangedFile) {
	if report == nil || report.IncludeTests {
		return
	}
	for _, file := range files {
		if strings.HasSuffix(file.Path, "_test.go") {
			markFreshnessStale(report, fmt.Sprintf("changed test file %q is not represented because graph was analyzed with --tests=false", file.Path))
			// Do NOT return early — continue so every changed test file is
			// recorded, giving the user a complete list to act on.
		}
	}
}

// finishFreshness determines the final status from accumulated staleReasons and
// unknownReasons and appends them to the report with "[stale]" / "[unknown]"
// prefixes so callers can distinguish the two classes without re-parsing.
// Priority: stale > unknown > fresh.
func finishFreshness(report FreshnessReport, staleReasons []string, unknownReasons []string) FreshnessReport {
	switch {
	case len(staleReasons) > 0:
		report.Status = FreshnessStale
		for _, r := range staleReasons {
			report.Reasons = append(report.Reasons, "[stale] "+r)
		}
		for _, r := range unknownReasons {
			report.Reasons = append(report.Reasons, "[unknown] "+r)
		}
	case len(unknownReasons) > 0:
		report.Status = FreshnessUnknown
		for _, r := range unknownReasons {
			report.Reasons = append(report.Reasons, "[unknown] "+r)
		}
	default:
		report.Status = FreshnessFresh
	}
	return report
}

// markFreshnessStale appends a stale reason and ensures the report status is
// set to FreshnessStale. Reasons are stored as-is (no prefix) because
// markFreshnessStale is called directly for post-analysis findings such as
// changed test files, which are always stale by definition.
func markFreshnessStale(report *FreshnessReport, reason string) {
	report.Status = FreshnessStale
	report.Reasons = append(report.Reasons, "[stale] "+reason)
}

// samePath reports whether two paths refer to the same filesystem location
// after resolving symlinks and normalising separators. pathCache is shared
// with the broader impact analysis to avoid redundant EvalSymlinks calls.
func samePath(a, b string, pathCache map[string]string) bool {
	return comparableFreshnessPath(a, pathCache) == comparableFreshnessPath(b, pathCache)
}

// comparableFreshnessPath returns a canonical, absolute path string suitable
// for exact equality comparison. It resolves symlinks where possible and falls
// back gracefully when the path does not yet exist on disk. Results are
// memoised in pathCache to avoid repeated disk I/O for the same path.
func comparableFreshnessPath(path string, pathCache map[string]string) string {
	if path == "" {
		return ""
	}
	if cached, ok := pathCache[path]; ok {
		return cached
	}
	cleaned := filepath.Clean(path)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = filepath.Clean(abs)
	}
	var result string
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		result = filepath.Clean(resolved)
	} else {
		parent := filepath.Dir(cleaned)
		if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
			result = filepath.Join(resolvedParent, filepath.Base(cleaned))
		} else {
			result = cleaned
		}
	}
	pathCache[path] = result
	return result
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func (r *Report) GraphFreshnessStatus() FreshnessStatus {
	if r == nil || len(r.Repos) == 0 {
		return FreshnessUnknown
	}
	sawFreshness := false
	sawUnknown := false
	for _, repo := range r.Repos {
		if repo.Freshness == nil {
			sawUnknown = true
			continue
		}
		sawFreshness = true
		switch repo.Freshness.Status {
		case FreshnessStale:
			return FreshnessStale
		case FreshnessUnknown:
			sawUnknown = true
		}
	}
	if sawUnknown || !sawFreshness {
		return FreshnessUnknown
	}
	return FreshnessFresh
}

func (r *Report) HasStaleFreshness() bool {
	return r.GraphFreshnessStatus() == FreshnessStale
}

func (r *Report) HasNonFreshFreshness() bool {
	return r.GraphFreshnessStatus() != FreshnessFresh
}
