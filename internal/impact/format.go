package impact

import (
	"fmt"
	"sort"
	"strings"
)

func FormatMarkdown(report *Report) string {
	var b strings.Builder
	b.WriteString("## Change Impact\n\n")
	if report == nil {
		b.WriteString("_No impact report available._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "- Base ref: %s\n", markdownInlineCode(report.BaseRef))
	fmt.Fprintf(&b, "- Repositories scanned: **%d**\n", scannedRepoCount(report))
	fmt.Fprintf(&b, "- Changed files: **%d**\n", len(report.ChangedFiles))
	fmt.Fprintf(&b, "- Changed nodes: **%d**\n", len(report.ChangedNodes))
	fmt.Fprintf(&b, "- Impacted nodes: **%d**\n", len(report.ImpactedNodes))
	fmt.Fprintf(&b, "- Warnings: **%d**\n", len(report.Warnings))

	if len(report.ChangedFiles) == 0 {
		b.WriteString("\nNo changes detected.\n")
		writeWarnings(&b, report.Warnings)
		return b.String()
	}

	b.WriteString("\n### Changed Files\n\n")
	for _, file := range report.ChangedFiles {
		fmt.Fprintf(&b, "- %s", markdownInlineCode(file.Path))
		if file.RepoID != "" {
			fmt.Fprintf(&b, " repo %s", markdownInlineCode(file.RepoID))
		}
		fmt.Fprintf(&b, " status %s", markdownInlineCode(file.Status))
		if file.WholeFile {
			b.WriteString(" whole file")
		} else if len(file.Ranges) > 0 {
			b.WriteString(" lines ")
			for i, r := range file.Ranges {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%d-%d", r.Start, r.End)
			}
		}
		b.WriteByte('\n')
	}

	b.WriteString("\n### Changed Nodes\n\n")
	writeNodeSummaries(&b, report.ChangedNodes)

	b.WriteString("\n### Impacted Dependents\n\n")
	if len(report.ImpactedNodes) == 0 {
		b.WriteString("_No impacted dependency nodes found._\n")
	} else {
		currentDistance := -1
		distanceNodes := make([]NodeSummary, 0)
		for _, node := range report.ImpactedNodes {
			if node.Distance != currentDistance {
				writeGroupedNodeSummaries(&b, distanceNodes)
				distanceNodes = distanceNodes[:0]
				currentDistance = node.Distance
				fmt.Fprintf(&b, "\nDistance %d:\n", currentDistance)
			}
			distanceNodes = append(distanceNodes, node.NodeSummary)
		}
		writeGroupedNodeSummaries(&b, distanceNodes)
	}

	writeWarnings(&b, report.Warnings)
	return b.String()
}

func scannedRepoCount(report *Report) int {
	count := 0
	for _, repo := range report.Repos {
		if repo.Scanned {
			count++
		}
	}
	return count
}

func writeNodeSummaries(b *strings.Builder, nodes []NodeSummary) {
	if len(nodes) == 0 {
		b.WriteString("_No matching graph nodes found._\n")
		return
	}
	writeGroupedNodeSummaries(b, nodes)
}

func writeGroupedNodeSummaries(b *strings.Builder, nodes []NodeSummary) {
	if len(nodes) == 0 {
		return
	}
	nodes = sortedNodeSummaries(nodes)
	currentRepo := ""
	currentPkg := ""
	currentFile := ""
	for _, node := range nodes {
		if node.RepoID != currentRepo {
			currentRepo = node.RepoID
			currentPkg = ""
			currentFile = ""
			if currentRepo != "" {
				fmt.Fprintf(b, "\nRepo %s:\n", markdownInlineCode(currentRepo))
			}
		}
		if node.PkgPath != currentPkg {
			currentPkg = node.PkgPath
			currentFile = ""
			if currentPkg != "" {
				fmt.Fprintf(b, "\nPackage %s:\n", markdownInlineCode(currentPkg))
			}
		}
		if node.FilePath != currentFile {
			currentFile = node.FilePath
			if currentFile != "" {
				fmt.Fprintf(b, "\nFile %s:\n", markdownInlineCode(currentFile))
			}
		}
		writeNodeSummaryLine(b, node)
	}
}

func sortedNodeSummaries(nodes []NodeSummary) []NodeSummary {
	sorted := append([]NodeSummary(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].RepoID != sorted[j].RepoID {
			return sorted[i].RepoID < sorted[j].RepoID
		}
		if sorted[i].PkgPath != sorted[j].PkgPath {
			return sorted[i].PkgPath < sorted[j].PkgPath
		}
		if sorted[i].FilePath != sorted[j].FilePath {
			return sorted[i].FilePath < sorted[j].FilePath
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}

func writeNodeSummaryLine(b *strings.Builder, node NodeSummary) {
	fmt.Fprintf(b, "- **%s** (%s) ID: %s", markdownInlineCode(node.Name), markdownInlineCode(node.Type), markdownInlineCode(node.ID))
	if node.FilePath != "" && node.LineStart > 0 && node.LineEnd >= node.LineStart {
		fmt.Fprintf(b, " %s L%d-%d", markdownInlineCode(node.FilePath), node.LineStart, node.LineEnd)
	} else if node.PkgPath != "" {
		fmt.Fprintf(b, " pkg %s", markdownInlineCode(node.PkgPath))
	}
	b.WriteByte('\n')
}

func writeWarnings(b *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	b.WriteString("\n### Warnings\n\n")
	for _, warning := range warnings {
		fmt.Fprintf(b, "- %s\n", markdownText(warning))
	}
}

func markdownInlineCode(value string) string {
	value = markdownText(value)
	fence := strings.Repeat("`", maxBacktickRun(value)+1)
	if value == "" {
		return fence + " " + fence
	}
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") ||
		strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return fence + " " + value + " " + fence
	}
	return fence + value + fence
}

func markdownText(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func maxBacktickRun(value string) int {
	maxRun := 0
	currentRun := 0
	for _, r := range value {
		if r == '`' {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
			continue
		}
		currentRun = 0
	}
	return maxRun
}
