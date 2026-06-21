package graph

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/hungpdn/gokg/internal/parser"
)

var (
	dotReplacer = strings.NewReplacer(
		"\\", "\\\\",
		`"`, `\"`,
		"\r", `\r`,
		"\n", `\n`,
	)
	mermaidLabelReplacer = strings.NewReplacer(
		"\\", "\\\\",
		`"`, `\"`,
		"\r", `\r`,
		"\n", `\n`,
	)
)

// ExportJSON exports the graph to JSON format.
func (g *Graph) ExportJSON() (string, error) {
	var b strings.Builder
	if err := g.ExportJSONTo(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ExportJSONTo streams the graph as JSON without materializing full node and
// edge slices or a full output byte buffer.
func (g *Graph) ExportJSONTo(w io.Writer) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ew := exportWriter{w: w}
	ew.WriteString("{\n  \"nodes\": [")
	wroteNodes := false
	for _, node := range g.nodes {
		if node == nil {
			continue
		}
		if wroteNodes {
			ew.WriteString(",\n")
		} else {
			ew.WriteString("\n")
			wroteNodes = true
		}
		writeIndentedJSONValue(&ew, node, "    ")
	}
	if wroteNodes {
		ew.WriteString("\n  ")
	}

	ew.WriteString("],\n  \"edges\": [")
	wroteEdges := false
	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				if edge == nil {
					continue
				}
				if wroteEdges {
					ew.WriteString(",\n")
				} else {
					ew.WriteString("\n")
					wroteEdges = true
				}
				writeIndentedJSONValue(&ew, edge, "    ")
			}
		}
	}
	if wroteEdges {
		ew.WriteString("\n  ")
	}
	ew.WriteString("]\n}")
	return ew.Err()
}

// ExportMermaid exports the graph to Mermaid flowchart format.
func (g *Graph) ExportMermaid() string {
	var b strings.Builder
	if err := g.ExportMermaidTo(&b); err != nil {
		return ""
	}
	return b.String()
}

// ExportMermaidTo streams the graph to Mermaid flowchart format.
func (g *Graph) ExportMermaidTo(w io.Writer) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ew := exportWriter{w: w}
	ew.WriteString("flowchart TD\n")

	for _, node := range g.nodes {
		sid := mermaidSafeID(node.ID)
		ew.WriteString("    ")
		ew.WriteString(sid)
		ew.WriteString("[\"")
		writeEscapedMermaidLabel(&ew, node.Name, string(node.Type))
		ew.WriteString("\"]\n")
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				sFrom := mermaidSafeID(edge.From)
				sTo := mermaidSafeID(edge.To)
				ew.WriteString("    ")
				ew.WriteString(sFrom)
				ew.WriteString(" -->|")
				writeEscapedMermaidString(&ew, string(edge.Type))
				ew.WriteString("| ")
				ew.WriteString(sTo)
				ew.WriteByteValue('\n')
			}
		}
	}

	return ew.Err()
}

// ExportDot exports the graph to Graphviz DOT format.
func (g *Graph) ExportDot() string {
	var b strings.Builder
	if err := g.ExportDotTo(&b); err != nil {
		return ""
	}
	return b.String()
}

// ExportDotTo streams the graph to Graphviz DOT format.
func (g *Graph) ExportDotTo(w io.Writer) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ew := exportWriter{w: w}
	ew.WriteString("digraph G {\n")
	ew.WriteString("  node [shape=box];\n")

	for _, node := range g.nodes {
		ew.WriteString("  ")
		writeDotQuoted(&ew, node.ID)
		ew.WriteString(" [label=")
		writeDotLabelQuoted(&ew, node.Name, string(node.Type))
		ew.WriteString("];\n")
	}

	for _, edgeMap := range g.edges {
		for _, edges := range edgeMap {
			for _, edge := range edges {
				ew.WriteString("  ")
				writeDotQuoted(&ew, edge.From)
				ew.WriteString(" -> ")
				writeDotQuoted(&ew, edge.To)
				ew.WriteByteValue(' ')
				writeDotEdgeAttrs(&ew, edge)
				ew.WriteString(";\n")
			}
		}
	}

	ew.WriteString("}\n")
	return ew.Err()
}

func writeDotEdgeAttrs(ew *exportWriter, edge *parser.Edge) {
	ew.WriteString("[label=")
	writeDotQuoted(ew, string(edge.Type))
	if len(edge.Occurrences) > 0 {
		ew.WriteString(", occurrences=")
		writeDotQuoted(ew, strconv.Itoa(len(edge.Occurrences)))
		ew.WriteString(", lines=")
		writeDotQuoted(ew, edgeOccurrenceLines(edge.Occurrences))
	}
	ew.WriteByteValue(']')
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

type exportWriter struct {
	w   io.Writer
	err error
}

func (w *exportWriter) WriteString(s string) {
	if w.err != nil {
		return
	}
	_, w.err = io.WriteString(w.w, s)
}

func (w *exportWriter) WriteByteValue(b byte) {
	if w.err != nil {
		return
	}
	var buf [1]byte
	buf[0] = b
	_, w.err = w.w.Write(buf[:])
}

func (w *exportWriter) WriteBytes(data []byte) {
	if w.err != nil {
		return
	}
	_, w.err = w.w.Write(data)
}

func (w *exportWriter) WriteReplaced(replacer *strings.Replacer, s string) {
	if w.err != nil {
		return
	}
	_, w.err = replacer.WriteString(w.w, s)
}

func (w *exportWriter) Err() error {
	return w.err
}

func writeIndentedJSONValue(w *exportWriter, value interface{}, indent string) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		w.err = err
		return
	}

	for len(data) > 0 {
		w.WriteString(indent)
		lineEnd := bytes.IndexByte(data, '\n')
		if lineEnd < 0 {
			w.WriteBytes(data)
			return
		}
		w.WriteBytes(data[:lineEnd])
		w.WriteByteValue('\n')
		data = data[lineEnd+1:]
	}
}

func writeDotQuoted(w *exportWriter, s string) {
	w.WriteByteValue('"')
	w.WriteReplaced(dotReplacer, s)
	w.WriteByteValue('"')
}

func writeDotLabelQuoted(w *exportWriter, name string, nodeType string) {
	w.WriteByteValue('"')
	w.WriteReplaced(dotReplacer, name)
	if nodeType != "" {
		w.WriteByteValue(':')
		w.WriteReplaced(dotReplacer, nodeType)
	}
	w.WriteByteValue('"')
}

func writeEscapedMermaidLabel(w *exportWriter, name string, nodeType string) {
	w.WriteReplaced(mermaidLabelReplacer, name)
	if nodeType != "" {
		w.WriteByteValue(':')
		w.WriteReplaced(mermaidLabelReplacer, nodeType)
	}
}

func writeEscapedMermaidString(w *exportWriter, s string) {
	w.WriteReplaced(mermaidLabelReplacer, s)
}
