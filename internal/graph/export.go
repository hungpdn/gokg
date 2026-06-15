package graph

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExportJSON exports the graph to JSON format.
func (g *Graph) ExportJSON() (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var out struct {
		Nodes []interface{} `json:"nodes"`
		Edges []interface{} `json:"edges"`
	}

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
		return fmt.Sprintf("\"%s\"", strings.ReplaceAll(id, "\"", "\\\""))
	}

	for _, node := range g.nodes {
		b.WriteString(fmt.Sprintf("  %s [label=\"%s\"];\n", safeID(node.ID), nodeLabel(node.Name, string(node.Type))))
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				b.WriteString(fmt.Sprintf("  %s -> %s [label=\"%s\"];\n", safeID(edge.From), safeID(edge.To), edge.Type))
			}
		}
	}

	b.WriteString("}\n")
	return b.String()
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
