package impact

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hungpdn/gokg/internal/gitstate"
	"github.com/hungpdn/gokg/internal/graph"
)

type FreshnessStatus string

const (
	FreshnessFresh   FreshnessStatus = "fresh"
	FreshnessStale   FreshnessStatus = "stale"
	FreshnessUnknown FreshnessStatus = "unknown"
)

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
	GitStatusFingerprint string          `json:"git_status_fingerprint,omitempty"`
}

func evaluateFreshness(ctx context.Context, runner CommandRunner, repo Repo) FreshnessReport {
	report := FreshnessReport{
		RepoID:        repo.ID,
		RepoRoot:      repo.Root,
		Status:        FreshnessUnknown,
		RepoRootMatch: true,
	}

	meta := repo.AnalysisMetadata
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
	if meta.GitDirtyAtAnalyze {
		unknownReasons = append(unknownReasons, "graph was analyzed while the working tree was dirty")
	}
	if !meta.GitAvailable {
		if meta.GitError != "" {
			unknownReasons = append(unknownReasons, "git state was unavailable during analyze: "+meta.GitError)
		} else {
			unknownReasons = append(unknownReasons, "git state was unavailable during analyze")
		}
	}
	if meta.RepoID != "" && repo.ID != "" && meta.RepoID != repo.ID {
		staleReasons = append(staleReasons, fmt.Sprintf("graph repo id %q differs from requested repo id %q", meta.RepoID, repo.ID))
	}
	if meta.RepoRoot != "" && repo.Root != "" && !samePath(meta.RepoRoot, repo.Root) {
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

	if meta.GitRoot != "" && snapshot.Root != "" && !samePath(meta.GitRoot, snapshot.Root) {
		staleReasons = append(staleReasons, fmt.Sprintf("graph git root %q differs from current git root %q", meta.GitRoot, snapshot.Root))
	}
	if meta.GitHead != "" && snapshot.Head != "" && meta.GitHead != snapshot.Head {
		staleReasons = append(staleReasons, fmt.Sprintf("graph HEAD %s differs from current HEAD %s", shortCommit(meta.GitHead), shortCommit(snapshot.Head)))
	}
	if meta.GitAvailable && meta.GitHead == "" {
		unknownReasons = append(unknownReasons, "graph metadata did not record a git HEAD")
	}
	if snapshot.Head == "" {
		unknownReasons = append(unknownReasons, "current git HEAD is empty")
	}

	return finishFreshness(report, staleReasons, unknownReasons)
}

func applyAnalysisMetadata(report *FreshnessReport, meta *graph.AnalysisMetadata) {
	report.AnalyzedAt = meta.AnalyzedAt.Format("2006-01-02T15:04:05Z07:00")
	report.AnalyzedHead = meta.GitHead
	report.AnalyzedGitRoot = meta.GitRoot
	report.AnalyzedBranch = meta.GitBranch
	report.IncludeTests = meta.IncludeTests
	report.GitDirtyAtAnalyze = meta.GitDirtyAtAnalyze
	report.GitStatusFingerprint = meta.GitStatusFingerprint
}

func applyChangedFileFreshness(report *FreshnessReport, files []ChangedFile) {
	if report == nil || report.IncludeTests {
		return
	}
	for _, file := range files {
		if strings.HasSuffix(file.Path, "_test.go") {
			markFreshnessStale(report, fmt.Sprintf("changed test file %q is not represented because graph was analyzed with --tests=false", file.Path))
			return
		}
	}
}

func finishFreshness(report FreshnessReport, staleReasons []string, unknownReasons []string) FreshnessReport {
	switch {
	case len(staleReasons) > 0:
		report.Status = FreshnessStale
		report.Reasons = append(report.Reasons, staleReasons...)
		report.Reasons = append(report.Reasons, unknownReasons...)
	case len(unknownReasons) > 0:
		report.Status = FreshnessUnknown
		report.Reasons = append(report.Reasons, unknownReasons...)
	default:
		report.Status = FreshnessFresh
	}
	return report
}

func markFreshnessStale(report *FreshnessReport, reason string) {
	report.Status = FreshnessStale
	report.Reasons = append(report.Reasons, reason)
}

func samePath(a string, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
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
