package parser

import (
	"strings"
	"sync"
)

// NodeType defines the type of a parsed node
type NodeType string

const (
	NodeTypePackage   NodeType = "PACKAGE"
	NodeTypeFile      NodeType = "FILE"
	NodeTypeFolder    NodeType = "FOLDER"
	NodeTypeStruct    NodeType = "STRUCT"
	NodeTypeInterface NodeType = "INTERFACE"
	NodeTypeFunc      NodeType = "FUNC"
	NodeTypeMethod    NodeType = "METHOD"
	NodeTypeVar       NodeType = "VAR"
	NodeTypeChannel   NodeType = "CHANNEL"
	NodeTypeGoroutine NodeType = "GOROUTINE"
	NodeTypeBoundary  NodeType = "BOUNDARY" // External package/func
	NodeTypeRepo      NodeType = "REPO"
	NodeTypeWorkspace NodeType = "WORKSPACE"
)

type Node struct {
	ID       string // Unique identifier (e.g. fully qualified name or path)
	Type     NodeType
	Name     string
	PkgPath  string
	FilePath string
	Lines    [2]int // Start, End line
	RepoID   string // The ID of the repository this node belongs to
}

// EdgeType defines the relation between two nodes
type EdgeType string

const (
	EdgeTypeCalls        EdgeType = "CALLS"
	EdgeTypeImplements   EdgeType = "IMPLEMENTS"
	EdgeTypeImports      EdgeType = "IMPORTS"
	EdgeTypeReferences   EdgeType = "REFERENCES"
	EdgeTypeInstantiates EdgeType = "INSTANTIATES"
	EdgeTypeSpawns       EdgeType = "SPAWNS"
	EdgeTypeSendsTo      EdgeType = "SENDS_TO"
	EdgeTypeReceivesFrom EdgeType = "RECEIVES_FROM"
	EdgeTypeContains     EdgeType = "CONTAINS" // e.g. Package contains File, File contains Func
)

// Edge represents a relation between two nodes
type Edge struct {
	From   string
	To     string
	Type   EdgeType
	RepoID string // The ID of the repository that discovered this edge
}

// Memory Optimization: Object pools for Node and Edge
var nodePool = sync.Pool{
	New: func() interface{} {
		return new(Node)
	},
}

var edgePool = sync.Pool{
	New: func() interface{} {
		return new(Edge)
	},
}

// NewNode acquires a Node from the pool
func NewNode() *Node {
	return nodePool.Get().(*Node)
}

// ReleaseNode resets and returns a Node to the pool
func ReleaseNode(n *Node) {
	*n = Node{} // Reset
	nodePool.Put(n)
}

// NewEdge acquires an Edge from the pool
func NewEdge() *Edge {
	return edgePool.Get().(*Edge)
}

// ReleaseEdge resets and returns an Edge to the pool
func ReleaseEdge(e *Edge) {
	*e = Edge{} // Reset
	edgePool.Put(e)
}

// BuildID optimizes string concatenation for node IDs using strings.Builder
func BuildID(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}

	var length int
	for _, p := range parts {
		length += len(p)
	}

	var b strings.Builder
	b.Grow(length)
	for _, p := range parts {
		b.WriteString(p)
	}
	return b.String()
}
