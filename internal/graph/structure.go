package graph

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hungpdn/gokg/internal/parser"
)

const (
	RepositoryStructureDefaultMaxDepth = 4
	RepositoryStructureMaxDepth        = 32
	RepositoryStructureDefaultMaxNodes = 2_000
	RepositoryStructureMaxNodes        = 5_000
)

// RepositoryStructureOptions controls how repository structure is read from the
// graph.
type RepositoryStructureOptions struct {
	RepoID          string `json:"repo_id,omitempty"`
	Root            string `json:"root,omitempty"`
	MaxDepth        int    `json:"max_depth,omitempty"`
	MaxNodes        int    `json:"max_nodes,omitempty"`
	IncludePackages bool   `json:"include_packages"`
	IncludeFiles    bool   `json:"include_files"`
}

// RepositoryStructureNode is a tree node returned by GetRepositoryStructure.
type RepositoryStructureNode struct {
	Node     *parser.Node               `json:"node"`
	Children []*RepositoryStructureNode `json:"children,omitempty"`
}

// PackageFoldersForRoot returns known package locations keyed by repository
// relative directory. It uses the graph's current FILE nodes, so callers should
// refresh packages before refreshing repository structure.
func (g *Graph) PackageFoldersForRoot(root string, repoID string) map[string]map[string]bool {
	root = cleanAbsPath(root)

	g.mu.RLock()
	defer g.mu.RUnlock()

	packageFolders := make(map[string]map[string]bool)
	for _, node := range g.nodes {
		if node == nil || node.Type != parser.NodeTypeFile || node.FilePath == "" || node.PkgPath == "" {
			continue
		}
		if repoID != "" && node.RepoID != repoID {
			continue
		}

		relDir, err := filepath.Rel(root, filepath.Dir(node.FilePath))
		if err != nil || !isGraphPathInsideRoot(relDir) {
			continue
		}
		relDir = filepath.ToSlash(relDir)
		if packageFolders[relDir] == nil {
			packageFolders[relDir] = make(map[string]bool)
		}
		packageFolders[relDir][node.PkgPath] = true
	}
	return packageFolders
}

// PackagePathsUnderDir returns package paths with known source files anywhere
// under dir.
func (g *Graph) PackagePathsUnderDir(dir string) []string {
	targetDir := cleanAbsPath(dir)

	g.mu.RLock()
	defer g.mu.RUnlock()

	seen := make(map[string]struct{})
	for _, node := range g.nodes {
		if node == nil || node.FilePath == "" || node.PkgPath == "" {
			continue
		}
		fileDir := cleanAbsPath(filepath.Dir(node.FilePath))
		if fileDir == targetDir || strings.HasPrefix(fileDir, targetDir+string(filepath.Separator)) {
			seen[node.PkgPath] = struct{}{}
		}
	}

	paths := make([]string, 0, len(seen))
	for pkgPath := range seen {
		paths = append(paths, pkgPath)
	}
	sort.Strings(paths)
	return paths
}

// ReplaceRepositoryStructure replaces only the repository structure snapshot:
// FOLDER nodes, non-Go FILE nodes, and CONTAINS edges originating from
// WORKSPACE, REPO, or FOLDER nodes. It intentionally leaves package snapshots,
// Go source file nodes, and dependency edges alone.
func (g *Graph) ReplaceRepositoryStructure(ctx context.Context, repoID string, result *parser.ParseResult) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.removeRepositoryStructureLocked(ctx, repoID); err != nil {
		return err
	}

	nodeStages, nextNodeID, err := g.stageNodes(ctx, result)
	if err != nil {
		return err
	}
	if err := g.persistStagedNodes(ctx, nodeStages); err != nil {
		return err
	}
	g.applyStagedNodes(nodeStages, nextNodeID)

	edgeStages, unresolvedEdges, err := g.stageEdges(ctx, result)
	if err != nil {
		return err
	}
	if err := g.persistStagedEdges(ctx, edgeStages, unresolvedEdges); err != nil {
		return err
	}
	g.applyStagedEdges(edgeStages)
	return nil
}

func (g *Graph) removeRepositoryStructureLocked(ctx context.Context, repoID string) error {
	structureNodeIDs := make(map[int64]string)
	for id, node := range g.nodes {
		if node == nil || !isRepositoryStructureOwnedNode(node) {
			continue
		}
		if repoID != "" && node.RepoID != repoID {
			continue
		}
		structureNodeIDs[id] = node.ID
		if store := g.storageForNode(node); store != nil {
			if err := store.Delete(ctx, []byte("node:"+node.ID)); err != nil {
				return fmt.Errorf("delete repository structure node %q: %w", node.ID, err)
			}
		}
	}

	for fromID, outEdges := range g.edges {
		for toID, edges := range outEdges {
			kept := edges[:0]
			for _, edge := range edges {
				if edge == nil || !g.isRepositoryStructureEdgeLocked(edge, repoID) {
					kept = append(kept, edge)
					continue
				}
				if store := g.storageForEdge(edge); store != nil {
					for _, key := range edgeStorageDeleteKeys(edge) {
						if err := store.Delete(ctx, key); err != nil {
							return fmt.Errorf("delete structure edge %q -> %q (%s): %w", edge.From, edge.To, edge.Type, err)
						}
					}
				}
			}
			if len(kept) == 0 {
				delete(outEdges, toID)
			} else {
				outEdges[toID] = kept
			}
		}
		if len(outEdges) == 0 {
			delete(g.edges, fromID)
		}
	}

	for id, externalID := range structureNodeIDs {
		delete(g.nodeMap, externalID)
		delete(g.nodes, id)
		delete(g.edges, id)
	}
	return nil
}

func isRepositoryStructureOwnedNode(node *parser.Node) bool {
	if node == nil {
		return false
	}
	return node.Type == parser.NodeTypeFolder ||
		(node.Type == parser.NodeTypeFile && node.PkgPath == "")
}

func (g *Graph) isRepositoryStructureEdgeLocked(edge *parser.Edge, repoID string) bool {
	if edge.Type != parser.EdgeTypeContains {
		return false
	}
	if repoID != "" && edge.RepoID != repoID {
		return false
	}

	fromNode := g.nodeByExternalIDLocked(edge.From)
	toNode := g.nodeByExternalIDLocked(edge.To)
	return isRepositoryStructureEndpoint(fromNode) || (toNode != nil && toNode.Type == parser.NodeTypeFolder)
}

func (g *Graph) nodeByExternalIDLocked(nodeID string) *parser.Node {
	id, ok := g.nodeMap[nodeID]
	if !ok {
		return nil
	}
	return g.nodes[id]
}

func isRepositoryStructureEndpoint(node *parser.Node) bool {
	if node == nil {
		return false
	}
	return node.Type == parser.NodeTypeWorkspace || node.Type == parser.NodeTypeRepo || node.Type == parser.NodeTypeFolder
}

// GetRepositoryStructure returns a repository tree rooted at a FOLDER, PACKAGE,
// or FILE node.
func (qb *QueryBuilder) GetRepositoryStructure(opts RepositoryStructureOptions) (*RepositoryStructureNode, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	rootID, err := qb.repositoryStructureRootIDLocked(opts)
	if err != nil {
		return nil, err
	}
	rootNumID, err := qb.requireNodeIDLocked(rootID)
	if err != nil {
		return nil, err
	}

	maxDepth := opts.MaxDepth
	if maxDepth < 0 {
		return nil, fmt.Errorf("max_depth must be at least 1")
	}
	if maxDepth == 0 {
		maxDepth = RepositoryStructureDefaultMaxDepth
	}
	if maxDepth > RepositoryStructureMaxDepth {
		return nil, fmt.Errorf("max_depth must be at most %d", RepositoryStructureMaxDepth)
	}

	maxNodes := opts.MaxNodes
	if maxNodes < 0 {
		return nil, fmt.Errorf("max_nodes must be at least 1")
	}
	if maxNodes == 0 {
		maxNodes = RepositoryStructureDefaultMaxNodes
	}
	if maxNodes > RepositoryStructureMaxNodes {
		return nil, fmt.Errorf("max_nodes must be at most %d", RepositoryStructureMaxNodes)
	}

	seen := make(map[int64]bool)
	nodeCount := 0
	return qb.buildRepositoryStructureNodeLocked(rootNumID, opts, maxDepth, 0, seen, &nodeCount, maxNodes)
}

func (qb *QueryBuilder) repositoryStructureRootIDLocked(opts RepositoryStructureOptions) (string, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = "."
	}

	if strings.HasPrefix(root, "folder:") || strings.HasPrefix(root, "repo:") || strings.HasPrefix(root, "workspace:") {
		return root, nil
	}

	if opts.RepoID != "" {
		return parser.BuildID(parser.RepoNodeID(opts.RepoID), ":", graphFolderNodeID(root)), nil
	}

	id := graphFolderNodeID(root)
	if numID, ok := qb.g.nodeMap[id]; ok && qb.g.nodes[numID] != nil {
		return id, nil
	}

	var matches []string
	suffix := ":" + id
	for _, node := range qb.g.nodes {
		if node == nil || node.Type != parser.NodeTypeFolder {
			continue
		}
		if node.ID == id || strings.HasSuffix(node.ID, suffix) {
			matches = append(matches, node.ID)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return id, nil
	case 1:
		return matches[0], nil
	default:
		if root == "." {
			for _, node := range qb.g.nodes {
				if node != nil && node.Type == parser.NodeTypeWorkspace {
					return node.ID, nil
				}
			}
		}
		return "", fmt.Errorf("multiple repository roots match %q; pass repo_id", root)
	}
}

func (qb *QueryBuilder) buildRepositoryStructureNodeLocked(
	numID int64,
	opts RepositoryStructureOptions,
	maxDepth int,
	depth int,
	seen map[int64]bool,
	nodeCount *int,
	maxNodes int,
) (*RepositoryStructureNode, error) {
	node := qb.g.nodes[numID]
	if node == nil {
		return nil, nil
	}
	if *nodeCount >= maxNodes {
		return nil, repositoryStructureLimitError(maxNodes)
	}
	*nodeCount = *nodeCount + 1

	tree := &RepositoryStructureNode{Node: node}
	if seen[numID] || depth >= maxDepth {
		return tree, nil
	}
	seen[numID] = true

	childIDs, err := qb.repositoryStructureChildIDsLocked(numID, opts, maxNodes-*nodeCount, maxNodes)
	if err != nil {
		return nil, err
	}
	for _, childID := range childIDs {
		child, err := qb.buildRepositoryStructureNodeLocked(childID, opts, maxDepth, depth+1, seen, nodeCount, maxNodes)
		if err != nil {
			return nil, err
		}
		if child != nil {
			tree.Children = append(tree.Children, child)
		}
	}
	delete(seen, numID)
	return tree, nil
}

func repositoryStructureLimitError(maxNodes int) error {
	return fmt.Errorf(
		"repository structure exceeds max_nodes=%d; narrow root, reduce max_depth, or exclude files",
		maxNodes,
	)
}

func (qb *QueryBuilder) repositoryStructureChildIDsLocked(
	numID int64,
	opts RepositoryStructureOptions,
	maxChildren int,
	maxNodes int,
) ([]int64, error) {
	capacity := len(qb.g.edges[numID])
	if capacity > maxChildren {
		capacity = maxChildren
	}
	children := make([]int64, 0, capacity)
	for toID, edges := range qb.g.edges[numID] {
		child := qb.g.nodes[toID]
		if child == nil || !repositoryStructureIncludesNode(child, opts) {
			continue
		}
		if !hasContainsEdge(edges) {
			continue
		}
		if len(children) >= maxChildren {
			return nil, repositoryStructureLimitError(maxNodes)
		}
		children = append(children, toID)
	}
	sort.Slice(children, func(i, j int) bool {
		return compareRepositoryStructureNodes(qb.g.nodes[children[i]], qb.g.nodes[children[j]]) < 0
	})
	return children, nil
}

func repositoryStructureIncludesNode(node *parser.Node, opts RepositoryStructureOptions) bool {
	switch node.Type {
	case parser.NodeTypeWorkspace, parser.NodeTypeRepo, parser.NodeTypeFolder:
		return true
	case parser.NodeTypePackage:
		return opts.IncludePackages
	case parser.NodeTypeFile:
		return opts.IncludeFiles
	default:
		return false
	}
}

func compareRepositoryStructureNodes(a, b *parser.Node) int {
	ar, br := repositoryStructureRank(a), repositoryStructureRank(b)
	if ar != br {
		return ar - br
	}
	an, bn := "", ""
	if a != nil {
		an = a.Name
	}
	if b != nil {
		bn = b.Name
	}
	if an != bn {
		if an < bn {
			return -1
		}
		return 1
	}
	if a != nil && b != nil && a.ID < b.ID {
		return -1
	}
	if a != nil && b != nil && a.ID > b.ID {
		return 1
	}
	return 0
}

func repositoryStructureRank(node *parser.Node) int {
	if node == nil {
		return 99
	}
	switch node.Type {
	case parser.NodeTypeWorkspace:
		return -2
	case parser.NodeTypeRepo:
		return -1
	case parser.NodeTypeFolder:
		return 0
	case parser.NodeTypePackage:
		return 1
	case parser.NodeTypeFile:
		return 2
	default:
		return 3
	}
}

func hasContainsEdge(edges []*parser.Edge) bool {
	for _, edge := range edges {
		if edge != nil && edge.Type == parser.EdgeTypeContains {
			return true
		}
	}
	return false
}

func graphFolderNodeID(rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return "folder:."
	}
	return parser.BuildID("folder:", rel)
}

func isGraphPathInsideRoot(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel))
}
