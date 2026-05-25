package graph

import (
	"fmt"

	"github.com/hungpdn/gokg/internal/parser"
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
