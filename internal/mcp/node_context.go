package mcp

import (
	"fmt"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/parser"
)

func formatNodeContextMarkdown(ctx *graph.NodeContext) string {
	var b strings.Builder
	b.WriteString("## Node Context\n\n")
	if ctx == nil || ctx.Node == nil {
		b.WriteString("_No node context available._\n")
		return b.String()
	}

	writeNodeContextNode(&b, ctx.Node)
	writeNodeContextSource(&b, ctx)
	writeNodeContextRelations(&b, "Dependencies", ctx.Dependencies, "No direct dependency nodes found.")
	writeNodeContextDependents(&b, ctx.Dependents)
	writeNodeContextLocation(&b, ctx)
	writeNodeContextRelations(&b, "Routes", ctx.Routes, "No related route nodes found.")
	writeNodeContextRelations(&b, "Interfaces", ctx.Interfaces, "No related interface or implementation nodes found.")
	writeNodeContextConcurrency(&b, ctx.Concurrency)
	writeNodeContextWarnings(&b, ctx.Warnings)
	return b.String()
}

func writeNodeContextNode(b *strings.Builder, node *parser.Node) {
	b.WriteString("### Node\n\n")
	fmt.Fprintf(b, "- Name: %s\n", markdownInlineCode(node.Name))
	fmt.Fprintf(b, "- ID: %s\n", markdownInlineCode(node.ID))
	fmt.Fprintf(b, "- Type: %s\n", markdownInlineCode(string(node.Type)))
	if node.PkgPath != "" {
		fmt.Fprintf(b, "- Package: %s\n", markdownInlineCode(node.PkgPath))
	}
	if node.FilePath != "" {
		fmt.Fprintf(b, "- File: %s", markdownInlineCode(node.FilePath))
		if node.Lines[0] > 0 && node.Lines[1] >= node.Lines[0] {
			fmt.Fprintf(b, " L%d-%d", node.Lines[0], node.Lines[1])
		}
		b.WriteByte('\n')
	}
	if node.RepoID != "" {
		fmt.Fprintf(b, "- Repo: %s\n", markdownInlineCode(node.RepoID))
	}
}

func writeNodeContextSource(b *strings.Builder, ctx *graph.NodeContext) {
	b.WriteString("\n### Source\n\n")
	if !ctx.SourceRequested {
		b.WriteString("_Source not requested._\n")
		return
	}
	if ctx.SourceCode == "" {
		b.WriteString("_No source available._\n")
		return
	}
	writeMarkdownFencedBlock(b, "go", ctx.SourceCode)
}

func writeNodeContextRelations(b *strings.Builder, title string, relations []graph.NodeContextRelation, emptyMessage string) {
	fmt.Fprintf(b, "\n### %s\n\n", title)
	if len(relations) == 0 {
		fmt.Fprintf(b, "_%s_\n", emptyMessage)
		return
	}
	for _, relation := range relations {
		writeNodeContextRelationLine(b, relation)
	}
}

func writeNodeContextDependents(b *strings.Builder, dependents []graph.NodeDistance) {
	b.WriteString("\n### Dependents\n\n")
	if len(dependents) == 0 {
		b.WriteString("_No dependent nodes found._\n")
		return
	}
	currentDistance := -1
	for _, dependent := range dependents {
		if dependent.Node == nil {
			continue
		}
		if dependent.Distance != currentDistance {
			currentDistance = dependent.Distance
			fmt.Fprintf(b, "Distance %d:\n", currentDistance)
		}
		fmt.Fprintf(b, "- %s\n", nodeContextNodeLabel(dependent.Node))
	}
}

func writeNodeContextLocation(b *strings.Builder, ctx *graph.NodeContext) {
	b.WriteString("\n### Location\n\n")
	node := ctx.Node
	if node.PkgPath == "" && node.FilePath == "" && node.RepoID == "" && len(ctx.Parents) == 0 && len(ctx.Children) == 0 {
		b.WriteString("_No location context available._\n")
		return
	}
	if node.RepoID != "" {
		fmt.Fprintf(b, "- Repo: %s\n", markdownInlineCode(node.RepoID))
	}
	if node.PkgPath != "" {
		fmt.Fprintf(b, "- Package: %s\n", markdownInlineCode(node.PkgPath))
	}
	if node.FilePath != "" {
		fmt.Fprintf(b, "- File: %s\n", markdownInlineCode(node.FilePath))
	}
	if len(ctx.Parents) > 0 {
		b.WriteString("- Parents:\n")
		for _, relation := range ctx.Parents {
			fmt.Fprintf(b, "  - %s\n", nodeContextNodeLabel(relation.Node))
		}
	}
	if len(ctx.Children) > 0 {
		b.WriteString("- Children:\n")
		for _, relation := range ctx.Children {
			fmt.Fprintf(b, "  - %s\n", nodeContextNodeLabel(relation.Node))
		}
	}
}

func writeNodeContextConcurrency(b *strings.Builder, connections []graph.ConcurrencyConnection) {
	b.WriteString("\n### Concurrency\n\n")
	if len(connections) == 0 {
		b.WriteString("_No concurrency context found._\n")
		return
	}
	for _, conn := range connections {
		if conn.Node == nil || conn.Edge == nil {
			continue
		}
		fmt.Fprintf(
			b,
			"- %s %s %s\n",
			markdownInlineCode(conn.Direction),
			markdownInlineCode(string(conn.Edge.Type)),
			nodeContextNodeLabel(conn.Node),
		)
	}
}

func writeNodeContextWarnings(b *strings.Builder, warnings []string) {
	b.WriteString("\n### Warnings\n\n")
	if len(warnings) == 0 {
		b.WriteString("_No warnings._\n")
		return
	}
	for _, warning := range warnings {
		fmt.Fprintf(b, "- %s\n", strings.NewReplacer("\r", " ", "\n", " ").Replace(warning))
	}
}

func writeNodeContextRelationLine(b *strings.Builder, relation graph.NodeContextRelation) {
	if relation.Node == nil {
		return
	}
	edgeType := ""
	if relation.Edge != nil {
		edgeType = string(relation.Edge.Type)
	}
	if edgeType == "" {
		fmt.Fprintf(b, "- %s %s\n", markdownInlineCode(relation.Direction), nodeContextNodeLabel(relation.Node))
		return
	}
	fmt.Fprintf(b, "- %s %s: %s\n", markdownInlineCode(relation.Direction), markdownInlineCode(edgeType), nodeContextNodeLabel(relation.Node))
}

func nodeContextNodeLabel(node *parser.Node) string {
	if node == nil {
		return "_missing node_"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** (%s) ID: %s", markdownInlineCode(node.Name), markdownInlineCode(string(node.Type)), markdownInlineCode(node.ID))
	if node.FilePath != "" && node.Lines[0] > 0 && node.Lines[1] >= node.Lines[0] {
		fmt.Fprintf(&b, " %s L%d-%d", markdownInlineCode(node.FilePath), node.Lines[0], node.Lines[1])
	} else if node.PkgPath != "" {
		fmt.Fprintf(&b, " pkg %s", markdownInlineCode(node.PkgPath))
	}
	return b.String()
}
