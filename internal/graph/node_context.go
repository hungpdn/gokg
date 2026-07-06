package graph

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/hungpdn/gokg/internal/parser"
)

const (
	NodeContextDefaultMaxDependents   = 20
	NodeContextDefaultMaxCallers      = NodeContextDefaultMaxDependents
	NodeContextDefaultMaxDependencies = 20
	NodeContextDefaultMaxRelations    = 20
	NodeContextDefaultMaxDepth        = 1
	NodeContextDefaultMaxSourceLines  = 200
	NodeContextDefaultMaxSourceBytes  = 64 * 1024
	NodeContextMaxDependents          = 100
	NodeContextMaxCallers             = NodeContextMaxDependents
	NodeContextMaxDependencies        = 100
	NodeContextMaxRelations           = 100
	NodeContextMaxDepth               = 3
	NodeContextMaxSourceLines         = 1000
	NodeContextMaxSourceBytes         = 512 * 1024
)

type NodeContextOptions struct {
	IncludeSource *bool
	MaxDependents int
	// Deprecated: use MaxDependents.
	MaxCallers      int
	MaxDependencies int
	MaxRelations    int
	MaxDepth        int
	MaxSourceLines  int
	MaxSourceBytes  int
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
	ParentsTruncated      bool
	Children              []NodeContextRelation
	ChildrenTruncated     bool
	Routes                []NodeContextRelation
	RoutesTruncated       bool
	Interfaces            []NodeContextRelation
	InterfacesTruncated   bool
	Concurrency           []ConcurrencyConnection
	ConcurrencyTruncated  bool
	SourceTruncated       bool
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
	if opts.MaxDependents == 0 {
		opts.MaxDependents = opts.MaxCallers
	}
	if opts.MaxDependents == 0 {
		opts.MaxDependents = NodeContextDefaultMaxDependents
	}
	opts.MaxCallers = opts.MaxDependents
	if opts.MaxRelations == 0 {
		opts.MaxRelations = NodeContextDefaultMaxRelations
	}
	if opts.MaxDependencies == 0 {
		opts.MaxDependencies = NodeContextDefaultMaxDependencies
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = NodeContextDefaultMaxDepth
	}
	if opts.MaxSourceLines == 0 {
		opts.MaxSourceLines = NodeContextDefaultMaxSourceLines
	}
	if opts.MaxSourceBytes == 0 {
		opts.MaxSourceBytes = NodeContextDefaultMaxSourceBytes
	}
	return opts
}

func ValidateNodeContextOptions(opts NodeContextOptions) error {
	if opts.MaxDependents < 1 || opts.MaxDependents > NodeContextMaxDependents {
		return fmt.Errorf("max dependents must be between 1 and %d", NodeContextMaxDependents)
	}
	if opts.MaxDependencies < 1 || opts.MaxDependencies > NodeContextMaxDependencies {
		return fmt.Errorf("max dependencies must be between 1 and %d", NodeContextMaxDependencies)
	}
	if opts.MaxRelations < 1 || opts.MaxRelations > NodeContextMaxRelations {
		return fmt.Errorf("max relations must be between 1 and %d", NodeContextMaxRelations)
	}
	if opts.MaxDepth < 1 || opts.MaxDepth > NodeContextMaxDepth {
		return fmt.Errorf("max depth must be between 1 and %d", NodeContextMaxDepth)
	}
	if opts.MaxSourceLines < 1 || opts.MaxSourceLines > NodeContextMaxSourceLines {
		return fmt.Errorf("max source lines must be between 1 and %d", NodeContextMaxSourceLines)
	}
	if opts.MaxSourceBytes < 1 || opts.MaxSourceBytes > NodeContextMaxSourceBytes {
		return fmt.Errorf("max source bytes must be between 1 and %d", NodeContextMaxSourceBytes)
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

	if ctx.SourceRequested {
		source, truncated, err := readNodeContextSource(ctx.Node, opts.MaxSourceLines, opts.MaxSourceBytes)
		if err != nil {
			ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("source unavailable: %v", err))
		} else {
			ctx.SourceCode = source
			ctx.SourceTruncated = truncated
			if truncated {
				ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("source truncated at max_source_lines=%d or max_source_bytes=%d", opts.MaxSourceLines, opts.MaxSourceBytes))
			}
		}
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
	target := cloneNode(qb.g.nodes[numID])
	ctx := &NodeContext{
		Node:            target,
		SourceRequested: *opts.IncludeSource,
	}

	dependencies := make([]NodeContextRelation, 0)
	inboundDependencyIDs := make(map[int64][]int64)
	concurrency := make([]ConcurrencyConnection, 0)
	seenConcurrency := make(map[concurrencySeenKey]bool)

	for toID, edges := range qb.g.edges[numID] {
		other := qb.g.nodes[toID]
		if other == nil {
			continue
		}
		for _, edge := range sortedEdges(edges) {
			if edge == nil {
				continue
			}
			relation := NodeContextRelation{Node: cloneNode(other), Edge: cloneEdge(edge), Direction: "outbound"}
			switch {
			case isDependencyEdge(edge.Type):
				dependencies = append(dependencies, relation)
			case edge.Type == parser.EdgeTypeContains:
				ctx.Children = append(ctx.Children, relation)
			case isRouteContextRelation(target, other, edge):
				ctx.Routes = append(ctx.Routes, relation)
			case edge.Type == parser.EdgeTypeImplements:
				ctx.Interfaces = append(ctx.Interfaces, relation)
			}
		}
		if !isConcurrencyNode(other.Type) {
			qb.appendCalledConcurrencyConnections(toID, edges, "via_call", &concurrency, seenConcurrency)
			qb.appendSpawnedConcurrencyConnections(toID, edges, &concurrency, seenConcurrency)
			continue
		}
		for _, edge := range edges {
			if edge == nil || !isConcurrencyEdge(edge.Type) {
				continue
			}
			key := concurrencySeenKey{direction: "outbound", fromID: numID, toID: toID, edgeType: edge.Type}
			if seenConcurrency[key] {
				continue
			}
			seenConcurrency[key] = true
			concurrency = append(concurrency, ConcurrencyConnection{Node: other, Edge: edge, Direction: "outbound"})
		}
		if other.Type == parser.NodeTypeGoroutine {
			qb.appendSpawnedConcurrencyConnections(toID, edges, &concurrency, seenConcurrency)
		}
	}

	for fromID, outEdges := range qb.g.edges {
		for toID, edges := range outEdges {
			if hasDependencyEdge(edges) {
				inboundDependencyIDs[toID] = append(inboundDependencyIDs[toID], fromID)
			}
			if toID != numID {
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
				relation := NodeContextRelation{Node: cloneNode(other), Edge: cloneEdge(edge), Direction: "inbound"}
				switch {
				case edge.Type == parser.EdgeTypeContains:
					ctx.Parents = append(ctx.Parents, relation)
				case isRouteContextRelation(target, other, edge):
					ctx.Routes = append(ctx.Routes, relation)
				case edge.Type == parser.EdgeTypeImplements:
					ctx.Interfaces = append(ctx.Interfaces, relation)
				}
			}
			if !isConcurrencyNode(other.Type) {
				continue
			}
			for _, edge := range edges {
				if edge == nil {
					continue
				}
				isGoroutineCall := edge.Type == parser.EdgeTypeCalls && other.Type == parser.NodeTypeGoroutine
				if !isConcurrencyEdge(edge.Type) && !isGoroutineCall {
					continue
				}
				key := concurrencySeenKey{direction: "inbound", fromID: fromID, toID: numID, edgeType: edge.Type}
				if seenConcurrency[key] {
					continue
				}
				seenConcurrency[key] = true
				concurrency = append(concurrency, ConcurrencyConnection{Node: other, Edge: edge, Direction: "inbound"})
			}
		}
	}
	sortInboundDependencyIDsLocked(qb, inboundDependencyIDs)
	ctx.Dependents, ctx.DependentsTruncated = qb.nodeDistancesFromInboundLocked(numID, inboundDependencyIDs, opts.MaxDepth, opts.MaxDependents)
	if ctx.DependentsTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("dependents truncated at max_dependents=%d", opts.MaxDependents))
	}

	ctx.Dependencies, ctx.DependenciesTruncated = capNodeContextRelations(dependencies, opts.MaxDependencies)
	if ctx.DependenciesTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("dependencies truncated at max_dependencies=%d", opts.MaxDependencies))
	}
	ctx.Parents, ctx.ParentsTruncated = capNodeContextRelations(ctx.Parents, opts.MaxRelations)
	if ctx.ParentsTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("parents truncated at max_relations=%d", opts.MaxRelations))
	}
	ctx.Children, ctx.ChildrenTruncated = capNodeContextRelations(ctx.Children, opts.MaxRelations)
	if ctx.ChildrenTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("children truncated at max_relations=%d", opts.MaxRelations))
	}
	ctx.Routes, ctx.RoutesTruncated = capNodeContextRelations(ctx.Routes, opts.MaxRelations)
	if ctx.RoutesTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("routes truncated at max_relations=%d", opts.MaxRelations))
	}
	ctx.Interfaces, ctx.InterfacesTruncated = capNodeContextRelations(ctx.Interfaces, opts.MaxRelations)
	if ctx.InterfacesTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("interfaces truncated at max_relations=%d", opts.MaxRelations))
	}
	ctx.Concurrency, ctx.ConcurrencyTruncated = capConcurrencyConnections(concurrency, opts.MaxRelations)
	if ctx.ConcurrencyTruncated {
		ctx.Warnings = append(ctx.Warnings, fmt.Sprintf("concurrency context truncated at max_relations=%d", opts.MaxRelations))
	}
	return ctx, nil
}

func (qb *QueryBuilder) nodeDistancesFromInboundLocked(sourceID int64, inbound map[int64][]int64, maxDepth int, maxNodes int) ([]NodeDistance, bool) {
	type queuedNode struct {
		id       int64
		distance int
	}
	queue := []queuedNode{{id: sourceID, distance: 0}}
	seen := map[int64]int{sourceID: 0}
	resultsByID := make(map[int64]NodeDistance)

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if current.distance >= maxDepth {
			continue
		}
		nextDistance := current.distance + 1
		for _, fromID := range inbound[current.id] {
			if qb.g.nodes[fromID] == nil {
				continue
			}
			if prevDistance, ok := seen[fromID]; ok && prevDistance <= nextDistance {
				continue
			}
			seen[fromID] = nextDistance
			result := NodeDistance{Node: cloneNode(qb.g.nodes[fromID]), Distance: nextDistance}
			resultsByID[fromID] = result
			queue = append(queue, queuedNode{id: fromID, distance: nextDistance})
			if maxNodes > 0 && len(resultsByID) >= maxNodes {
				return sortedNodeDistances(resultsByID), true
			}
		}
	}
	return sortedNodeDistances(resultsByID), false
}

func readNodeContextSource(node *parser.Node, maxLines int, maxBytes int) (string, bool, error) {
	if node == nil {
		return "", false, fmt.Errorf("node data missing")
	}
	filePath := node.FilePath
	lines := node.Lines
	if filePath == "" {
		return "", false, fmt.Errorf("node %s has no file path", node.ID)
	}
	if lines[0] <= 0 || lines[1] <= 0 {
		return "", false, fmt.Errorf("node %s has no line range info", node.ID)
	}
	if lines[1] < lines[0] {
		return "", false, fmt.Errorf("node %s has invalid line range: %d-%d", node.ID, lines[0], lines[1])
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", false, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer f.Close() //nolint:errcheck

	var b strings.Builder
	if maxBytes < 4096 {
		b.Grow(maxBytes)
	} else {
		b.Grow(4096)
	}
	reader := bufio.NewReader(f)
	lineNum := 1
	capturedLines := 0
	truncated := false

	for lineNum <= lines[1] {
		part, readErr := reader.ReadSlice('\n')
		if readErr != nil && readErr != io.EOF && readErr != bufio.ErrBufferFull {
			return "", false, fmt.Errorf("error reading file %s: %w", filePath, readErr)
		}
		if len(part) == 0 && readErr == io.EOF {
			break
		}

		inRange := lineNum >= lines[0] && lineNum <= lines[1]
		if inRange {
			if capturedLines >= maxLines {
				truncated = true
				break
			}
			if b.Len() >= maxBytes {
				truncated = true
				break
			}
			remaining := maxBytes - b.Len()
			if len(part) > remaining {
				b.Write(part[:remaining])
				truncated = true
				break
			}
			b.Write(part)
		}

		if readErr != bufio.ErrBufferFull {
			if inRange {
				capturedLines++
				if capturedLines >= maxLines && lineNum < lines[1] {
					truncated = true
					break
				}
			}
			lineNum++
		}
		if readErr == io.EOF {
			break
		}
	}
	return b.String(), truncated, nil
}

func capNodeContextRelations(relations []NodeContextRelation, max int) ([]NodeContextRelation, bool) {
	sortNodeContextRelations(relations)
	if len(relations) <= max {
		return relations, false
	}
	return append([]NodeContextRelation(nil), relations[:max]...), true
}

func capConcurrencyConnections(connections []ConcurrencyConnection, max int) ([]ConcurrencyConnection, bool) {
	sortConcurrencyConnections(connections)
	if len(connections) > max {
		connections = connections[:max]
		cloned := cloneConcurrencyConnections(connections)
		return cloned, true
	}
	return cloneConcurrencyConnections(connections), false
}

func cloneConcurrencyConnections(connections []ConcurrencyConnection) []ConcurrencyConnection {
	cloned := make([]ConcurrencyConnection, 0, len(connections))
	for _, conn := range connections {
		cloned = append(cloned, ConcurrencyConnection{
			Node:      cloneNode(conn.Node),
			Edge:      cloneEdge(conn.Edge),
			Direction: conn.Direction,
		})
	}
	return cloned
}

func sortInboundDependencyIDsLocked(qb *QueryBuilder, inbound map[int64][]int64) {
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
}

func cloneNode(node *parser.Node) *parser.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	return &cloned
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
