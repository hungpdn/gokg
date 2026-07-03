package impact

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
	"golang.org/x/sync/errgroup"
)

const (
	DefaultBaseRef  = "HEAD"
	DefaultMaxDepth = 1
	DefaultMaxNodes = 100
	DefaultMaxFiles = 1000
	MaxDepthLimit   = 5
	MaxNodesLimit   = 500
	MaxFilesLimit   = 10000

	maxImpactRepoWorkers = 8
)

var errAllReposFailed = errors.New("impact analysis failed for all repositories")

// Options configures the behavior of the impact analysis.
type Options struct {
	BaseRef          string
	MaxDepth         int
	MaxNodes         int
	MaxFiles         int
	IncludeUntracked bool
}

// Repo represents a Git repository within the workspace.
type Repo struct {
	ID               string                  `json:"id"`
	Root             string                  `json:"root"`
	AnalysisMetadata *graph.AnalysisMetadata `json:"-"`
}

// RepoReport summarizes the analysis status of a single repository.
type RepoReport struct {
	ID        string           `json:"id"`
	Root      string           `json:"root"`
	Scanned   bool             `json:"scanned"`
	Error     string           `json:"error,omitempty"`
	Freshness *FreshnessReport `json:"freshness,omitempty"`
}

// LineRange represents a start and end line number in a file.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ChangedFile represents a file that was modified, added, or deleted.
type ChangedFile struct {
	RepoID       string      `json:"repo_id,omitempty"`
	Path         string      `json:"path"`
	AbsolutePath string      `json:"absolute_path"`
	Status       string      `json:"status"`
	Ranges       []LineRange `json:"ranges,omitempty"`
	WholeFile    bool        `json:"whole_file,omitempty"`
	Untracked    bool        `json:"untracked,omitempty"`
}

// NodeSummary provides a lightweight summary of a graph node.
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

// ImpactNode represents a node impacted by a change, including its distance.
type ImpactNode struct {
	NodeSummary
	Distance int `json:"distance"`
}

// Report contains the complete results of an impact analysis.
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

// Analyze performs a change impact analysis using the default execCommand runner.
func Analyze(ctx context.Context, g *graph.Graph, repos []Repo, opts Options) (*Report, error) {
	return AnalyzeWithRunner(ctx, g, repos, opts, execRunner{})
}

// AnalyzeWithRunner performs a change impact analysis using a custom CommandRunner.
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

	eg, ctxGroup := errgroup.WithContext(ctx)
	eg.SetLimit(impactRepoWorkerLimit(len(repos)))

	repoReports := make([]RepoReport, len(repos))
	repoFiles := make([][]ChangedFile, len(repos))
	repoWarnings := make([][]string, len(repos))

	for i, repo := range repos {
		i, repo := i, repo // capture for goroutine
		eg.Go(func() error {
			repoReport := RepoReport{ID: repo.ID, Root: repo.Root}
			freshness := evaluateFreshness(ctxGroup, runner, repo)
			repoReport.Freshness = &freshness
			files, err := changedFilesForRepo(ctxGroup, runner, repo, opts)

			if err != nil {
				repoReport.Error = err.Error()
				repoWarnings[i] = []string{fmt.Sprintf("repo %q: %v", repo.ID, err)}
				repoReports[i] = repoReport
				return nil
			}

			applyChangedFileFreshness(&freshness, files)
			repoReport.Scanned = true
			repoReports[i] = repoReport
			repoFiles[i] = files
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	successes := 0
	for i := range repos {
		report.Repos = append(report.Repos, repoReports[i])
		if repoReports[i].Scanned {
			successes++
			report.ChangedFiles = append(report.ChangedFiles, repoFiles[i]...)
		}
		report.Warnings = append(report.Warnings, repoWarnings[i]...)
	}

	if successes == 0 {
		return nil, fmt.Errorf("%w: %s", errAllReposFailed, strings.Join(report.Warnings, "; "))
	}
	sortChangedFiles(report.ChangedFiles)
	report.truncateChangedFiles(opts.MaxFiles)

	fileRanges := make([]graph.FileRange, 0)
	for _, changed := range report.ChangedFiles {
		fileRanges = append(fileRanges, graphRangesForChangedFile(changed)...)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	changedNodes, err := g.Query().NodesForFileRanges(fileRanges)
	if err != nil {
		return nil, err
	}
	report.ChangedNodes = summarizeNodes(changedNodes)

	pathCache := make(map[string]string)
	report.addUnmatchedFileWarnings(changedNodes, pathCache)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

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
	slices.Sort(report.Warnings)
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
	if opts.MaxFiles == 0 {
		opts.MaxFiles = DefaultMaxFiles
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
	if opts.MaxFiles < 1 || opts.MaxFiles > MaxFilesLimit {
		return fmt.Errorf("max files must be between 1 and %d", MaxFilesLimit)
	}
	return nil
}

var baseRefRegexp = regexp.MustCompile(`^[a-zA-Z0-9_/.^~-]+$`)

func validateBaseRef(baseRef string) error {
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return fmt.Errorf("base ref is required")
	}
	if strings.HasPrefix(baseRef, "-") {
		return fmt.Errorf("base ref must not start with '-'")
	}
	if strings.HasPrefix(baseRef, "^") {
		return fmt.Errorf("base ref must not start with '^'")
	}
	if strings.Contains(baseRef, "..") {
		return fmt.Errorf("base ref must be a single revision, not a range")
	}
	if !baseRefRegexp.MatchString(baseRef) {
		return fmt.Errorf("base ref contains invalid characters")
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
	slices.SortFunc(normalized, func(a, b Repo) int {
		return strings.Compare(a.ID, b.ID)
	})
	return normalized
}

func impactRepoWorkerLimit(repoCount int) int {
	if repoCount < 1 {
		return 1
	}
	if repoCount < maxImpactRepoWorkers {
		return repoCount
	}
	return maxImpactRepoWorkers
}

func changedFilesForRepo(ctx context.Context, runner CommandRunner, repo Repo, opts Options) ([]ChangedFile, error) {
	baseCommit, err := resolveBaseCommit(ctx, runner, repo, opts.BaseRef)
	if err != nil {
		return nil, err
	}

	diffArgs := []string{"diff", "--unified=0", "--no-ext-diff", "--no-color", baseCommit, "--"}
	diffOutput, err := runner.Run(ctx, repo.Root, "git", diffArgs...)
	if err != nil {
		return nil, err
	}

	files, err := ParseDiff(repo, bytes.NewReader(diffOutput))
	if err != nil {
		return nil, err
	}
	if opts.IncludeUntracked {
		untrackedOutput, err := runner.Run(ctx, repo.Root, "git", "ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		untrackedFiles, err := ParseUntracked(repo, bytes.NewReader(untrackedOutput))
		if err != nil {
			return nil, err
		}
		files = append(files, untrackedFiles...)
	}
	sortChangedFiles(files)
	return files, nil
}

func resolveBaseCommit(ctx context.Context, runner CommandRunner, repo Repo, baseRef string) (string, error) {
	out, err := runner.Run(ctx, repo.Root, "git", "rev-parse", "--verify", "--end-of-options", baseRef+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("invalid base ref %q: %w", baseRef, err)
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("invalid base ref %q: resolved empty commit", baseRef)
	}
	return commit, nil
}

// ParseDiff parses unified diff output into a list of ChangedFiles.
func ParseDiff(repo Repo, r io.Reader) ([]ChangedFile, error) {
	var files []ChangedFile
	var current *ChangedFile

	flush := func() {
		if current == nil {
			return
		}
		if current.Status == "" {
			current.Status = "M"
		}
		if len(current.Ranges) == 0 && !current.WholeFile {
			current.WholeFile = true
		}
		current.AbsolutePath = cleanRepoFile(repo.Root, current.Path)
		files = append(files, *current)
		current = nil
	}

	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			parseDiffLine(repo, line, flush, &current)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	flush()
	return files, nil
}

func parseDiffLine(repo Repo, line string, flush func(), current **ChangedFile) {
	if strings.HasPrefix(line, "diff --git a/") {
		flush()
		rest := strings.TrimPrefix(line, "diff --git a/")
		idx := strings.LastIndex(rest, " b/")
		path := ""
		if idx != -1 {
			path = rest[idx+3:]
			if path == "/dev/null" {
				path = rest[:idx]
			}
		}
		*current = &ChangedFile{RepoID: repo.ID, Path: path, Status: "M"}
		return
	}
	if *current == nil {
		return
	}
	switch {
	case strings.HasPrefix(line, "new file mode"):
		(*current).Status = "A"
	case strings.HasPrefix(line, "deleted file mode"):
		(*current).Status = "D"
		(*current).WholeFile = true
	case strings.HasPrefix(line, "rename from "):
		(*current).Status = "R"
	case strings.HasPrefix(line, "rename to "):
		(*current).Status = "R"
		(*current).Path = strings.TrimSpace(strings.TrimPrefix(line, "rename to "))
	case strings.HasPrefix(line, "+++ b/"):
		(*current).Path = strings.TrimPrefix(line, "+++ b/")
	case strings.HasPrefix(line, "--- a/") && (*current).Path == "":
		(*current).Path = strings.TrimPrefix(line, "--- a/")
	case strings.HasPrefix(line, "@@ "):
		rg, ok := parseHunkRange(line)
		if ok {
			(*current).Ranges = append((*current).Ranges, rg)
		}
	}
}

func parseHunkRange(line string) (LineRange, bool) {
	idx := strings.Index(line, " +")
	if idx == -1 {
		return LineRange{}, false
	}
	rest := line[idx+2:]
	endIdx := strings.Index(rest, " @@")
	if endIdx == -1 {
		return LineRange{}, false
	}
	plusPart := rest[:endIdx]

	commaIdx := strings.IndexByte(plusPart, ',')
	startStr := plusPart
	countStr := ""
	if commaIdx != -1 {
		startStr = plusPart[:commaIdx]
		countStr = plusPart[commaIdx+1:]
	}

	start, err := strconv.Atoi(startStr)
	if err != nil {
		return LineRange{}, false
	}

	count := 1
	if countStr != "" {
		count, err = strconv.Atoi(countStr)
		if err != nil {
			return LineRange{}, false
		}
	}

	if count <= 0 {
		return LineRange{Start: start, End: start}, true
	}
	return LineRange{Start: start, End: start + count - 1}, true
}

func ParseUntracked(repo Repo, r io.Reader) ([]ChangedFile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var files []ChangedFile
	parts := bytes.Split(data, []byte{0})
	if !bytes.Contains(data, []byte{0}) {
		parts = bytes.Split(data, []byte{'\n'})
	}
	for _, rawPath := range parts {
		if len(rawPath) == 0 {
			continue
		}
		path := strings.TrimSuffix(string(rawPath), "\r")
		files = append(files, ChangedFile{
			RepoID:       repo.ID,
			Path:         filepath.ToSlash(path),
			AbsolutePath: cleanRepoFile(repo.Root, path),
			Status:       "??",
			WholeFile:    true,
			Untracked:    true,
		})
	}
	return files, nil
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

func (r *Report) addUnmatchedFileWarnings(changedNodes []*parser.Node, pathCache map[string]string) {
	nodesByPath := nodesByComparablePath(changedNodes, pathCache)
	for _, changed := range r.ChangedFiles {
		if changedFileHasMatchingNode(changed, nodesByPath[comparableImpactPath(changed.AbsolutePath, pathCache)], pathCache) {
			continue
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"no graph nodes matched %s; run `gokg analyze --rebuild` if the graph is stale",
			changed.AbsolutePath,
		))
	}
}

func (r *Report) truncateChangedFiles(maxFiles int) {
	if maxFiles <= 0 || len(r.ChangedFiles) <= maxFiles {
		return
	}
	total := len(r.ChangedFiles)
	r.ChangedFiles = append([]ChangedFile(nil), r.ChangedFiles[:maxFiles]...)
	r.Warnings = append(r.Warnings, fmt.Sprintf(
		"changed files truncated at max_files=%d from %d total files; impact only includes listed files",
		maxFiles,
		total,
	))
}

func nodesByComparablePath(nodes []*parser.Node, pathCache map[string]string) map[string][]*parser.Node {
	nodesByPath := make(map[string][]*parser.Node)
	for _, node := range nodes {
		if node == nil || node.FilePath == "" {
			continue
		}
		path := comparableImpactPath(node.FilePath, pathCache)
		nodesByPath[path] = append(nodesByPath[path], node)
	}
	return nodesByPath
}

func changedFileHasMatchingNode(changed ChangedFile, nodes []*parser.Node, pathCache map[string]string) bool {
	for _, node := range nodes {
		if changedFileMatchesNode(changed, node, pathCache) {
			return true
		}
	}
	return false
}

func changedFileMatchesNode(changed ChangedFile, node *parser.Node, pathCache map[string]string) bool {
	if node == nil || node.FilePath == "" {
		return false
	}
	if changed.RepoID != "" && node.RepoID != "" && changed.RepoID != node.RepoID {
		return false
	}
	if comparableImpactPath(changed.AbsolutePath, pathCache) != comparableImpactPath(node.FilePath, pathCache) {
		return false
	}
	if changed.WholeFile {
		return true
	}
	for _, r := range changed.Ranges {
		if lineRangeOverlapsNode(r, node) {
			return true
		}
	}
	return false
}

func lineRangeOverlapsNode(r LineRange, node *parser.Node) bool {
	if node == nil || node.Lines[0] <= 0 || node.Lines[1] <= 0 || node.Lines[1] < node.Lines[0] {
		return false
	}
	start := r.Start
	end := r.End
	if start <= 0 || end <= 0 {
		return false
	}
	if end < start {
		start, end = end, start
	}
	return node.Lines[0] <= end && node.Lines[1] >= start
}

func comparableImpactPath(path string, pathCache map[string]string) string {
	if path == "" {
		return ""
	}
	if cached, ok := pathCache[path]; ok {
		return cached
	}

	resolvedPath := path
	if abs, err := filepath.Abs(resolvedPath); err == nil {
		resolvedPath = abs
	}
	cleaned := filepath.Clean(resolvedPath)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		final := filepath.Clean(resolved)
		pathCache[path] = final
		return final
	}
	parent := filepath.Dir(cleaned)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		final := filepath.Join(resolvedParent, filepath.Base(cleaned))
		pathCache[path] = final
		return final
	}
	pathCache[path] = cleaned
	return cleaned
}

func summarizeNodes(nodes []*parser.Node) []NodeSummary {
	summaries := make([]NodeSummary, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		summaries = append(summaries, summarizeNode(node))
	}
	slices.SortFunc(summaries, func(a, b NodeSummary) int {
		return strings.Compare(a.ID, b.ID)
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
	slices.SortFunc(summaries, func(a, b ImpactNode) int {
		if a.Distance != b.Distance {
			return a.Distance - b.Distance
		}
		return strings.Compare(a.ID, b.ID)
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
	slices.SortFunc(files, func(a, b ChangedFile) int {
		if a.RepoID != b.RepoID {
			return strings.Compare(a.RepoID, b.RepoID)
		}
		return strings.Compare(a.Path, b.Path)
	})
}
