package graph

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/hungpdn/gokg/internal/parser"
	"gonum.org/v1/gonum/graph/path"
	"gonum.org/v1/gonum/graph/simple"
)

// QueryBuilder provides an interface to search the graph.
type QueryBuilder struct {
	g *Graph
}

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

	var deps []*parser.Node
	nodes := qb.g.directed.From(id)
	for nodes.Next() {
		toID := nodes.Node().ID()
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
	nodes := qb.g.directed.To(id)
	for nodes.Next() {
		fromID := nodes.Node().ID()
		if pNode, ok := qb.g.nodes[fromID]; ok {
			blast = append(blast, pNode)
		}
	}
	return blast, nil
}

// GetConcurrencyFlow returns goroutines and channels connected to this node.
func (qb *QueryBuilder) GetConcurrencyFlow(nodeID string) ([]*parser.Node, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	id, exists := qb.g.nodeMap[nodeID]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}

	var flows []*parser.Node
	nodes := qb.g.directed.From(id)
	for nodes.Next() {
		toID := nodes.Node().ID()
		edge := qb.g.edges[id][toID]
		if edge != nil && (edge.Type == parser.EdgeTypeSpawns || edge.Type == parser.EdgeTypeSendsTo || edge.Type == parser.EdgeTypeReceivesFrom) {
			if pNode, ok := qb.g.nodes[toID]; ok {
				flows = append(flows, pNode)
			}
		}
	}
	return flows, nil
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
	// Walk all nodes pointing TO the interface via IMPLEMENTS edges
	inbound := qb.g.directed.To(ifaceNumID)
	for inbound.Next() {
		fromNumID := inbound.Node().ID()
		edge := qb.g.edges[fromNumID][ifaceNumID]
		if edge != nil && edge.Type == parser.EdgeTypeImplements {
			if pNode, ok := qb.g.nodes[fromNumID]; ok {
				impls = append(impls, pNode)
			}
		}
	}
	return impls, nil
}

// GetSourceCode reads the source code of the given node from disk.
func (qb *QueryBuilder) GetSourceCode(nodeID string) (string, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	numID, exists := qb.g.nodeMap[nodeID]
	if !exists {
		return "", fmt.Errorf("node not found: %s", nodeID)
	}

	pNode := qb.g.nodes[numID]
	if pNode == nil {
		return "", fmt.Errorf("node data missing: %s", nodeID)
	}

	if pNode.FilePath == "" {
		return "", fmt.Errorf("node %s has no file path", nodeID)
	}

	if pNode.Lines[0] == 0 && pNode.Lines[1] == 0 {
		return "", fmt.Errorf("node %s has no line range info", nodeID)
	}

	f, err := os.Open(pNode.FilePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", pNode.FilePath, err)
	}
	defer f.Close()

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= pNode.Lines[0] && lineNum <= pNode.Lines[1] {
			b.WriteString(scanner.Text())
			b.WriteByte('\n')
		}
		if lineNum > pNode.Lines[1] {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file %s: %w", pNode.FilePath, err)
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

	// Use gonum's shortest path (BFS with uniform weight = shortest hop count)
	shortest := path.DijkstraFrom(simple.Node(srcNumID), qb.g.directed)
	pathNodes, _ := shortest.To(tgtNumID)

	if len(pathNodes) == 0 {
		return nil, fmt.Errorf("no path found from %s to %s", sourceID, targetID)
	}

	var results []PathResult
	for i, gNode := range pathNodes {
		numID := gNode.ID()
		pNode := qb.g.nodes[numID]
		if pNode == nil {
			continue
		}

		pr := PathResult{Node: pNode}

		// Add the edge type connecting this node to the next
		if i < len(pathNodes)-1 {
			nextNumID := pathNodes[i+1].ID()
			if edgeMap, ok := qb.g.edges[numID]; ok {
				if edge, ok := edgeMap[nextNumID]; ok {
					pr.EdgeType = string(edge.Type)
				}
			}
		}

		results = append(results, pr)
	}

	return results, nil
}
