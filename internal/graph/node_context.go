package graph

import (
	"fmt"
	"sort"

	"github.com/hungpdn/gokg/internal/parser"
)

const (
	NodeContextDefaultMaxCallers      = 20
	NodeContextDefaultMaxDependencies = 20
	NodeContextDefaultMaxDepth        = 1
	NodeContextMaxCallers             = 100
	NodeContextMaxDependencies        = 100
	NodeContextMaxDepth               = 3
)

type NodeContextOptions struct {
	IncludeSource   *bool
	MaxCallers      int
	MaxDependencies int
	MaxDepth        int
}

type NodeContext struct {
	Node                  *parser.Node
	SourceRequested       bool
	SourceCode            string
	Dependencies          []NodeContextRelation
	DependenciesTruncated bool
	Dependents            []NodeDistance
	DependentsTruncated   bool
	Parents               []NodeContextRelation
	Children              []NodeContextRelation
	Routes                []NodeContextRelation
	Interfaces            []NodeContextRelation
	Concurrency           []ConcurrencyConnection
	Warnings              []string
}

type NodeContextRelation struct {
	Node      *parser.Node
	Edge      *parser.Edge
	Direction string
}

func NormalizeNodeContextOptions(opts NodeContextOptions) NodeContextOptions {
	if opts.IncludeSource == nil {
		includeSource := true
		opts.IncludeSource = &includeSource
	}
	if opts.MaxCallers == 0 {
		opts.MaxCallers = NodeContextDefaultMaxCallers
	}
	if opts.MaxDependencies == 0 {
		opts.MaxDependencies = NodeContextDefaultMaxDependencies
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = NodeContextDefaultMaxDepth
	}
	return opts
}

func ValidateNodeContextOptions(opts NodeContextOptions) error {
	if opts.MaxCallers < 1 || opts.MaxCallers > NodeContextMaxCallers {
		return fmt.Errorf("max callers must be between 1 and %d", NodeContextMaxCallers)
	}
	if opts.MaxDependencies < 1 || opts.MaxDependencies > NodeContextMaxDependencies {
		return fmt.Errorf("max dependencies must be between 1 and %d", NodeContextMaxDependencies)
	}
	if opts.MaxDepth < 1 || opts.MaxDepth > NodeContextMaxDepth {
		return fmt.Errorf("max depth must be between 1 and %d", NodeContextMaxDepth)
	}
	return nil
}

func (qb *QueryBuilder) GetNodeContext(nodeID string, opts NodeContextOptions) (*NodeContext, error) {
	opts = NormalizeNodeContextOptions(opts)
	if err := ValidateNodeContextOptions(opts); err != nil {
		return nil, err
	}

	ctx, err := qb.nodeContextSnapshot(nodeID, opts)
	if err != nil {
		return nil, err
	}

	dependents, truncated, err := qb.GetBlastRadiusDepth([]string{nodeID}, opts.MaxDepth, opts.MaxCallers)
	if err != nil {
		return nil, err
	}
	ctx.Dependents = dependents
	ctx.DependentsTruncated = truncated
	if truncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("dependents truncated at max_callers=%d", opts.MaxCallers))
	}

	if ctx.SourceRequested {
		source, err := qb.GetSourceCode(nodeID)
		if err != nil {
			ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("source unavailable: %v", err))
		} else {
			ctx.SourceCode = source
		}
	}

	concurrency, err := qb.GetConcurrencyGraph(nodeID)
	if err != nil {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("concurrency context unavailable: %v", err))
	} else {
		sortConcurrencyConnections(concurrency)
		ctx.Concurrency = concurrency
	}
	sort.Strings(ctx.Warnings)
	return ctx, nil
}

func (qb *QueryBuilder) nodeContextSnapshot(nodeID string, opts NodeContextOptions) (*NodeContext, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	numID, err := qb.requireNodeIDLocked(nodeID)
	if err != nil {
		return nil, err
	}
	target := qb.g.nodes[numID]
	ctx := &NodeContext{
		Node:            target,
		SourceRequested: *opts.IncludeSource,
	}

	dependencies := make([]NodeContextRelation, 0)
	for toID, edges := range qb.g.edges[numID] {
		other := qb.g.nodes[toID]
		if other == nil {
			continue
		}
		for _, edge := range sortedEdges(edges) {
			if edge == nil {
				continue
			}
			switch {
			case isDependencyEdge(edge.Type):
				dependencies = append(dependencies, NodeContextRelation{Node: other, Edge: edge, Direction: "outbound"})
			case edge.Type == parser.EdgeTypeContains:
				ctx.Children = append(ctx.Children, NodeContextRelation{Node: other, Edge: edge, Direction: "outbound"})
			case isRouteContextRelation(target, other, edge):
				ctx.Routes = append(ctx.Routes, NodeContextRelation{Node: other, Edge: edge, Direction: "outbound"})
			case edge.Type == parser.EdgeTypeImplements:
				ctx.Interfaces = append(ctx.Interfaces, NodeContextRelation{Node: other, Edge: edge, Direction: "outbound"})
			}
		}
	}

	for fromID, outEdges := range qb.g.edges {
		edges, ok := outEdges[numID]
		if !ok {
			continue
		}
		other := qb.g.nodes[fromID]
		if other == nil {
			continue
		}
		for _, edge := range sortedEdges(edges) {
			if edge == nil {
				continue
			}
			switch {
			case edge.Type == parser.EdgeTypeContains:
				ctx.Parents = append(ctx.Parents, NodeContextRelation{Node: other, Edge: edge, Direction: "inbound"})
			case isRouteContextRelation(target, other, edge):
				ctx.Routes = append(ctx.Routes, NodeContextRelation{Node: other, Edge: edge, Direction: "inbound"})
			case edge.Type == parser.EdgeTypeImplements:
				ctx.Interfaces = append(ctx.Interfaces, NodeContextRelation{Node: other, Edge: edge, Direction: "inbound"})
			}
		}
	}

	sortNodeContextRelations(dependencies)
	if len(dependencies) > opts.MaxDependencies {
		ctx.Dependencies = append([]NodeContextRelation(nil), dependencies[:opts.MaxDependencies]...)
		ctx.DependenciesTruncated = true
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("dependencies truncated at max_dependencies=%d", opts.MaxDependencies))
	} else {
		ctx.Dependencies = dependencies
	}
	sortNodeContextRelations(ctx.Parents)
	sortNodeContextRelations(ctx.Children)
	sortNodeContextRelations(ctx.Routes)
	sortNodeContextRelations(ctx.Interfaces)
	return ctx, nil
}

func isRouteContextRelation(target *parser.Node, other *parser.Node, edge *parser.Edge) bool {
	if edge == nil {
		return false
	}
	if edge.Type == parser.EdgeTypeRegistersRoute {
		return true
	}
	return edge.Type == parser.EdgeTypeReferences &&
		(target != nil && target.Type == parser.NodeTypeRoute || other != nil && other.Type == parser.NodeTypeRoute)
}

func sortedEdges(edges []*parser.Edge) []*parser.Edge {
	sorted := append([]*parser.Edge(nil), edges...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i] == nil || sorted[j] == nil {
			return sorted[j] != nil
		}
		if sorted[i].Type != sorted[j].Type {
			return sorted[i].Type < sorted[j].Type
		}
		if sorted[i].From != sorted[j].From {
			return sorted[i].From < sorted[j].From
		}
		return sorted[i].To < sorted[j].To
	})
	return sorted
}

func sortNodeContextRelations(relations []NodeContextRelation) {
	sort.Slice(relations, func(i, j int) bool {
		left := relations[i]
		right := relations[j]
		leftID := ""
		rightID := ""
		if left.Node != nil {
			leftID = left.Node.ID
		}
		if right.Node != nil {
			rightID = right.Node.ID
		}
		if leftID != rightID {
			return leftID < rightID
		}
		leftType := ""
		rightType := ""
		if left.Edge != nil {
			leftType = string(left.Edge.Type)
		}
		if right.Edge != nil {
			rightType = string(right.Edge.Type)
		}
		if leftType != rightType {
			return leftType < rightType
		}
		return left.Direction < right.Direction
	})
}

func sortConcurrencyConnections(connections []ConcurrencyConnection) {
	sort.Slice(connections, func(i, j int) bool {
		left := connections[i]
		right := connections[j]
		if left.Direction != right.Direction {
			return left.Direction < right.Direction
		}
		leftID := ""
		rightID := ""
		if left.Node != nil {
			leftID = left.Node.ID
		}
		if right.Node != nil {
			rightID = right.Node.ID
		}
		if leftID != rightID {
			return leftID < rightID
		}
		leftType := ""
		rightType := ""
		if left.Edge != nil {
			leftType = string(left.Edge.Type)
		}
		if right.Edge != nil {
			rightType = string(right.Edge.Type)
		}
		return leftType < rightType
	})
}
