package graph

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/hungpdn/gokg/internal/parser"
)

// QueryBuilder provides an interface to search the graph.
type QueryBuilder struct {
	g *Graph
}

type ConcurrencyConnection struct {
	Node      *parser.Node `json:"node"`
	Edge      *parser.Edge `json:"edge"`
	Direction string       `json:"direction"`
}

type concurrencySeenKey struct {
	direction string
	fromID    int64
	toID      int64
	edgeType  parser.EdgeType
}

const maxSearchResults = 50

// Query returns a new QueryBuilder for the graph.
func (g *Graph) Query() *QueryBuilder {
	return &QueryBuilder{g: g}
}

// GetDependencies returns all nodes that the given node ID calls or imports.
func (qb *QueryBuilder) GetDependencies(nodeID string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	id, exists := qb.g.nodeMap[nodeID]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}

	deps := make([]*parser.Node, 0, len(qb.g.edges[id]))
	for toID := range qb.g.edges[id] {
		if pNode, ok := qb.g.nodes[toID]; ok {
			deps = append(deps, pNode)
		}
	}
	return deps, nil
}

// GetBlastRadius returns all nodes that depend on the given node ID.
func (qb *QueryBuilder) GetBlastRadius(nodeID string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	id, exists := qb.g.nodeMap[nodeID]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}

	var blast []*parser.Node
	for fromID, outEdges := range qb.g.edges {
		if _, ok := outEdges[id]; !ok {
			continue
		}
		if pNode, ok := qb.g.nodes[fromID]; ok {
			blast = append(blast, pNode)
		}
	}
	return blast, nil
}

// GetConcurrencyFlow returns goroutines and channels connected to this node.
func (qb *QueryBuilder) GetConcurrencyFlow(nodeID string) ([]*parser.Node, error) {
	connections, err := qb.GetConcurrencyGraph(nodeID)
	if err != nil {
		return nil, err
	}

	seen := make(map[*parser.Node]bool)
	flows := make([]*parser.Node, 0, len(connections))
	for _, conn := range connections {
		if conn.Node == nil || seen[conn.Node] {
			continue
		}
		seen[conn.Node] = true
		flows = append(flows, conn.Node)
	}

	return flows, nil
}

// GetConcurrencyGraph returns goroutine and channel connections for a node.
func (qb *QueryBuilder) GetConcurrencyGraph(nodeID string) ([]ConcurrencyConnection, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	id, exists := qb.g.nodeMap[nodeID]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}

	connections := make([]ConcurrencyConnection, 0)
	seen := make(map[concurrencySeenKey]bool)

	for toID, edges := range qb.g.edges[id] {
		pNode, ok := qb.g.nodes[toID]
		if !ok || !isConcurrencyNode(pNode.Type) {
			qb.appendCalledConcurrencyConnections(toID, edges, "via_call", &connections, seen)
			qb.appendSpawnedConcurrencyConnections(toID, edges, &connections, seen)
			continue
		}

		for _, edge := range edges {
			if edge == nil || !isConcurrencyEdge(edge.Type) {
				continue
			}

			key := concurrencySeenKey{direction: "outbound", fromID: id, toID: toID, edgeType: edge.Type}
			if seen[key] {
				continue
			}
			seen[key] = true
			connections = append(connections, ConcurrencyConnection{Node: pNode, Edge: edge, Direction: "outbound"})
		}
		if pNode.Type == parser.NodeTypeGoroutine {
			qb.appendSpawnedConcurrencyConnections(toID, edges, &connections, seen)
		}
	}

	for fromID, outEdges := range qb.g.edges {
		edges, ok := outEdges[id]
		if !ok {
			continue
		}
		pNode, ok := qb.g.nodes[fromID]
		if !ok || !isConcurrencyNode(pNode.Type) {
			continue
		}

		for _, edge := range edges {
			if edge == nil {
				continue
			}
			if !isConcurrencyEdge(edge.Type) && !(edge.Type == parser.EdgeTypeCalls && pNode.Type == parser.NodeTypeGoroutine) {
				continue
			}

			key := concurrencySeenKey{direction: "inbound", fromID: fromID, toID: id, edgeType: edge.Type}
			if seen[key] {
				continue
			}
			seen[key] = true
			connections = append(connections, ConcurrencyConnection{Node: pNode, Edge: edge, Direction: "inbound"})
		}
	}

	return connections, nil
}

func (qb *QueryBuilder) appendCalledConcurrencyConnections(
	calleeID int64,
	callEdges []*parser.Edge,
	direction string,
	connections *[]ConcurrencyConnection,
	seen map[concurrencySeenKey]bool,
) {
	for _, callEdge := range callEdges {
		if callEdge == nil || callEdge.Type != parser.EdgeTypeCalls {
			continue
		}
		qb.appendOutboundConcurrencyConnections(calleeID, direction, connections, seen)
	}
}

func (qb *QueryBuilder) appendSpawnedConcurrencyConnections(
	goroutineID int64,
	spawnEdges []*parser.Edge,
	connections *[]ConcurrencyConnection,
	seen map[concurrencySeenKey]bool,
) {
	for _, spawnEdge := range spawnEdges {
		if spawnEdge == nil || spawnEdge.Type != parser.EdgeTypeSpawns {
			continue
		}
		qb.appendOutboundConcurrencyConnections(goroutineID, "via_goroutine", connections, seen)
		for calleeID, edges := range qb.g.edges[goroutineID] {
			qb.appendCalledConcurrencyConnections(calleeID, edges, "via_goroutine", connections, seen)
		}
	}
}

func (qb *QueryBuilder) appendOutboundConcurrencyConnections(
	fromID int64,
	direction string,
	connections *[]ConcurrencyConnection,
	seen map[concurrencySeenKey]bool,
) {
	for toID, edges := range qb.g.edges[fromID] {
		pNode, ok := qb.g.nodes[toID]
		if !ok || !isConcurrencyNode(pNode.Type) {
			continue
		}
		for _, edge := range edges {
			if edge == nil || !isConcurrencyEdge(edge.Type) {
				continue
			}
			key := concurrencySeenKey{direction: direction, fromID: fromID, toID: toID, edgeType: edge.Type}
			if seen[key] {
				continue
			}
			seen[key] = true
			*connections = append(*connections, ConcurrencyConnection{Node: pNode, Edge: edge, Direction: direction})
		}
	}
}

func isConcurrencyEdge(edgeType parser.EdgeType) bool {
	return edgeType == parser.EdgeTypeSpawns ||
		edgeType == parser.EdgeTypeSendsTo ||
		edgeType == parser.EdgeTypeReceivesFrom
}

func isConcurrencyNode(nodeType parser.NodeType) bool {
	return nodeType == parser.NodeTypeGoroutine || nodeType == parser.NodeTypeChannel
}

// GetImplementations returns all structs that implement the given interface node ID.
func (qb *QueryBuilder) GetImplementations(interfaceID string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	ifaceNumID, exists := qb.g.nodeMap[interfaceID]
	if !exists {
		return nil, fmt.Errorf("interface node not found: %s", interfaceID)
	}

	// Check that the target is actually an INTERFACE node
	ifaceNode := qb.g.nodes[ifaceNumID]
	if ifaceNode != nil && ifaceNode.Type != parser.NodeTypeInterface {
		return nil, fmt.Errorf("node %s is not an INTERFACE (type: %s)", interfaceID, ifaceNode.Type)
	}

	var impls []*parser.Node
	for fromNumID, outEdges := range qb.g.edges {
		for _, edge := range outEdges[ifaceNumID] {
			if edge != nil && edge.Type == parser.EdgeTypeImplements {
				if pNode, ok := qb.g.nodes[fromNumID]; ok {
					impls = append(impls, pNode)
				}
				break
			}
		}
	}
	return impls, nil
}

// GetSourceCode reads the source code of the given node from disk.
func (qb *QueryBuilder) GetSourceCode(nodeID string) (string, error) {
	qb.g.mu.RLock()

	numID, exists := qb.g.nodeMap[nodeID]
	if !exists {
		qb.g.mu.RUnlock()
		return "", fmt.Errorf("node not found: %s", nodeID)
	}

	pNode := qb.g.nodes[numID]
	if pNode == nil {
		qb.g.mu.RUnlock()
		return "", fmt.Errorf("node data missing: %s", nodeID)
	}

	filePath := pNode.FilePath
	lines := pNode.Lines
	qb.g.mu.RUnlock()

	if filePath == "" {
		return "", fmt.Errorf("node %s has no file path", nodeID)
	}

	if lines[0] == 0 && lines[1] == 0 {
		return "", fmt.Errorf("node %s has no line range info", nodeID)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer f.Close()

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= lines[0] && lineNum <= lines[1] {
			b.WriteString(scanner.Text())
			b.WriteByte('\n')
		}
		if lineNum > lines[1] {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	return b.String(), nil
}

// PathResult represents a node in the shortest path with edge info.
type PathResult struct {
	Node     *parser.Node `json:"node"`
	EdgeType string       `json:"edge_type,omitempty"` // edge connecting this node to the next
}

// FindPath finds the shortest path between two nodes using BFS.
func (qb *QueryBuilder) FindPath(sourceID, targetID string) ([]PathResult, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	srcNumID, srcExists := qb.g.nodeMap[sourceID]
	tgtNumID, tgtExists := qb.g.nodeMap[targetID]

	if !srcExists {
		return nil, fmt.Errorf("source node not found: %s", sourceID)
	}
	if !tgtExists {
		return nil, fmt.Errorf("target node not found: %s", targetID)
	}
	if qb.g.nodes[srcNumID] == nil {
		return nil, fmt.Errorf("source node not found: %s", sourceID)
	}
	if qb.g.nodes[tgtNumID] == nil {
		return nil, fmt.Errorf("target node not found: %s", targetID)
	}

	pathIDs := qb.shortestPathIDs(srcNumID, tgtNumID)

	if len(pathIDs) == 0 {
		return nil, fmt.Errorf("no path found from %s to %s", sourceID, targetID)
	}

	results := make([]PathResult, 0, len(pathIDs))
	for i, numID := range pathIDs {
		pNode := qb.g.nodes[numID]
		if pNode == nil {
			continue
		}

		pr := PathResult{Node: pNode}

		// Add the edge type connecting this node to the next
		if i < len(pathIDs)-1 {
			nextNumID := pathIDs[i+1]
			if edgeMap, ok := qb.g.edges[numID]; ok {
				if edges, ok := edgeMap[nextNumID]; ok && len(edges) > 0 {
					pr.EdgeType = string(edges[0].Type)
				}
			}
		}

		results = append(results, pr)
	}

	return results, nil
}

func (qb *QueryBuilder) shortestPathIDs(sourceID, targetID int64) []int64 {
	if sourceID == targetID {
		return []int64{sourceID}
	}

	queue := []int64{sourceID}
	seen := map[int64]bool{sourceID: true}
	prev := make(map[int64]int64)

	for head := 0; head < len(queue); head++ {
		currentID := queue[head]
		for nextID := range qb.g.edges[currentID] {
			if seen[nextID] || qb.g.nodes[nextID] == nil {
				continue
			}
			seen[nextID] = true
			prev[nextID] = currentID
			if nextID == targetID {
				return reconstructPathIDs(sourceID, targetID, prev)
			}
			queue = append(queue, nextID)
		}
	}

	return nil
}

func reconstructPathIDs(sourceID, targetID int64, prev map[int64]int64) []int64 {
	reversed := []int64{targetID}
	for currentID := targetID; currentID != sourceID; {
		parentID, ok := prev[currentID]
		if !ok {
			return nil
		}
		reversed = append(reversed, parentID)
		currentID = parentID
	}

	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

// SearchNodes returns up to 50 nodes whose Name or ID contains the query string (case-insensitive).
func (qb *QueryBuilder) SearchNodes(query string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	lowerQuery := strings.ToLower(query)
	asciiQuery := isASCIIString(lowerQuery)
	results := make([]*parser.Node, 0, maxSearchResults)

	for _, pNode := range qb.g.nodes {
		if containsCaseInsensitive(pNode.Name, lowerQuery, asciiQuery) || containsCaseInsensitive(pNode.ID, lowerQuery, asciiQuery) {
			results = append(results, pNode)
			if len(results) >= maxSearchResults {
				break
			}
		}
	}

	return results, nil
}

func containsCaseInsensitive(s string, lowerQuery string, asciiQuery bool) bool {
	if lowerQuery == "" {
		return true
	}
	if asciiQuery {
		return containsASCIIFold(s, lowerQuery)
	}
	return strings.Contains(strings.ToLower(s), lowerQuery)
}

func containsASCIIFold(s string, lowerQuery string) bool {
	queryLen := len(lowerQuery)
	if queryLen == 0 {
		return true
	}
	if queryLen > len(s) {
		return false
	}

	first := lowerQuery[0]
	for i := 0; i <= len(s)-queryLen; i++ {
		if asciiLower(s[i]) != first {
			continue
		}

		matched := true
		for j := 1; j < queryLen; j++ {
			if asciiLower(s[i+j]) != lowerQuery[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
