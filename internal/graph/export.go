package graph

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/hungpdn/gokg/internal/parser"
)

// ExportJSON exports the graph to JSON format.
func (g *Graph) ExportJSON() (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var out struct {
		Nodes []*parser.Node `json:"nodes"`
		Edges []*parser.Edge `json:"edges"`
	}
	out.Nodes = make([]*parser.Node, 0, len(g.nodes))
	out.Edges = make([]*parser.Edge, 0, g.edgeCountLocked())

	for _, node := range g.nodes {
		out.Nodes = append(out.Nodes, node)
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				out.Edges = append(out.Edges, edge)
			}
		}
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (g *Graph) edgeCountLocked() int {
	count := 0
	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			count += len(edges)
		}
	}
	return count
}

// ExportMermaid exports the graph to Mermaid flowchart format.
func (g *Graph) ExportMermaid() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var b strings.Builder
	b.WriteString("flowchart TD\n")

	for _, node := range g.nodes {
		sid := mermaidSafeID(node.ID)
		b.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", sid, escapeMermaidLabel(nodeLabel(node.Name, string(node.Type)))))
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				sFrom := mermaidSafeID(edge.From)
				sTo := mermaidSafeID(edge.To)
				b.WriteString(fmt.Sprintf("    %s -->|%s| %s\n", sFrom, escapeMermaidLabel(string(edge.Type)), sTo))
			}
		}
	}

	return b.String()
}

// ExportDot exports the graph to Graphviz DOT format.
func (g *Graph) ExportDot() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var b strings.Builder
	b.WriteString("digraph G {\n")
	b.WriteString("  node [shape=box];\n")

	safeID := func(id string) string {
		return dotQuote(id)
	}

	for _, node := range g.nodes {
		b.WriteString(fmt.Sprintf("  %s [label=%s];\n", safeID(node.ID), dotQuote(nodeLabel(node.Name, string(node.Type)))))
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				b.WriteString(fmt.Sprintf("  %s -> %s %s;\n", safeID(edge.From), safeID(edge.To), dotEdgeAttrs(edge)))
			}
		}
	}

	b.WriteString("}\n")
	return b.String()
}

func dotEdgeAttrs(edge *parser.Edge) string {
	attrs := []string{fmt.Sprintf("label=%s", dotQuote(string(edge.Type)))}
	if len(edge.Occurrences) > 0 {
		attrs = append(attrs, fmt.Sprintf("occurrences=%s", dotQuote(fmt.Sprint(len(edge.Occurrences)))))
		attrs = append(attrs, fmt.Sprintf("lines=%s", dotQuote(edgeOccurrenceLines(edge.Occurrences))))
	}
	return "[" + strings.Join(attrs, ", ") + "]"
}

func edgeOccurrenceLines(occurrences []parser.EdgeOccurrence) string {
	var b strings.Builder
	for i, occurrence := range occurrences {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(occurrence.FilePath)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(occurrence.Line))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(occurrence.Column))
	}
	return b.String()
}

func dotQuote(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		`"`, `\"`,
		"\r", `\r`,
		"\n", `\n`,
	)
	return `"` + replacer.Replace(s) + `"`
}

func nodeLabel(name, nodeType string) string {
	if nodeType == "" {
		return name
	}
	return fmt.Sprintf("%s:%s", name, nodeType)
}

func mermaidSafeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r == '_' || isASCIIAlphaNum(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "_"
	}
	safe := b.String()
	if safe[0] >= '0' && safe[0] <= '9' {
		return "_" + safe
	}
	return safe
}

func isASCIIAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func escapeMermaidLabel(label string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		`"`, `\"`,
		"\r", `\r`,
		"\n", `\n`,
	)
	return replacer.Replace(label)
}
