package graph

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/hungpdn/gokg/internal/parser"
	"github.com/hungpdn/gokg/internal/storage"
)

var ErrUnknownEdgeEndpoint = errors.New("edge references unknown nodes")

const maxPersistBatchEntries = 1024

type Graph struct {
	mu         sync.RWMutex
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

	return nil
}

func (g *Graph) loadNodeLocked(pNode *parser.Node) {
	if pNode == nil {
		return
	}

	if id, exists := g.nodeMap[pNode.ID]; exists {
		if g.nodes[id] == nil || shouldReplaceNode(g.nodes[id], pNode) {
			g.nodes[id] = pNode
		}
		return
	}

	g.nextNodeID++
	id := g.nextNodeID
	g.nodeMap[pNode.ID] = id
	g.nodes[id] = pNode
}

func (g *Graph) loadEdgeLocked(pEdge *parser.Edge) error {
	if pEdge == nil {
		return nil
	}

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
			mergeEdgeOccurrences(edge, pEdge)
			return nil
		}
	}

	g.edges[fromID][toID] = append(g.edges[fromID][toID], pEdge)
	return nil
}

func mergeEdgeOccurrences(existing, candidate *parser.Edge) bool {
	occurrences, changed := edgeOccurrencesAfterMerge(existing, candidate)
	if changed {
		existing.Occurrences = occurrences
	}
	return changed
}

func edgeOccurrencesAfterMerge(existing, candidate *parser.Edge) ([]parser.EdgeOccurrence, bool) {
	if existing == nil || candidate == nil || len(candidate.Occurrences) == 0 {
		return nil, false
	}

	occurrences := existing.Occurrences
	changed := false
	for _, occurrence := range candidate.Occurrences {
		if hasEdgeOccurrence(occurrences, occurrence) {
			continue
		}
		if !changed {
			occurrences = append([]parser.EdgeOccurrence(nil), existing.Occurrences...)
			changed = true
		}
		occurrences = append(occurrences, occurrence)
	}
	return occurrences, changed
}

func hasEdgeOccurrence(occurrences []parser.EdgeOccurrence, candidate parser.EdgeOccurrence) bool {
	for _, occurrence := range occurrences {
		if occurrence == candidate {
			return true
		}
	}
	return false
}

func cloneEdge(edge *parser.Edge) *parser.Edge {
	if edge == nil {
		return nil
	}
	cloned := *edge
	if len(edge.Occurrences) > 0 {
		cloned.Occurrences = append([]parser.EdgeOccurrence(nil), edge.Occurrences...)
	}
	return &cloned
}

// BuildFromParseResult builds the graph from the parse result
func (g *Graph) BuildFromParseResult(ctx context.Context, result *parser.ParseResult) error {
	return g.BuildFromParseResults(ctx, result)
}

// BuildFromParseResults merges one or more parse results into the graph.
func (g *Graph) BuildFromParseResults(ctx context.Context, results ...*parser.ParseResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	nodeStages, nextNodeID, err := g.stageNodes(ctx, results...)
	if err != nil {
		return err
	}
	if err := g.persistStagedNodes(ctx, nodeStages); err != nil {
		return err
	}
	g.applyStagedNodes(nodeStages, nextNodeID)

	edgeStages, unresolvedEdges, err := g.stageEdges(ctx, results...)
	if err != nil {
		return err
	}
	if err := g.persistStagedEdges(ctx, edgeStages, unresolvedEdges); err != nil {
		return err
	}
	g.applyStagedEdges(edgeStages)

	return nil
}

type stagedNodeUpsert struct {
	id       int64
	node     *parser.Node
	addToMap bool
}

type graphEdgeKey struct {
	fromID int64
	toID   int64
	typ    parser.EdgeType
}

type stagedGraphEdge struct {
	fromID   int64
	toID     int64
	edge     *parser.Edge
	existing *parser.Edge
	isNew    bool
	changed  bool
}

func (g *Graph) stageNodes(ctx context.Context, results ...*parser.ParseResult) (map[string]*stagedNodeUpsert, int64, error) {
	stages := make(map[string]*stagedNodeUpsert)
	nextNodeID := g.nextNodeID

	for _, result := range results {
		if result == nil {
			continue
		}
		for _, node := range result.Nodes {
			if err := ctx.Err(); err != nil {
				return nil, nextNodeID, err
			}
			if node == nil {
				continue
			}

			if staged, ok := stages[node.ID]; ok {
				if shouldReplaceNode(staged.node, node) {
					staged.node = node
				}
				continue
			}

			if id, exists := g.nodeMap[node.ID]; exists {
				if g.nodes[id] == nil || shouldReplaceNode(g.nodes[id], node) {
					stages[node.ID] = &stagedNodeUpsert{id: id, node: node}
				}
				continue
			}

			nextNodeID++
			stages[node.ID] = &stagedNodeUpsert{id: nextNodeID, node: node, addToMap: true}
		}
	}

	return stages, nextNodeID, nil
}

func (g *Graph) persistStagedNodes(ctx context.Context, stages map[string]*stagedNodeUpsert) error {
	entries := newStorageEntryBuffer(ctx)
	for _, stage := range stages {
		entry, err := g.nodeStorageEntry(stage.node)
		if err != nil {
			return err
		}
		if err := entries.Add(g.storageForNode(stage.node), entry); err != nil {
			return fmt.Errorf("persist graph nodes: %w", err)
		}
	}
	if err := entries.Flush(); err != nil {
		return fmt.Errorf("persist graph nodes: %w", err)
	}
	return nil
}

func (g *Graph) applyStagedNodes(stages map[string]*stagedNodeUpsert, nextNodeID int64) {
	for _, stage := range stages {
		if stage.addToMap {
			g.nodeMap[stage.node.ID] = stage.id
		}
		g.nodes[stage.id] = stage.node
	}
	g.nextNodeID = nextNodeID
}

func (g *Graph) stageEdges(ctx context.Context, results ...*parser.ParseResult) (map[graphEdgeKey]*stagedGraphEdge, []*parser.Edge, error) {
	stages := make(map[graphEdgeKey]*stagedGraphEdge)
	var unresolved []*parser.Edge

	for _, result := range results {
		if result == nil {
			continue
		}
		for _, edge := range result.Edges {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if edge == nil {
				continue
			}
			fromID, fromExists := g.nodeMap[edge.From]
			toID, toExists := g.nodeMap[edge.To]
			if !fromExists || !toExists {
				unresolved = append(unresolved, edge)
				continue
			}

			key := graphEdgeKey{fromID: fromID, toID: toID, typ: edge.Type}
			if staged, ok := stages[key]; ok {
				if occurrences, changed := edgeOccurrencesAfterMerge(staged.edge, edge); changed {
					staged.edge.Occurrences = occurrences
					staged.changed = true
				}
				continue
			}

			existing := g.findEdgeByType(fromID, toID, edge.Type)
			if existing != nil {
				staged := &stagedGraphEdge{
					fromID:   fromID,
					toID:     toID,
					edge:     cloneEdge(existing),
					existing: existing,
				}
				if occurrences, changed := edgeOccurrencesAfterMerge(staged.edge, edge); changed {
					staged.edge.Occurrences = occurrences
					staged.changed = true
				}
				stages[key] = staged
				continue
			}

			stages[key] = &stagedGraphEdge{
				fromID:  fromID,
				toID:    toID,
				edge:    cloneEdge(edge),
				isNew:   true,
				changed: true,
			}
		}
	}

	return stages, unresolved, nil
}

func (g *Graph) findEdgeByType(fromID int64, toID int64, edgeType parser.EdgeType) *parser.Edge {
	for _, edge := range g.edges[fromID][toID] {
		if edge != nil && edge.Type == edgeType {
			return edge
		}
	}
	return nil
}

func (g *Graph) persistStagedEdges(ctx context.Context, stages map[graphEdgeKey]*stagedGraphEdge, unresolved []*parser.Edge) error {
	entries := newStorageEntryBuffer(ctx)

	for _, edge := range unresolved {
		entry, err := g.edgeStorageEntry(edge)
		if err != nil {
			return err
		}
		if err := entries.Add(g.storageForEdge(edge), entry); err != nil {
			return fmt.Errorf("persist graph edges: %w", err)
		}
	}

	for _, stage := range stages {
		if !stage.isNew && !stage.changed {
			continue
		}
		entry, err := g.edgeStorageEntry(stage.edge)
		if err != nil {
			return err
		}
		if err := entries.Add(g.storageForEdge(stage.edge), entry); err != nil {
			return fmt.Errorf("persist graph edges: %w", err)
		}
	}

	if err := entries.Flush(); err != nil {
		return fmt.Errorf("persist graph edges: %w", err)
	}
	return nil
}

func (g *Graph) applyStagedEdges(stages map[graphEdgeKey]*stagedGraphEdge) {
	for _, stage := range stages {
		if stage.existing != nil {
			if stage.changed {
				stage.existing.Occurrences = stage.edge.Occurrences
			}
			continue
		}
		if g.edges[stage.fromID] == nil {
			g.edges[stage.fromID] = make(map[int64][]*parser.Edge)
		}
		g.edges[stage.fromID][stage.toID] = append(g.edges[stage.fromID][stage.toID], stage.edge)
	}
}

type storageEntryBuffer struct {
	ctx     context.Context
	limit   int
	entries map[storage.Storage][]storage.Entry
}

func newStorageEntryBuffer(ctx context.Context) *storageEntryBuffer {
	return &storageEntryBuffer{
		ctx:     ctx,
		limit:   maxPersistBatchEntries,
		entries: make(map[storage.Storage][]storage.Entry),
	}
}

func (b *storageEntryBuffer) Add(store storage.Storage, entry storage.Entry) error {
	if store == nil {
		return nil
	}
	entries := append(b.entries[store], entry)
	b.entries[store] = entries
	if len(entries) >= b.limit {
		return b.flushStore(store, entries)
	}
	return nil
}

func (b *storageEntryBuffer) Flush() error {
	for store, entries := range b.entries {
		if err := b.flushStore(store, entries); err != nil {
			return err
		}
	}
	return nil
}

func (b *storageEntryBuffer) flushStore(store storage.Storage, entries []storage.Entry) error {
	if len(entries) == 0 {
		delete(b.entries, store)
		return nil
	}
	if batchStore, ok := store.(storage.BatchPutter); ok {
		if err := batchStore.PutBatch(b.ctx, entries); err != nil {
			return err
		}
		delete(b.entries, store)
		return nil
	}
	for _, entry := range entries {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		if err := store.Put(b.ctx, entry.Key, entry.Value); err != nil {
			return err
		}
	}
	delete(b.entries, store)
	return nil
}

func (g *Graph) persistNode(ctx context.Context, pNode *parser.Node) error {
	store := g.storageForNode(pNode)
	if store == nil {
		return nil
	}
	entry, err := g.nodeStorageEntry(pNode)
	if err != nil {
		return err
	}
	if err := store.Put(ctx, entry.Key, entry.Value); err != nil {
		return fmt.Errorf("persist node %q: %w", pNode.ID, err)
	}
	return nil
}

func (g *Graph) nodeStorageEntry(pNode *parser.Node) (storage.Entry, error) {
	data, err := json.Marshal(pNode)
	if err != nil {
		return storage.Entry{}, fmt.Errorf("marshal node %q: %w", pNode.ID, err)
	}
	return storage.Entry{Key: []byte("node:" + pNode.ID), Value: data}, nil
}

func (g *Graph) persistEdge(ctx context.Context, pEdge *parser.Edge) error {
	store := g.storageForEdge(pEdge)
	if store == nil {
		return nil
	}
	entry, err := g.edgeStorageEntry(pEdge)
	if err != nil {
		return err
	}
	if err := store.Put(ctx, entry.Key, entry.Value); err != nil {
		return fmt.Errorf("persist edge %q -> %q (%s): %w", pEdge.From, pEdge.To, pEdge.Type, err)
	}
	return nil
}

func (g *Graph) edgeStorageEntry(pEdge *parser.Edge) (storage.Entry, error) {
	data, err := json.Marshal(pEdge)
	if err != nil {
		return storage.Entry{}, fmt.Errorf("marshal edge %q -> %q: %w", pEdge.From, pEdge.To, err)
	}
	return storage.Entry{Key: edgeStorageKey(pEdge), Value: data}, nil
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
	b := make([]byte, 0, len(edge.From)+len(edge.To)+len(edge.Type)+48)
	b = append(b, "edge:v3:"...)
	b = appendLengthPrefixedString(b, edge.From)
	b = appendLengthPrefixedString(b, edge.To)
	b = appendLengthPrefixedString(b, string(edge.Type))
	return b
}

func appendLengthPrefixedString(dst []byte, s string) []byte {
	dst = strconv.AppendInt(dst, int64(len(s)), 10)
	dst = append(dst, ':')
	dst = append(dst, s...)
	return dst
}

func edgeStorageKeyV2(edge *parser.Edge) []byte {
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
	return [][]byte{edgeStorageKey(edge), edgeStorageKeyV2(edge), legacyEdgeStorageKey(edge)}
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

	g.mu.Lock()
	defer g.mu.Unlock()

	// First pass: load all nodes
	for _, store := range stores {
		err := iterateStoragePrefix(ctx, store, []byte("node:"), func(key []byte, value []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			pNode := new(parser.Node)
			if err := json.Unmarshal(value, pNode); err != nil {
				return err
			}
			g.loadNodeLocked(pNode)
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to iterate storage for nodes: %w", err)
		}
	}

	// Second pass: load all edges
	for _, store := range stores {
		err := iterateStoragePrefix(ctx, store, []byte("edge:"), func(key []byte, value []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			pEdge := new(parser.Edge)
			if err := json.Unmarshal(value, pEdge); err != nil {
				return err
			}
			if err := g.loadEdgeLocked(pEdge); err != nil && !errors.Is(err, ErrUnknownEdgeEndpoint) {
				return err
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to iterate storage for edges: %w", err)
		}
	}

	return nil
}

func iterateStoragePrefix(ctx context.Context, store storage.Storage, prefix []byte, fn func(key []byte, value []byte) error) error {
	if prefixStore, ok := store.(storage.PrefixIterator); ok {
		return prefixStore.IteratePrefix(ctx, prefix, fn)
	}
	return store.Iterate(ctx, func(key []byte, value []byte) error {
		if !bytes.HasPrefix(key, prefix) {
			return nil
		}
		return fn(key, value)
	})
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
		delete(g.nodes, id)
		delete(g.edges, id)
	}

	return nil
}
