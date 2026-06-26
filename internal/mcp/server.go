package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hungpdn/gokg/internal/graph"
	"github.com/hungpdn/gokg/internal/impact"
	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/version"
)

type Server struct {
	graph       *graph.Graph
	impactRepos []impact.Repo
}

type ServerOption func(*Server)

const (
	maxStdioMessageBytes = 4 << 20

	latestMCPProtocolVersion = "2025-06-18"
	legacyMCPProtocolVersion = "2024-11-05"
)

var errMCPCypherLimitRequired = errors.New("execute_cypher requires LIMIT to protect MCP clients from unbounded result sets")

var supportedMCPProtocolVersions = map[string]struct{}{
	latestMCPProtocolVersion: {},
	legacyMCPProtocolVersion: {},
}

func NewServer(g *graph.Graph, opts ...ServerOption) *Server {
	s := &Server{graph: g}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func WithImpactRepos(repos []impact.Repo) ServerOption {
	return func(s *Server) {
		s.impactRepos = append([]impact.Repo(nil), repos...)
	}
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Start(ctx context.Context) error {
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStdioMessageBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}

		line := scanner.Bytes()

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := writeError(out, nil, -32700, "Parse error"); err != nil {
				return err
			}
			continue
		}

		res := s.handleRequest(&req)
		if res != nil {
			if err := writeResponse(out, res); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequest(req *Request) *Response {
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32600, Message: "Invalid Request"}}
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	}

	if req.ID != nil {
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Method not found: " + req.Method}}
	}
	return nil
}

func (s *Server) handleInitialize(req *Request) *Response {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32602, Message: "Invalid params"}}
		}
	}

	return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
		"protocolVersion": negotiateMCPProtocolVersion(params.ProtocolVersion),
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"serverInfo":      map[string]string{"name": "gokg", "version": version.Get().Version},
	}}
}

func negotiateMCPProtocolVersion(requested string) string {
	if _, ok := supportedMCPProtocolVersions[requested]; ok {
		return requested
	}
	return latestMCPProtocolVersion
}

func (s *Server) handleToolsList(req *Request) *Response {
	tools := make([]map[string]interface{}, 0, len(s.toolDefinitions()))
	for _, tool := range s.toolDefinitions() {
		tools = append(tools, tool.metadata())
	}

	return &Response{ID: req.ID, JSONRPC: "2.0", Result: map[string]interface{}{
		"tools": tools,
	}}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32602, Message: "Invalid params"}}
	}

	for _, tool := range s.toolDefinitions() {
		if tool.name == params.Name {
			return tool.handler(s, req.ID, params.Arguments)
		}
	}
	return &Response{ID: req.ID, JSONRPC: "2.0", Error: &Error{Code: -32601, Message: "Unknown tool: " + params.Name}}
}

// --- Markdown formatting helpers ---

func formatNodeListMarkdown(title, nodeID string, nodes []*parser.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s of `%s`\n\n", title, nodeID)

	if len(nodes) == 0 {
		b.WriteString("_No results found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Found **%d** node(s):\n\n", len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&b, "- **`%s`** (`%s`) — ID: `%s`", n.Name, n.Type, n.ID)
		if n.FilePath != "" && n.Lines[0] > 0 && n.Lines[1] >= n.Lines[0] {
			fmt.Fprintf(&b, " — `%s` L%d-%d", n.FilePath, n.Lines[0], n.Lines[1])
		} else if n.PkgPath != "" {
			fmt.Fprintf(&b, " — pkg: `%s`", n.PkgPath)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatConcurrencyGraphMarkdown(nodeID string, connections []graph.ConcurrencyConnection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Concurrency Graph of `%s`\n\n", nodeID)

	if len(connections) == 0 {
		b.WriteString("_No concurrency nodes found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Found **%d** connection(s):\n\n", len(connections))
	for _, conn := range connections {
		if conn.Node == nil || conn.Edge == nil {
			continue
		}

		if conn.Direction == "inbound" {
			fmt.Fprintf(&b, "- **`%s`** (`%s`) --_%s_--> `%s`\n", conn.Node.Name, conn.Node.Type, conn.Edge.Type, nodeID)
		} else {
			fmt.Fprintf(&b, "- `%s` --_%s_--> **`%s`** (`%s`)\n", nodeID, conn.Edge.Type, conn.Node.Name, conn.Node.Type)
		}
	}

	return b.String()
}

func formatSourceCodeMarkdown(nodeID, code string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Source Code of `%s`\n\n", nodeID)
	writeMarkdownFencedBlock(&b, "go", code)
	return b.String()
}

func formatRepositoryStructureMarkdown(root *graph.RepositoryStructureNode) string {
	var b strings.Builder
	b.WriteString("## Repository Structure\n\n")
	if root == nil || root.Node == nil {
		b.WriteString("_No repository structure found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "%s\n", repositoryStructureLabel(root.Node))
	for i, child := range root.Children {
		writeRepositoryStructureNode(&b, child, "", i == len(root.Children)-1)
	}
	return b.String()
}

func writeRepositoryStructureNode(b *strings.Builder, node *graph.RepositoryStructureNode, prefix string, last bool) {
	if node == nil || node.Node == nil {
		return
	}
	connector := "|-- "
	nextPrefix := prefix + "|   "
	if last {
		connector = "└─- "
		nextPrefix = prefix + "    "
	}
	fmt.Fprintf(b, "%s%s%s\n", prefix, connector, repositoryStructureLabel(node.Node))
	for i, child := range node.Children {
		writeRepositoryStructureNode(b, child, nextPrefix, i == len(node.Children)-1)
	}
}

func repositoryStructureLabel(node *parser.Node) string {
	name := markdownInlineCode(node.Name)
	switch node.Type {
	case parser.NodeTypeFolder:
		return fmt.Sprintf("%s (`%s`)", markdownInlineCode(node.Name+"/"), node.Type)
	case parser.NodeTypePackage:
		return fmt.Sprintf("%s (`%s`, pkg: %s)", name, node.Type, markdownInlineCode(node.ID))
	case parser.NodeTypeFile:
		return fmt.Sprintf("%s (`%s`)", name, node.Type)
	default:
		return fmt.Sprintf("%s (`%s`)", name, node.Type)
	}
}

func formatPathMarkdown(sourceID, targetID string, pathResults []graph.PathResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Shortest Path: `%s` → `%s`\n\n", sourceID, targetID)

	if len(pathResults) == 0 {
		b.WriteString("_No path found._\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Path length: **%d** hop(s)\n\n", len(pathResults)-1)
	for i, pr := range pathResults {
		var prefix string
		if i == 0 {
			prefix = "▶ "
		} else if i == len(pathResults)-1 {
			prefix = "◉ "
		} else {
			prefix = "→ "
		}

		fmt.Fprintf(&b, "%s**`%s`** (`%s`)\n", prefix, pr.Node.Name, pr.Node.Type)
		if pr.EdgeType != "" {
			fmt.Fprintf(&b, "  ↓ _%s_\n", pr.EdgeType)
		}
	}
	return b.String()
}

func formatCypherMarkdown(query, jsonData string) string {
	var b strings.Builder
	b.WriteString("## Cypher Query Results\n\n")
	b.WriteString("**Query:**\n")
	writeMarkdownFencedBlock(&b, "cypher", query)
	b.WriteByte('\n')
	b.WriteString("**Results:**\n")
	writeMarkdownFencedBlock(&b, "json", jsonData)
	return b.String()
}

func writeMarkdownFencedBlock(b *strings.Builder, language string, content string) {
	fence := markdownFence(content)
	b.WriteString(fence)
	b.WriteString(language)
	b.WriteByte('\n')
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(fence)
	b.WriteByte('\n')
}

func markdownFence(content string) string {
	maxRun := maxBacktickRun(content)
	if maxRun < 3 {
		maxRun = 3
	} else {
		maxRun++
	}
	return strings.Repeat("`", maxRun)
}

func markdownInlineCode(value string) string {
	value = strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
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

func encodeIndentedJSON(value interface{}) (string, error) {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// --- Response helpers ---

func (s *Server) textResult(id interface{}, text string) *Response {
	return &Response{ID: id, JSONRPC: "2.0", Result: map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
	}}
}

func (s *Server) errorResult(id interface{}, err error) *Response {
	return &Response{ID: id, JSONRPC: "2.0", Error: &Error{Code: -32000, Message: err.Error()}}
}

func writeError(out io.Writer, id interface{}, code int, message string) error {
	return writeResponse(out, &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	})
}

func writeResponse(out io.Writer, res *Response) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", data)
	return err
}
