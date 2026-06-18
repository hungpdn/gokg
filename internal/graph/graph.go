package graph

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"

	"gonum.org/v1/gonum/graph/simple"
)

var ErrUnknownEdgeEndpoint = errors.New("edge references unknown nodes")

type Graph struct {
	mu         sync.RWMutex
	directed   *simple.DirectedGraph
	nodeMap    map[string]int64
	nodes      map[int64]*parser.Node
	edges      map[int64]map[int64][]*parser.Edge
	store      storage.Storage
	repoStores map[string]storage.Storage
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
		repoStores: make(map[string]storage.Storage),
		nextNodeID: 0,
	}
}

// SetRepoStore registers a storage backend for a specific repository. Repo
// stores are used in workspace mode so one in-memory graph can persist updates
// back to the correct per-repo database.
func (g *Graph) SetRepoStore(repoID string, store storage.Storage) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if store == nil {
		delete(g.repoStores, repoID)
		return
	}
	g.repoStores[repoID] = store
}

// SetStore replaces the default storage backend. It is used by long-running
// servers that load once, close the DB while idle, and reopen it only for
// incremental persistence.
func (g *Graph) SetStore(store storage.Storage) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.store = store
}

// AddNode adds a parser node to the graph and persists it.
func (g *Graph) AddNode(ctx context.Context, pNode *parser.Node) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if id, exists := g.nodeMap[pNode.ID]; exists {
		if g.nodes[id] == nil {
			if err := g.persistNode(ctx, pNode); err != nil {
				return id, err
			}
			g.nodes[id] = pNode
			gNode := simple.Node(id)
			g.directed.AddNode(gNode)
			g.restoreInboundEdges(id)
		} else if shouldReplaceNode(g.nodes[id], pNode) {
			if err := g.persistNode(ctx, pNode); err != nil {
				return id, err
			}
			g.nodes[id] = pNode
		}
		return id, nil // Already exists
	}

	if err := g.persistNode(ctx, pNode); err != nil {
		return 0, err
	}

	g.nextNodeID++
	id := g.nextNodeID

	g.nodeMap[pNode.ID] = id
	g.nodes[id] = pNode

	// Add to gonum graph
	gNode := simple.Node(id)
	g.directed.AddNode(gNode)

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
		return fmt.Errorf("%w: %s -> %s", ErrUnknownEdgeEndpoint, pEdge.From, pEdge.To)
	}

	if g.edges[fromID] == nil {
		g.edges[fromID] = make(map[int64][]*parser.Edge)
	}
	for _, edge := range g.edges[fromID][toID] {
		if edge.Type == pEdge.Type {
			if mergeEdgeOccurrences(edge, pEdge) {
				if err := g.persistEdge(ctx, edge); err != nil {
					return err
				}
			}
			return nil
		}
	}

	if err := g.persistEdge(ctx, pEdge); err != nil {
		return err
	}

	g.edges[fromID][toID] = append(g.edges[fromID][toID], pEdge)

	// Keep self-edges in the semantic graph/export, but do not add them to
	// gonum's simple.DirectedGraph because it panics on self-loops.
	if fromID != toID {
		gEdge := simple.Edge{F: simple.Node(fromID), T: simple.Node(toID)}
		g.directed.SetEdge(gEdge)
	}

	return nil
}

func mergeEdgeOccurrences(existing, candidate *parser.Edge) bool {
	if existing == nil || candidate == nil || len(candidate.Occurrences) == 0 {
		return false
	}

	changed := false
	for _, occurrence := range candidate.Occurrences {
		if hasEdgeOccurrence(existing.Occurrences, occurrence) {
			continue
		}
		existing.Occurrences = append(existing.Occurrences, occurrence)
		changed = true
	}
	return changed
}

func hasEdgeOccurrence(occurrences []parser.EdgeOccurrence, candidate parser.EdgeOccurrence) bool {
	for _, occurrence := range occurrences {
		if occurrence == candidate {
			return true
		}
	}
	return false
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
				if !errors.Is(err, ErrUnknownEdgeEndpoint) {
					return err
				}
				// Keep unresolved edges in storage so multi-DB loads can resolve
				// them after all repos contribute their nodes.
				if persistErr := g.persistEdge(ctx, edge); persistErr != nil {
					return persistErr
				}
				continue
			}
		}
	}

	return nil
}

func (g *Graph) persistNode(ctx context.Context, pNode *parser.Node) error {
	store := g.storageForNode(pNode)
	if store == nil {
		return nil
	}
	data, err := json.Marshal(pNode)
	if err != nil {
		return fmt.Errorf("marshal node %q: %w", pNode.ID, err)
	}
	if err := store.Put(ctx, []byte("node:"+pNode.ID), data); err != nil {
		return fmt.Errorf("persist node %q: %w", pNode.ID, err)
	}
	return nil
}

func (g *Graph) persistEdge(ctx context.Context, pEdge *parser.Edge) error {
	store := g.storageForEdge(pEdge)
	if store == nil {
		return nil
	}
	data, err := json.Marshal(pEdge)
	if err != nil {
		return fmt.Errorf("marshal edge %q -> %q: %w", pEdge.From, pEdge.To, err)
	}
	if err := store.Put(ctx, edgeStorageKey(pEdge), data); err != nil {
		return fmt.Errorf("persist edge %q -> %q (%s): %w", pEdge.From, pEdge.To, pEdge.Type, err)
	}
	return nil
}

func (g *Graph) storageForNode(pNode *parser.Node) storage.Storage {
	if pNode != nil && pNode.RepoID != "" {
		if store := g.repoStores[pNode.RepoID]; store != nil {
			return store
		}
	}
	return g.store
}

func (g *Graph) storageForEdge(pEdge *parser.Edge) storage.Storage {
	if pEdge != nil && pEdge.RepoID != "" {
		if store := g.repoStores[pEdge.RepoID]; store != nil {
			return store
		}
	}
	return g.store
}

func edgeStorageKey(edge *parser.Edge) []byte {
	parts := [3]string{edge.From, edge.To, string(edge.Type)}
	data, err := json.Marshal(parts)
	if err != nil {
		return legacyEdgeStorageKey(edge)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return []byte("edge:v2:" + encoded)
}

func legacyEdgeStorageKey(edge *parser.Edge) []byte {
	return []byte(fmt.Sprintf("edge:%s:%s:%s", edge.From, edge.To, edge.Type))
}

func edgeStorageDeleteKeys(edge *parser.Edge) [][]byte {
	return [][]byte{edgeStorageKey(edge), legacyEdgeStorageKey(edge)}
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

	// Temporarily unset stores so AddNode and AddEdge don't write back to DB
	store := g.store
	repoStores := g.repoStores
	g.store = nil
	g.repoStores = nil
	defer func() {
		g.store = store
		g.repoStores = repoStores
	}()

	// First pass: load all nodes
	for _, store := range stores {
		err := store.Iterate(ctx, func(key []byte, value []byte) error {
			keyStr := string(key)
			if strings.HasPrefix(keyStr, "node:") {
				var pNode parser.Node
				if err := json.Unmarshal(value, &pNode); err != nil {
					return err
				}
				_, _ = g.AddNode(ctx, &pNode)
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to iterate storage for nodes: %w", err)
		}
	}

	// Second pass: load all edges
	for _, store := range stores {
		err := store.Iterate(ctx, func(key []byte, value []byte) error {
			keyStr := string(key)
			if strings.HasPrefix(keyStr, "edge:") {
				var pEdge parser.Edge
				if err := json.Unmarshal(value, &pEdge); err != nil {
					return err
				}
				_ = g.AddEdge(ctx, &pEdge)
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to iterate storage for edges: %w", err)
		}
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
		if outEdges, ok := g.edges[id]; ok {
			for _, edges := range outEdges {
				for _, edge := range edges {
					if store := g.storageForEdge(edge); store != nil {
						for _, key := range edgeStorageDeleteKeys(edge) {
							if err := store.Delete(ctx, key); err != nil {
								return fmt.Errorf("delete edge %q -> %q (%s): %w", edge.From, edge.To, edge.Type, err)
							}
						}
					}
				}
			}
		}

		node := g.nodes[id]
		if store := g.storageForNode(node); store != nil {
			if err := store.Delete(ctx, []byte("node:"+node.ID)); err != nil {
				return fmt.Errorf("delete node %q: %w", node.ID, err)
			}
		}
	}

	for _, id := range nodesToRemove {
		g.directed.RemoveNode(id)
		delete(g.nodes, id)
		delete(g.edges, id)
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
		if fromID == id {
			continue
		}
		if _, exists := outEdges[id]; exists {
			gEdge := simple.Edge{F: simple.Node(fromID), T: simple.Node(id)}
			g.directed.SetEdge(gEdge)
		}
	}
}
