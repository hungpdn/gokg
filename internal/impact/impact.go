package impact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
)

const (
	DefaultBaseRef  = "HEAD"
	DefaultMaxDepth = 1
	DefaultMaxNodes = 100
	MaxDepthLimit   = 5
	MaxNodesLimit   = 500
)

var errAllReposFailed = errors.New("impact analysis failed for all repositories")

type Options struct {
	BaseRef          string
	MaxDepth         int
	MaxNodes         int
	IncludeUntracked bool
}

type Repo struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

type RepoReport struct {
	ID      string `json:"id"`
	Root    string `json:"root"`
	Scanned bool   `json:"scanned"`
	Error   string `json:"error,omitempty"`
}

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type ChangedFile struct {
	RepoID       string      `json:"repo_id,omitempty"`
	Path         string      `json:"path"`
	AbsolutePath string      `json:"absolute_path"`
	Status       string      `json:"status"`
	Ranges       []LineRange `json:"ranges,omitempty"`
	WholeFile    bool        `json:"whole_file,omitempty"`
	Untracked    bool        `json:"untracked,omitempty"`
}

type NodeSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	PkgPath   string `json:"pkg_path,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	RepoID    string `json:"repo_id,omitempty"`
}

type ImpactNode struct {
	NodeSummary
	Distance int `json:"distance"`
}

type Report struct {
	BaseRef       string        `json:"base_ref"`
	Repos         []RepoReport  `json:"repos"`
	ChangedFiles  []ChangedFile `json:"changed_files"`
	ChangedNodes  []NodeSummary `json:"changed_nodes"`
	ImpactedNodes []ImpactNode  `json:"impacted_nodes"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error)
}

type CommandRunnerFunc func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

func (f CommandRunnerFunc) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	return f(ctx, dir, name, args...)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, message)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

func Analyze(ctx context.Context, g *graph.Graph, repos []Repo, opts Options) (*Report, error) {
	return AnalyzeWithRunner(ctx, g, repos, opts, execRunner{})
}

func AnalyzeWithRunner(ctx context.Context, g *graph.Graph, repos []Repo, opts Options, runner CommandRunner) (*Report, error) {
	opts = NormalizeOptions(opts)
	if err := ValidateOptions(opts); err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("graph is required")
	}
	if runner == nil {
		runner = execRunner{}
	}

	report := &Report{BaseRef: opts.BaseRef}
	repos = normalizeRepos(repos)
	if len(repos) == 0 {
		return nil, fmt.Errorf("at least one repository root is required")
	}

	successes := 0
	for _, repo := range repos {
		repoReport := RepoReport{ID: repo.ID, Root: repo.Root}
		files, err := changedFilesForRepo(ctx, runner, repo, opts)
		if err != nil {
			repoReport.Error = err.Error()
			report.Warnings = append(report.Warnings, fmt.Sprintf("repo %q: %v", repo.ID, err))
			report.Repos = append(report.Repos, repoReport)
			continue
		}
		repoReport.Scanned = true
		report.Repos = append(report.Repos, repoReport)
		report.ChangedFiles = append(report.ChangedFiles, files...)
		successes++
	}

	if successes == 0 {
		return nil, fmt.Errorf("%w: %s", errAllReposFailed, strings.Join(report.Warnings, "; "))
	}
	sortChangedFiles(report.ChangedFiles)

	fileRanges := make([]graph.FileRange, 0)
	for _, changed := range report.ChangedFiles {
		fileRanges = append(fileRanges, graphRangesForChangedFile(changed)...)
	}

	changedNodes, err := g.Query().NodesForFileRanges(fileRanges)
	if err != nil {
		return nil, err
	}
	report.ChangedNodes = summarizeNodes(changedNodes)
	report.addUnmatchedFileWarnings(g)

	changedNodeIDs := make([]string, 0, len(changedNodes))
	for _, node := range changedNodes {
		changedNodeIDs = append(changedNodeIDs, node.ID)
	}
	impacted, truncated, err := g.Query().GetBlastRadiusDepth(changedNodeIDs, opts.MaxDepth, opts.MaxNodes)
	if err != nil {
		return nil, err
	}
	report.ImpactedNodes = summarizeImpactNodes(impacted)
	if truncated {
		report.Warnings = append(report.Warnings, fmt.Sprintf("impacted nodes truncated at max_nodes=%d", opts.MaxNodes))
	}
	sort.Strings(report.Warnings)
	return report, nil
}

func NormalizeOptions(opts Options) Options {
	opts.BaseRef = strings.TrimSpace(opts.BaseRef)
	if opts.BaseRef == "" {
		opts.BaseRef = DefaultBaseRef
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxNodes == 0 {
		opts.MaxNodes = DefaultMaxNodes
	}
	return opts
}

func ValidateOptions(opts Options) error {
	if err := validateBaseRef(opts.BaseRef); err != nil {
		return err
	}
	if opts.MaxDepth < 1 || opts.MaxDepth > MaxDepthLimit {
		return fmt.Errorf("max depth must be between 1 and %d", MaxDepthLimit)
	}
	if opts.MaxNodes < 1 || opts.MaxNodes > MaxNodesLimit {
		return fmt.Errorf("max nodes must be between 1 and %d", MaxNodesLimit)
	}
	return nil
}

func validateBaseRef(baseRef string) error {
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return fmt.Errorf("base ref is required")
	}
	if strings.HasPrefix(baseRef, "-") {
		return fmt.Errorf("base ref must not start with '-'")
	}
	for _, r := range baseRef {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("base ref must not contain control characters")
		}
	}
	return nil
}

func normalizeRepos(repos []Repo) []Repo {
	normalized := make([]Repo, 0, len(repos))
	for _, repo := range repos {
		repo.ID = strings.TrimSpace(repo.ID)
		repo.Root = strings.TrimSpace(repo.Root)
		if repo.Root == "" {
			continue
		}
		if repo.ID == "" {
			repo.ID = repo.Root
		}
		if abs, err := filepath.Abs(repo.Root); err == nil {
			repo.Root = filepath.Clean(abs)
		} else {
			repo.Root = filepath.Clean(repo.Root)
		}
		normalized = append(normalized, repo)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].ID < normalized[j].ID
	})
	return normalized
}

func changedFilesForRepo(ctx context.Context, runner CommandRunner, repo Repo, opts Options) ([]ChangedFile, error) {
	diffArgs := []string{"diff", "--unified=0", "--no-ext-diff", "--no-color", opts.BaseRef}
	diffOutput, err := runner.Run(ctx, repo.Root, "git", diffArgs...)
	if err != nil {
		return nil, err
	}

	files, err := ParseDiff(repo, string(diffOutput))
	if err != nil {
		return nil, err
	}
	if opts.IncludeUntracked {
		untrackedOutput, err := runner.Run(ctx, repo.Root, "git", "ls-files", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		files = append(files, ParseUntracked(repo, string(untrackedOutput))...)
	}
	sortChangedFiles(files)
	return files, nil
}

var diffHeaderRegexp = regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)
var hunkRegexp = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func ParseDiff(repo Repo, diff string) ([]ChangedFile, error) {
	var files []ChangedFile
	var current *ChangedFile

	flush := func() {
		if current == nil {
			return
		}
		if current.Status == "" {
			current.Status = "M"
		}
		current.AbsolutePath = cleanRepoFile(repo.Root, current.Path)
		files = append(files, *current)
		current = nil
	}

	for _, line := range strings.Split(diff, "\n") {
		if matches := diffHeaderRegexp.FindStringSubmatch(line); matches != nil {
			flush()
			path := matches[2]
			if path == "/dev/null" {
				path = matches[1]
			}
			current = &ChangedFile{RepoID: repo.ID, Path: path, Status: "M"}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "new file mode"):
			current.Status = "A"
		case strings.HasPrefix(line, "deleted file mode"):
			current.Status = "D"
			current.WholeFile = true
		case strings.HasPrefix(line, "rename from "):
			current.Status = "R"
		case strings.HasPrefix(line, "rename to "):
			current.Status = "R"
			current.Path = strings.TrimSpace(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "+++ b/"):
			current.Path = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/") && current.Path == "":
			current.Path = strings.TrimPrefix(line, "--- a/")
		case strings.HasPrefix(line, "@@ "):
			r, ok := parseHunkRange(line)
			if ok {
				current.Ranges = append(current.Ranges, r)
			}
		}
	}
	flush()
	return files, nil
}

func parseHunkRange(line string) (LineRange, bool) {
	matches := hunkRegexp.FindStringSubmatch(line)
	if matches == nil {
		return LineRange{}, false
	}
	start, err := strconv.Atoi(matches[1])
	if err != nil {
		return LineRange{}, false
	}
	count := 1
	if matches[2] != "" {
		count, err = strconv.Atoi(matches[2])
		if err != nil {
			return LineRange{}, false
		}
	}
	if count <= 0 {
		return LineRange{Start: start, End: start}, true
	}
	return LineRange{Start: start, End: start + count - 1}, true
}

func ParseUntracked(repo Repo, output string) []ChangedFile {
	var files []ChangedFile
	for _, line := range strings.Split(output, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		files = append(files, ChangedFile{
			RepoID:       repo.ID,
			Path:         filepath.ToSlash(path),
			AbsolutePath: cleanRepoFile(repo.Root, path),
			Status:       "??",
			WholeFile:    true,
			Untracked:    true,
		})
	}
	return files
}

func cleanRepoFile(root string, rel string) string {
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
}

func graphRangesForChangedFile(changed ChangedFile) []graph.FileRange {
	if changed.WholeFile {
		return []graph.FileRange{{
			FilePath:  changed.AbsolutePath,
			WholeFile: changed.WholeFile,
			RepoID:    changed.RepoID,
			StartLine: 1,
			EndLine:   1,
		}}
	}
	if len(changed.Ranges) == 0 {
		return nil
	}
	ranges := make([]graph.FileRange, 0, len(changed.Ranges))
	for _, r := range changed.Ranges {
		ranges = append(ranges, graph.FileRange{
			FilePath:  changed.AbsolutePath,
			StartLine: r.Start,
			EndLine:   r.End,
			RepoID:    changed.RepoID,
		})
	}
	return ranges
}

func (r *Report) addUnmatchedFileWarnings(g *graph.Graph) {
	for _, changed := range r.ChangedFiles {
		nodes, err := g.Query().NodesForFileRanges(graphRangesForChangedFile(changed))
		if err != nil || len(nodes) > 0 {
			continue
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"no graph nodes matched %s; run `gokg analyze --rebuild` if the graph is stale",
			changed.AbsolutePath,
		))
	}
}

func summarizeNodes(nodes []*parser.Node) []NodeSummary {
	summaries := make([]NodeSummary, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		summaries = append(summaries, summarizeNode(node))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].ID < summaries[j].ID
	})
	return summaries
}

func summarizeImpactNodes(nodes []graph.NodeDistance) []ImpactNode {
	summaries := make([]ImpactNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Node == nil {
			continue
		}
		summary := summarizeNode(node.Node)
		summaries = append(summaries, ImpactNode{NodeSummary: summary, Distance: node.Distance})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Distance != summaries[j].Distance {
			return summaries[i].Distance < summaries[j].Distance
		}
		return summaries[i].ID < summaries[j].ID
	})
	return summaries
}

func summarizeNode(node *parser.Node) NodeSummary {
	return NodeSummary{
		ID:        node.ID,
		Name:      node.Name,
		Type:      string(node.Type),
		PkgPath:   node.PkgPath,
		FilePath:  node.FilePath,
		LineStart: node.Lines[0],
		LineEnd:   node.Lines[1],
		RepoID:    node.RepoID,
	}
}

func sortChangedFiles(files []ChangedFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].RepoID != files[j].RepoID {
			return files[i].RepoID < files[j].RepoID
		}
		return files[i].Path < files[j].Path
	})
}
