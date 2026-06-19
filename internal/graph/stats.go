package graph

import (
	"sort"
	"unsafe"

	"github.com/hungpdn/gokg/internal/parser"
)

type Stats struct {
	NodeCount          int            `json:"node_count"`
	EdgeCount          int            `json:"edge_count"`
	FileNodeCount      int            `json:"file_node_count"`
	SourceFileCount    int            `json:"source_file_count"`
	RAMEstimateBytes   int64          `json:"ram_estimate_bytes"`
	NodesByKind        map[string]int `json:"nodes_by_kind"`
	EdgesByKind        map[string]int `json:"edges_by_kind"`
	NodesByRepo        map[string]int `json:"nodes_by_repo,omitempty"`
	EdgesByRepo        map[string]int `json:"edges_by_repo,omitempty"`
	TopPackagesByNodes []PackageStat  `json:"top_packages_by_nodes,omitempty"`
}

type PackageStat struct {
	PkgPath string `json:"pkg_path"`
	Nodes   int    `json:"nodes"`
}

// Stats returns aggregate graph statistics for CLI/reporting use.
func (g *Graph) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := Stats{
		NodesByKind: make(map[string]int, 16),
		EdgesByKind: make(map[string]int, 9),
		NodesByRepo: make(map[string]int, 4),
		EdgesByRepo: make(map[string]int, 4),
		RAMEstimateBytes: int64(unsafe.Sizeof(*g)) +
			int64(len(g.nodeMap))*64 +
			int64(len(g.nodes))*32 +
			int64(len(g.edges))*32,
	}

	sourceFiles := make(map[string]struct{}, len(g.nodes))
	nodesByPackage := make(map[string]int, len(g.nodes))

	for _, node := range g.nodes {
		if node == nil {
			continue
		}

		stats.NodeCount++
		stats.NodesByKind[string(node.Type)]++
		stats.RAMEstimateBytes += estimateNodeBytes(node)

		if node.Type == parser.NodeTypeFile {
			stats.FileNodeCount++
		}
		if node.FilePath != "" {
			sourceFiles[node.FilePath] = struct{}{}
		}
		if node.RepoID != "" {
			stats.NodesByRepo[node.RepoID]++
		}
		if node.PkgPath != "" {
			nodesByPackage[node.PkgPath]++
		}
	}

	for _, edgeMap := range g.edges {
		stats.RAMEstimateBytes += int64(len(edgeMap)) * 32
		for _, edges := range edgeMap {
			stats.RAMEstimateBytes += int64(len(edges)) * 16
			for _, edge := range edges {
				if edge == nil {
					continue
				}

				stats.EdgeCount++
				stats.EdgesByKind[string(edge.Type)]++
				stats.RAMEstimateBytes += estimateEdgeBytes(edge)
				if edge.RepoID != "" {
					stats.EdgesByRepo[edge.RepoID]++
				}
			}
		}
	}

	stats.SourceFileCount = len(sourceFiles)
	stats.TopPackagesByNodes = topPackageStats(nodesByPackage, 10)

	if len(stats.NodesByRepo) == 0 {
		stats.NodesByRepo = nil
	}
	if len(stats.EdgesByRepo) == 0 {
		stats.EdgesByRepo = nil
	}

	return stats
}

func estimateNodeBytes(node *parser.Node) int64 {
	return int64(unsafe.Sizeof(*node)) +
		int64(len(node.ID)+len(node.Name)+len(node.PkgPath)+len(node.FilePath)+len(node.RepoID))
}

func estimateEdgeBytes(edge *parser.Edge) int64 {
	return int64(unsafe.Sizeof(*edge)) +
		int64(len(edge.From)+len(edge.To)+len(edge.Type)+len(edge.RepoID))
}

func topPackageStats(nodesByPackage map[string]int, limit int) []PackageStat {
	if len(nodesByPackage) == 0 || limit <= 0 {
		return nil
	}

	packages := make([]PackageStat, 0, len(nodesByPackage))
	for pkgPath, nodes := range nodesByPackage {
		packages = append(packages, PackageStat{PkgPath: pkgPath, Nodes: nodes})
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Nodes == packages[j].Nodes {
			return packages[i].PkgPath < packages[j].PkgPath
		}
		return packages[i].Nodes > packages[j].Nodes
	})
	if len(packages) > limit {
		packages = packages[:limit]
	}
	return packages
}
