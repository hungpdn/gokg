package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	edges      map[int64]map[int64][]*parser.Edge
	store      storage.Storage
	nextNodeID int64
}

// NewGraph creates a new Graph instance with an optional storage backend.
func NewGraph(store storage.Storage) *Graph {
	return &Graph{
		directed:   simple.NewDirectedGraph(),
		nodeMap:    make(map[string]int64),
		nodes:      make(map[int64]*parser.Node),
		edges:      make(map[int64]map[int64][]*parser.Edge),
		store:      store,
		nextNodeID: 0,
	}
}

// AddNode adds a parser node to the graph and persists it.
func (g *Graph) AddNode(ctx context.Context, pNode *parser.Node) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id, exists := g.nodeMap[pNode.ID]; exists {
		if g.nodes[id] == nil {
			g.nodes[id] = pNode
			gNode := simple.Node(id)
			g.directed.AddNode(gNode)
			g.persistNode(ctx, pNode)
			g.restoreInboundEdges(id)
		} else if shouldReplaceNode(g.nodes[id], pNode) {
			g.nodes[id] = pNode
			g.persistNode(ctx, pNode)
		}
		return id, nil // Already exists
	}

	g.nextNodeID++
	id := g.nextNodeID

	g.nodeMap[pNode.ID] = id
	g.nodes[id] = pNode

	// Add to gonum graph
	gNode := simple.Node(id)
	g.directed.AddNode(gNode)

	g.persistNode(ctx, pNode)
	g.restoreInboundEdges(id)

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
		g.edges[fromID] = make(map[int64][]*parser.Edge)
	}
	for _, edge := range g.edges[fromID][toID] {
		if edge.Type == pEdge.Type {
			return nil
		}
	}
	g.edges[fromID][toID] = append(g.edges[fromID][toID], pEdge)

	gEdge := simple.Edge{F: simple.Node(fromID), T: simple.Node(toID)}
	g.directed.SetEdge(gEdge)

	g.persistEdge(ctx, pEdge)

	return nil
}

// BuildFromParseResult builds the graph from the parse result
func (g *Graph) BuildFromParseResult(ctx context.Context, result *parser.ParseResult) error {
	return g.BuildFromParseResults(ctx, result)
}

// BuildFromParseResults merges one or more parse results into the graph.
func (g *Graph) BuildFromParseResults(ctx context.Context, results ...*parser.ParseResult) error {
	for _, result := range results {
		if result == nil {
			continue
		}
		for _, node := range result.Nodes {
			if _, err := g.AddNode(ctx, node); err != nil {
				return err
			}
		}
	}

	for _, result := range results {
		if result == nil {
			continue
		}
		for _, edge := range result.Edges {
			if err := g.AddEdge(ctx, edge); err != nil {
				// Keep unresolved edges in storage so multi-DB loads can resolve
				// them after all repos contribute their nodes.
				g.persistEdge(ctx, edge)
				continue
			}
		}
	}

	return nil
}

func (g *Graph) persistNode(ctx context.Context, pNode *parser.Node) {
	if g.store == nil {
		return
	}
	data, err := json.Marshal(pNode)
	if err == nil {
		_ = g.store.Put(ctx, []byte("node:"+pNode.ID), data)
	}
}

func (g *Graph) persistEdge(ctx context.Context, pEdge *parser.Edge) {
	if g.store == nil {
		return
	}
	data, err := json.Marshal(pEdge)
	if err == nil {
		key := fmt.Sprintf("edge:%s:%s:%s", pEdge.From, pEdge.To, pEdge.Type)
		_ = g.store.Put(ctx, []byte(key), data)
	}
}

func shouldReplaceNode(existing, candidate *parser.Node) bool {
	if existing == nil || candidate == nil {
		return false
	}
	return existing.Type == parser.NodeTypeBoundary && candidate.Type != parser.NodeTypeBoundary
}

// LoadFromStorage reads the graph from the local storage.
func (g *Graph) LoadFromStorage(ctx context.Context) error {
	if g.store == nil {
		return fmt.Errorf("no storage backend available")
	}
	return g.LoadFromStorages(ctx, g.store)
}

// LoadFromStorages reads and merges graph data from multiple storage backends.
func (g *Graph) LoadFromStorages(ctx context.Context, stores ...storage.Storage) error {
	if len(stores) == 0 {
		return fmt.Errorf("no storage backends available")
	}

	for _, store := range stores {
		if store == nil {
			return fmt.Errorf("nil storage backend")
		}
	}

	var edgesData [][]byte

	// Temporarily unset g.store so AddNode and AddEdge don't write back to DB
	store := g.store
	g.store = nil
	defer func() { g.store = store }()

	for _, store := range stores {
		err := store.Iterate(ctx, func(key []byte, value []byte) error {
			keyStr := string(key)
			if strings.HasPrefix(keyStr, "node:") {
				var pNode parser.Node
				if err := json.Unmarshal(value, &pNode); err != nil {
					return err
				}
				_, _ = g.AddNode(ctx, &pNode)
			} else if strings.HasPrefix(keyStr, "edge:") {
				// Copy the value as Badger reuses the slice
				edgesData = append(edgesData, append([]byte(nil), value...))
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to iterate storage: %w", err)
		}
	}

	for _, data := range edgesData {
		var pEdge parser.Edge
		if err := json.Unmarshal(data, &pEdge); err != nil {
			return err
		}
		_ = g.AddEdge(ctx, &pEdge)
	}

	return nil
}

// RemovePackage removes all nodes and edges belonging to the given package path.
func (g *Graph) RemovePackage(ctx context.Context, pkgPath string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	var nodesToRemove []int64
	for id, node := range g.nodes {
		if node.PkgPath == pkgPath {
			nodesToRemove = append(nodesToRemove, id)
		}
	}

	for _, id := range nodesToRemove {
		node := g.nodes[id]

		// Remove from Gonum graph
		g.directed.RemoveNode(id)

		// Remove from internal maps (keep in nodeMap to maintain ID stability)
		delete(g.nodes, id)

		// Remove from BadgerDB
		if g.store != nil {
			_ = g.store.Delete(ctx, []byte("node:"+node.ID))
		}

		// Remove all outbound edges from this node
		if outEdges, ok := g.edges[id]; ok {
			for _, edges := range outEdges {
				for _, edge := range edges {
					if g.store != nil {
						key := fmt.Sprintf("edge:%s:%s:%s", edge.From, edge.To, edge.Type)
						_ = g.store.Delete(ctx, []byte(key))
					}
				}
			}
			delete(g.edges, id)
		}
	}

	return nil
}

// restoreInboundEdges restores all edges from other packages/nodes pointing to the given node.
func (g *Graph) restoreInboundEdges(id int64) {
	if g.nodes[id] == nil {
		return
	}
	for fromID, outEdges := range g.edges {
		if g.nodes[fromID] == nil {
			continue
		}
		if _, exists := outEdges[id]; exists {
			gEdge := simple.Edge{F: simple.Node(fromID), T: simple.Node(id)}
			g.directed.SetEdge(gEdge)
		}
	}
}
