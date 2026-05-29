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

	// Pre-process nodes to get safe IDs for mermaid
	safeID := func(id string) string {
		s := strings.ReplaceAll(id, ".", "_")
		s = strings.ReplaceAll(s, "/", "_")
		s = strings.ReplaceAll(s, "-", "_")
		s = strings.ReplaceAll(s, "(", "_")
		s = strings.ReplaceAll(s, ")", "_")
		s = strings.ReplaceAll(s, "*", "_")
		return s
	}

	for _, node := range g.nodes {
		sid := safeID(node.ID)
		b.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", sid, node.Name))
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				sFrom := safeID(edge.From)
				sTo := safeID(edge.To)
				b.WriteString(fmt.Sprintf("    %s -->|%s| %s\n", sFrom, edge.Type, sTo))
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
		b.WriteString(fmt.Sprintf("  %s [label=\"%s\"];\n", safeID(node.ID), node.Name))
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
