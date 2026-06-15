package cypher

// Query represents a parsed Cypher query.
type Query struct {
	Match  *MatchClause
	Where  *WhereClause
	Return *ReturnClause
	Limit  int
}

// MatchClause represents the MATCH part of the query.
type MatchClause struct {
	Pattern *Pattern
}

// Pattern represents a graph pattern: (Node1)-[Edge]->(Node2)
// Currently restricted to a single hop for simplicity.
type Pattern struct {
	Node1 *NodePattern
	Edge  *EdgePattern
	Node2 *NodePattern
}

// NodePattern represents a node in the pattern: (alias:Type)
type NodePattern struct {
	Alias string
	Type  string // e.g., FUNC, PACKAGE, FOLDER
}

// EdgeDirection specifies the direction of the edge pattern.
type EdgeDirection int

const (
	DirOutbound EdgeDirection = iota // -[...]->
	DirInbound                       // <-[...]-
	DirAny                           // -[...]-
)

// EdgePattern represents a relationship: -[alias:Type]->
type EdgePattern struct {
	Alias     string
	Type      string // e.g., CALLS, SPAWNS
	Direction EdgeDirection
}

// WhereClause represents the WHERE part of the query.
type WhereClause struct {
	Conditions []*Condition // implicitly ANDed
}

// Condition represents a single filter: alias.Property Operator Value
type Condition struct {
	Alias    string
	Property string // Name, ID, PkgPath, FilePath
	Operator string // =, !=, CONTAINS
	Value    string
}

// ReturnItem represents an item to return: alias or alias.property
type ReturnItem struct {
	Alias    string
	Property string // optional
}

// ReturnClause represents the RETURN part of the query.
type ReturnClause struct {
	Items []*ReturnItem
}
