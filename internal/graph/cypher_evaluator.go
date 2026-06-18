package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hungpdn/gokg/internal/cypher"
	"github.com/hungpdn/gokg/internal/parser"
)

const (
	cypherAliasNode = "node"
	cypherAliasEdge = "edge"
)

var validCypherNodeTypes = map[string]struct{}{
	string(parser.NodeTypePackage):   {},
	string(parser.NodeTypeFile):      {},
	string(parser.NodeTypeFolder):    {},
	string(parser.NodeTypeStruct):    {},
	string(parser.NodeTypeInterface): {},
	string(parser.NodeTypeFunc):      {},
	string(parser.NodeTypeMethod):    {},
	string(parser.NodeTypeConstant):  {},
	string(parser.NodeTypeVariable):  {},
	string(parser.NodeTypeTypeAlias): {},
	string(parser.NodeTypeVar):       {},
	string(parser.NodeTypeChannel):   {},
	string(parser.NodeTypeGoroutine): {},
	string(parser.NodeTypeBoundary):  {},
	string(parser.NodeTypeRepo):      {},
	string(parser.NodeTypeWorkspace): {},
}

var validCypherEdgeTypes = map[string]struct{}{
	string(parser.EdgeTypeCalls):        {},
	string(parser.EdgeTypeImplements):   {},
	string(parser.EdgeTypeImports):      {},
	string(parser.EdgeTypeReferences):   {},
	string(parser.EdgeTypeInstantiates): {},
	string(parser.EdgeTypeSpawns):       {},
	string(parser.EdgeTypeSendsTo):      {},
	string(parser.EdgeTypeReceivesFrom): {},
	string(parser.EdgeTypeContains):     {},
}

var validCypherNodeProperties = map[string]struct{}{
	"name":     {},
	"id":       {},
	"pkgpath":  {},
	"filepath": {},
	"type":     {},
	"repoid":   {},
}

var validCypherEdgeProperties = map[string]struct{}{
	"type":   {},
	"from":   {},
	"to":     {},
	"repoid": {},
}

// CypherResultRow represents one matched row of the query.
type CypherResultRow map[string]interface{}

// ExecuteCypher evaluates a parsed Cypher query against the Graph.
func (qb *QueryBuilder) ExecuteCypher(q *cypher.Query) ([]CypherResultRow, error) {
	qb.g.mu.RLock()
	defer qb.g.mu.RUnlock()

	if err := validateCypherQuery(q); err != nil {
		return nil, err
	}

	pattern := q.Match.Pattern
	results := make([]CypherResultRow, 0)

	for _, node1 := range qb.g.nodes {
		if !matchesCypherNodeType(node1, pattern.Node1.Type) {
			continue
		}

		if pattern.Edge == nil || pattern.Node2 == nil {
			row := make(CypherResultRow)
			bindCypherAlias(row, pattern.Node1.Alias, node1)
			if cypherRowMatchesWhere(q.Where, row) {
				results = append(results, row)
			}
			continue
		}

		numID1, ok := qb.g.nodeMap[node1.ID]
		if !ok {
			continue
		}

		if pattern.Edge.Direction == cypher.DirOutbound || pattern.Edge.Direction == cypher.DirAny {
			for numID2, edges := range qb.g.edges[numID1] {
				node2 := qb.g.nodes[numID2]
				if !matchesCypherNodeType(node2, pattern.Node2.Type) {
					continue
				}
				results = appendCypherEdgeRows(results, q, pattern, node1, node2, edges)
			}
		}

		if pattern.Edge.Direction == cypher.DirInbound || pattern.Edge.Direction == cypher.DirAny {
			for numID2, outEdges := range qb.g.edges {
				if pattern.Edge.Direction == cypher.DirAny && numID2 == numID1 {
					continue
				}
				node2 := qb.g.nodes[numID2]
				if !matchesCypherNodeType(node2, pattern.Node2.Type) {
					continue
				}
				edges, ok := outEdges[numID1]
				if !ok {
					continue
				}
				results = appendCypherEdgeRows(results, q, pattern, node1, node2, edges)
			}
		}
	}

	finalResults := make([]CypherResultRow, 0, len(results))
	for _, row := range results {
		finalRow := make(CypherResultRow)
		for _, item := range q.Return.Items {
			val := row[item.Alias]
			if item.Property == "" {
				finalRow[item.Alias] = val
				continue
			}

			propVal, ok := cypherPropertyValue(val, item.Property)
			if !ok {
				return nil, fmt.Errorf("unknown property %q for alias %q", item.Property, item.Alias)
			}
			finalRow[item.Alias+"."+item.Property] = propVal
		}
		finalResults = append(finalResults, finalRow)

		if q.Limit > 0 && len(finalResults) >= q.Limit {
			break
		}
	}

	return finalResults, nil
}

func appendCypherEdgeRows(
	results []CypherResultRow,
	q *cypher.Query,
	pattern *cypher.Pattern,
	node1 *parser.Node,
	node2 *parser.Node,
	edges []*parser.Edge,
) []CypherResultRow {
	for _, edge := range edges {
		if !matchesCypherEdgeType(edge, pattern.Edge.Type) {
			continue
		}
		row := make(CypherResultRow)
		bindCypherAlias(row, pattern.Node1.Alias, node1)
		bindCypherAlias(row, pattern.Node2.Alias, node2)
		bindCypherAlias(row, pattern.Edge.Alias, edge)
		if cypherRowMatchesWhere(q.Where, row) {
			results = append(results, row)
		}
		if pattern.Edge.Type != "" {
			break // A graph pair has at most one edge for a specific type.
		}
	}
	return results
}

func validateCypherQuery(q *cypher.Query) error {
	if q == nil {
		return fmt.Errorf("cypher query is nil")
	}
	if q.Match == nil || q.Match.Pattern == nil || q.Match.Pattern.Node1 == nil {
		return fmt.Errorf("cypher query must include a MATCH pattern")
	}
	if q.Return == nil || len(q.Return.Items) == 0 {
		return fmt.Errorf("cypher query must include at least one RETURN item")
	}

	pattern := q.Match.Pattern
	if err := validateCypherNodeType(pattern.Node1.Type); err != nil {
		return err
	}
	if pattern.Node2 != nil {
		if err := validateCypherNodeType(pattern.Node2.Type); err != nil {
			return err
		}
	}
	if pattern.Edge != nil {
		if err := validateCypherEdgeType(pattern.Edge.Type); err != nil {
			return err
		}
	}

	aliases, err := cypherPatternAliases(pattern)
	if err != nil {
		return err
	}

	for _, cond := range cypherWhereConditions(q) {
		kind, ok := aliases[cond.Alias]
		if !ok {
			return fmt.Errorf("unknown alias %q in WHERE; available aliases: %s", cond.Alias, formatCypherAliases(aliases))
		}
		if cond.Property == "" {
			return fmt.Errorf("WHERE condition for alias %q must specify a property, for example %s.Name", cond.Alias, cond.Alias)
		}
		if !validCypherProperty(kind, cond.Property) {
			return fmt.Errorf("unknown property %q for %s alias %q; valid properties: %s", cond.Property, kind, cond.Alias, formatCypherProperties(kind))
		}
		if !validCypherOperator(cond.Operator) {
			return fmt.Errorf("unsupported WHERE operator %q; valid operators: =, !=, CONTAINS", cond.Operator)
		}
	}

	for _, item := range q.Return.Items {
		kind, ok := aliases[item.Alias]
		if !ok {
			return fmt.Errorf("unknown alias %q in RETURN; available aliases: %s", item.Alias, formatCypherAliases(aliases))
		}
		if item.Property != "" && !validCypherProperty(kind, item.Property) {
			return fmt.Errorf("unknown property %q for %s alias %q; valid properties: %s", item.Property, kind, item.Alias, formatCypherProperties(kind))
		}
	}

	return nil
}

func cypherWhereConditions(q *cypher.Query) []*cypher.Condition {
	if q == nil || q.Where == nil {
		return nil
	}
	return q.Where.Conditions
}

func validateCypherNodeType(nodeType string) error {
	if nodeType == "" {
		return nil
	}
	if _, ok := validCypherNodeTypes[strings.ToUpper(nodeType)]; ok {
		return nil
	}
	return fmt.Errorf("unknown node type %q; valid node types: %s", nodeType, formatCypherSet(validCypherNodeTypes))
}

func validateCypherEdgeType(edgeType string) error {
	if edgeType == "" {
		return nil
	}
	if _, ok := validCypherEdgeTypes[strings.ToUpper(edgeType)]; ok {
		return nil
	}
	return fmt.Errorf("unknown edge type %q; valid edge types: %s", edgeType, formatCypherSet(validCypherEdgeTypes))
}

func cypherPatternAliases(pattern *cypher.Pattern) (map[string]string, error) {
	aliases := make(map[string]string)
	if err := addCypherAlias(aliases, pattern.Node1.Alias, cypherAliasNode); err != nil {
		return nil, err
	}
	if pattern.Edge != nil {
		if err := addCypherAlias(aliases, pattern.Edge.Alias, cypherAliasEdge); err != nil {
			return nil, err
		}
	}
	if pattern.Node2 != nil {
		if err := addCypherAlias(aliases, pattern.Node2.Alias, cypherAliasNode); err != nil {
			return nil, err
		}
	}
	return aliases, nil
}

func addCypherAlias(aliases map[string]string, alias string, kind string) error {
	if alias == "" {
		return nil
	}
	if existing, ok := aliases[alias]; ok {
		return fmt.Errorf("duplicate alias %q in MATCH pattern (already used for %s)", alias, existing)
	}
	aliases[alias] = kind
	return nil
}

func validCypherProperty(kind string, property string) bool {
	switch kind {
	case cypherAliasNode:
		_, ok := validCypherNodeProperties[strings.ToLower(property)]
		return ok
	case cypherAliasEdge:
		_, ok := validCypherEdgeProperties[strings.ToLower(property)]
		return ok
	default:
		return false
	}
}

func validCypherOperator(operator string) bool {
	switch strings.ToUpper(operator) {
	case "=", "!=", "CONTAINS":
		return true
	default:
		return false
	}
}

func bindCypherAlias(row CypherResultRow, alias string, val interface{}) {
	if alias != "" {
		row[alias] = val
	}
}

func matchesCypherNodeType(node *parser.Node, nodeType string) bool {
	if node == nil {
		return false
	}
	return nodeType == "" || strings.EqualFold(string(node.Type), nodeType)
}

func matchesCypherEdgeType(edge *parser.Edge, edgeType string) bool {
	if edge == nil {
		return false
	}
	return edgeType == "" || strings.EqualFold(string(edge.Type), edgeType)
}

func cypherRowMatchesWhere(where *cypher.WhereClause, row CypherResultRow) bool {
	if where == nil {
		return true
	}
	for _, cond := range where.Conditions {
		val, ok := row[cond.Alias]
		if !ok {
			return false
		}

		propVal, ok := cypherPropertyString(val, cond.Property)
		if !ok {
			return false
		}

		switch strings.ToUpper(cond.Operator) {
		case "=":
			if propVal != cond.Value {
				return false
			}
		case "!=":
			if propVal == cond.Value {
				return false
			}
		case "CONTAINS":
			if !strings.Contains(propVal, cond.Value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func cypherPropertyString(val interface{}, property string) (string, bool) {
	propVal, ok := cypherPropertyValue(val, property)
	if !ok {
		return "", false
	}
	str, ok := propVal.(string)
	return str, ok
}

func cypherPropertyValue(val interface{}, property string) (interface{}, bool) {
	switch v := val.(type) {
	case *parser.Node:
		return cypherNodeProperty(v, property)
	case *parser.Edge:
		return cypherEdgeProperty(v, property)
	default:
		return nil, false
	}
}

func cypherNodeProperty(node *parser.Node, property string) (interface{}, bool) {
	if node == nil {
		return nil, false
	}
	switch strings.ToLower(property) {
	case "name":
		return node.Name, true
	case "id":
		return node.ID, true
	case "pkgpath":
		return node.PkgPath, true
	case "filepath":
		return node.FilePath, true
	case "type":
		return string(node.Type), true
	case "repoid":
		return node.RepoID, true
	default:
		return nil, false
	}
}

func cypherEdgeProperty(edge *parser.Edge, property string) (interface{}, bool) {
	if edge == nil {
		return nil, false
	}
	switch strings.ToLower(property) {
	case "type":
		return string(edge.Type), true
	case "from":
		return edge.From, true
	case "to":
		return edge.To, true
	case "repoid":
		return edge.RepoID, true
	default:
		return nil, false
	}
}

func formatCypherAliases(aliases map[string]string) string {
	if len(aliases) == 0 {
		return "<none>"
	}
	names := make([]string, 0, len(aliases))
	for alias := range aliases {
		names = append(names, alias)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func formatCypherProperties(kind string) string {
	switch kind {
	case cypherAliasNode:
		return formatCypherSet(validCypherNodeProperties)
	case cypherAliasEdge:
		return formatCypherSet(validCypherEdgeProperties)
	default:
		return "<none>"
	}
}

func formatCypherSet(values map[string]struct{}) string {
	items := make([]string, 0, len(values))
	for item := range values {
		items = append(items, item)
	}
	sort.Strings(items)
	return strings.Join(items, ", ")
}
