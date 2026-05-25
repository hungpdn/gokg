package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"

	"gonum.org/v1/gonum/graph/simple"
)

type Graph struct {
	mu         sync.RWMutex
	directed   *simple.DirectedGraph
	nodeMap    map[string]int64
	nodes      map[int64]*parser.Node
	edges      map[int64]map[int64]*parser.Edge
	store      storage.Storage
	nextNodeID int64
}

// NewGraph creates a new Graph instance with an optional storage backend.
func NewGraph(store storage.Storage) *Graph {
	return &Graph{
		directed:   simple.NewDirectedGraph(),
		nodeMap:    make(map[string]int64),
		nodes:      make(map[int64]*parser.Node),
		edges:      make(map[int64]map[int64]*parser.Edge),
		store:      store,
		nextNodeID: 0,
	}
}

// AddNode adds a parser node to the graph and persists it.
func (g *Graph) AddNode(ctx context.Context, pNode *parser.Node) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id, exists := g.nodeMap[pNode.ID]; exists {
		return id, nil // Already exists
	}

	g.nextNodeID++
	id := g.nextNodeID

	g.nodeMap[pNode.ID] = id
	g.nodes[id] = pNode

	// Add to gonum graph
	gNode := simple.Node(id)
	g.directed.AddNode(gNode)

	// Persist to storage
	if g.store != nil {
		data, err := json.Marshal(pNode)
		if err == nil {
			_ = g.store.Put(ctx, []byte("node:"+pNode.ID), data)
		}
	}

	return id, nil
}

// AddEdge adds a parser edge to the graph.
func (g *Graph) AddEdge(ctx context.Context, pEdge *parser.Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	fromID, fromExists := g.nodeMap[pEdge.From]
	toID, toExists := g.nodeMap[pEdge.To]

	if !fromExists || !toExists {
		return fmt.Errorf("edge references unknown nodes: %s -> %s", pEdge.From, pEdge.To)
	}

	if g.edges[fromID] == nil {
		g.edges[fromID] = make(map[int64]*parser.Edge)
	}
	g.edges[fromID][toID] = pEdge

	gEdge := simple.Edge{F: simple.Node(fromID), T: simple.Node(toID)}
	g.directed.SetEdge(gEdge)

	// Persist to storage
	if g.store != nil {
		data, err := json.Marshal(pEdge)
		if err == nil {
			key := fmt.Sprintf("edge:%s:%s:%s", pEdge.From, pEdge.To, pEdge.Type)
			_ = g.store.Put(ctx, []byte(key), data)
		}
	}

	return nil
}

// BuildFromParseResult builds the graph from the parse result
func (g *Graph) BuildFromParseResult(ctx context.Context, result *parser.ParseResult) error {
	for _, node := range result.Nodes {
		if _, err := g.AddNode(ctx, node); err != nil {
			return err
		}
	}

	for _, edge := range result.Edges {
		if err := g.AddEdge(ctx, edge); err != nil {
			// Some edges might point to unresolved boundary nodes, just ignore or log
			continue
		}
	}

	return nil
}
