package graph

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// FileRange describes a changed line range in a source file. WholeFile ranges
// match every node with the same FilePath.
type FileRange struct {
	FilePath  string
	StartLine int
	EndLine   int
	WholeFile bool
	RepoID    string
}

// NodeDistance is a graph node reached at a specific inbound dependency depth.
type NodeDistance struct {
	Node     *parser.Node `json:"node"`
	Distance int          `json:"distance"`
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

// NodesForFileRanges returns graph nodes whose source ranges overlap any file
// range. Results are deterministic and de-duplicated by node ID.
func (qb *QueryBuilder) NodesForFileRanges(ranges []FileRange) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	results := make([]*parser.Node, 0)
	seen := make(map[string]bool)
	rangesByPath := make(map[string][]FileRange)
	pathCache := make(map[string]string, len(ranges))
	for _, r := range ranges {
		if r.FilePath == "" {
			continue
		}
		r.FilePath = comparableFilePathCached(r.FilePath, pathCache)
		rangesByPath[r.FilePath] = append(rangesByPath[r.FilePath], r)
	}
	if len(rangesByPath) == 0 {
		return nil, nil
	}

	for _, node := range qb.g.nodes {
		if node == nil || node.FilePath == "" {
			continue
		}
		if seen[node.ID] {
			continue
		}
		nodePath := comparableFilePathCached(node.FilePath, pathCache)
		for _, r := range rangesByPath[nodePath] {
			if !fileRangeMatchesNodeRepo(r, node) {
				continue
			}
			if !r.WholeFile && !nodeOverlapsFileRange(node, r) {
				continue
			}
			seen[node.ID] = true
			results = append(results, node)
			break
		}
	}
	sortNodesByID(results)
	return results, nil
}

func fileRangeMatchesNodeRepo(r FileRange, node *parser.Node) bool {
	return r.RepoID == "" || node.RepoID == "" || node.RepoID == r.RepoID
}

func comparableFilePathCached(path string, cache map[string]string) string {
	if cache != nil {
		if cached, ok := cache[path]; ok {
			return cached
		}
	}
	cleaned := cleanAbsPath(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		final := filepath.Clean(resolved)
		if cache != nil {
			cache[path] = final
		}
		return final
	}
	parent := filepath.Dir(cleaned)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		final := filepath.Join(resolvedParent, filepath.Base(cleaned))
		if cache != nil {
			cache[path] = final
		}
		return final
	}
	if cache != nil {
		cache[path] = cleaned
	}
	return cleaned
}

func nodeOverlapsFileRange(node *parser.Node, r FileRange) bool {
	if node == nil || node.Lines[0] <= 0 || node.Lines[1] <= 0 || node.Lines[1] < node.Lines[0] {
		return false
	}
	start := r.StartLine
	end := r.EndLine
	if start <= 0 || end <= 0 {
		return false
	}
	if end < start {
		start, end = end, start
	}
	return node.Lines[0] <= end && node.Lines[1] >= start
}

// GetDependencies returns all nodes connected by dependency edges from nodeID.
func (qb *QueryBuilder) GetDependencies(nodeID string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	id, err := qb.requireNodeIDLocked(nodeID)
	if err != nil {
		return nil, err
	}

	deps := make([]*parser.Node, 0, len(qb.g.edges[id]))
	for toID, edges := range qb.g.edges[id] {
		if !hasDependencyEdge(edges) {
			continue
		}
		if pNode, ok := qb.g.nodes[toID]; ok && pNode != nil {
			deps = append(deps, pNode)
		}
	}
	sortNodesByID(deps)
	return deps, nil
}

// GetBlastRadius returns all nodes that depend on the given node ID.
func (qb *QueryBuilder) GetBlastRadius(nodeID string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	id, err := qb.requireNodeIDLocked(nodeID)
	if err != nil {
		return nil, err
	}

	var blast []*parser.Node
	for fromID, outEdges := range qb.g.edges {
		edges, ok := outEdges[id]
		if !ok || !hasDependencyEdge(edges) {
			continue
		}
		if pNode, ok := qb.g.nodes[fromID]; ok && pNode != nil {
			blast = append(blast, pNode)
		}
	}
	sortNodesByID(blast)
	return blast, nil
}

// GetBlastRadiusDepth returns inbound dependency nodes up to maxDepth hops away.
// Source nodes are excluded from the result. maxNodes <= 0 means no cap.
func (qb *QueryBuilder) GetBlastRadiusDepth(nodeIDs []string, maxDepth int, maxNodes int) ([]NodeDistance, bool, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	if maxDepth <= 0 || len(nodeIDs) == 0 {
		return nil, false, nil
	}

	sourceIDs := make(map[int64]bool)
	queue := make([]NodeDistance, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		id, err := qb.requireNodeIDLocked(nodeID)
		if err != nil {
			return nil, false, err
		}
		if sourceIDs[id] {
			continue
		}
		sourceIDs[id] = true
		queue = append(queue, NodeDistance{Node: qb.g.nodes[id], Distance: 0})
	}

	seen := make(map[int64]int, len(sourceIDs))
	for id := range sourceIDs {
		seen[id] = 0
	}
	inboundDependencyIDs := qb.inboundDependencyNodeIDsByTargetLocked()
	resultsByID := make(map[int64]NodeDistance)
	truncated := false

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if current.Node == nil || current.Distance >= maxDepth {
			continue
		}
		currentID := qb.g.nodeMap[current.Node.ID]
		nextDistance := current.Distance + 1

		inboundIDs := inboundDependencyIDs[currentID]
		for _, fromID := range inboundIDs {
			if sourceIDs[fromID] || qb.g.nodes[fromID] == nil {
				continue
			}
			if prevDistance, ok := seen[fromID]; ok && prevDistance <= nextDistance {
				continue
			}
			if maxNodes > 0 && len(resultsByID) >= maxNodes {
				return sortedNodeDistances(resultsByID), true, nil
			}
			seen[fromID] = nextDistance
			nd := NodeDistance{Node: qb.g.nodes[fromID], Distance: nextDistance}
			resultsByID[fromID] = nd
			queue = append(queue, nd)
		}
	}

	return sortedNodeDistances(resultsByID), truncated, nil
}

func (qb *QueryBuilder) inboundDependencyNodeIDsByTargetLocked() map[int64][]int64 {
	inbound := make(map[int64][]int64)
	for fromID, outEdges := range qb.g.edges {
		for toID, edges := range outEdges {
			if !hasDependencyEdge(edges) {
				continue
			}
			inbound[toID] = append(inbound[toID], fromID)
		}
	}
	for targetID, ids := range inbound {
		sort.Slice(ids, func(i, j int) bool {
			left := qb.g.nodes[ids[i]]
			right := qb.g.nodes[ids[j]]
			if left == nil || right == nil {
				return ids[i] < ids[j]
			}
			return left.ID < right.ID
		})
		inbound[targetID] = ids
	}
	return inbound
}

func sortedNodeDistances(resultsByID map[int64]NodeDistance) []NodeDistance {
	results := make([]NodeDistance, 0, len(resultsByID))
	for _, result := range resultsByID {
		if result.Node != nil {
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Distance != results[j].Distance {
			return results[i].Distance < results[j].Distance
		}
		return results[i].Node.ID < results[j].Node.ID
	})
	return results
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

	id, err := qb.requireNodeIDLocked(nodeID)
	if err != nil {
		return nil, err
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
			isGoroutineCall := edge.Type == parser.EdgeTypeCalls && pNode.Type == parser.NodeTypeGoroutine
			if !isConcurrencyEdge(edge.Type) && !isGoroutineCall {
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

	ifaceNumID, err := qb.requireNodeIDLocked(interfaceID)
	if err != nil {
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
				if pNode, ok := qb.g.nodes[fromNumID]; ok && pNode != nil {
					impls = append(impls, pNode)
				}
				break
			}
		}
	}
	sortNodesByID(impls)
	return impls, nil
}

// GetSourceCode reads the source code of the given node from disk.
func (qb *QueryBuilder) GetSourceCode(nodeID string) (code string, err error) {
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

	if lines[0] <= 0 || lines[1] <= 0 {
		return "", fmt.Errorf("node %s has no line range info", nodeID)
	}
	if lines[1] < lines[0] {
		return "", fmt.Errorf("node %s has invalid line range: %d-%d", nodeID, lines[0], lines[1])
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close file %s: %w", filePath, closeErr)
		}
	}()

	var b strings.Builder
	reader := bufio.NewReader(f)
	lineNum := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return "", fmt.Errorf("error reading file %s: %w", filePath, readErr)
		}
		if readErr == io.EOF && line == "" {
			break
		}

		lineNum++
		if lineNum >= lines[0] && lineNum <= lines[1] {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		if lineNum > lines[1] {
			break
		}
		if readErr == io.EOF {
			break
		}
	}

	return b.String(), nil
}

// PathResult represents a node in the shortest path with edge info.
type PathResult struct {
	Node     *parser.Node `json:"node"`
	EdgeType string       `json:"edge_type,omitempty"` // edge connecting this node to the next
}

// FindPath finds the shortest call path between two nodes using BFS.
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
				if edges, ok := edgeMap[nextNumID]; ok {
					pr.EdgeType = pathEdgeType(edges)
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
		for nextID, edges := range qb.g.edges[currentID] {
			if !hasCallEdge(edges) {
				continue
			}
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

func (qb *QueryBuilder) requireNodeIDLocked(nodeID string) (int64, error) {
	id, exists := qb.g.nodeMap[nodeID]
	if !exists || qb.g.nodes[id] == nil {
		return 0, fmt.Errorf("node not found: %s", nodeID)
	}
	return id, nil
}

func hasDependencyEdge(edges []*parser.Edge) bool {
	for _, edge := range edges {
		if edge != nil && isDependencyEdge(edge.Type) {
			return true
		}
	}
	return false
}

func hasCallEdge(edges []*parser.Edge) bool {
	for _, edge := range edges {
		if edge != nil && edge.Type == parser.EdgeTypeCalls {
			return true
		}
	}
	return false
}

func pathEdgeType(edges []*parser.Edge) string {
	for _, edge := range edges {
		if edge != nil && edge.Type == parser.EdgeTypeCalls {
			return string(parser.EdgeTypeCalls)
		}
	}
	return ""
}

func isDependencyEdge(edgeType parser.EdgeType) bool {
	switch edgeType {
	case parser.EdgeTypeCalls, parser.EdgeTypeImports, parser.EdgeTypeReferences, parser.EdgeTypeInstantiates:
		return true
	default:
		return false
	}
}

func sortNodesByID(nodes []*parser.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})
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
		if pNode == nil {
			continue
		}
		if containsCaseInsensitive(pNode.Name, lowerQuery, asciiQuery) || containsCaseInsensitive(pNode.ID, lowerQuery, asciiQuery) {
			results = appendBoundedNodeResult(results, pNode, maxSearchResults)
		}
	}

	return results, nil
}

func appendBoundedNodeResult(results []*parser.Node, node *parser.Node, limit int) []*parser.Node {
	if node == nil || limit <= 0 {
		return results
	}

	insertAt := sort.Search(len(results), func(i int) bool {
		return results[i].ID >= node.ID
	})
	if insertAt < len(results) && results[insertAt].ID == node.ID {
		return results
	}
	if len(results) == limit && insertAt >= limit {
		return results
	}

	results = append(results, nil)
	copy(results[insertAt+1:], results[insertAt:])
	results[insertAt] = node
	if len(results) > limit {
		results = results[:limit]
	}
	return results
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
