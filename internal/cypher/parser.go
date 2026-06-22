package cypher

import (
	"fmt"
	"strconv"
	"strings"
)

type Parser struct {
	l         *Lexer
	curToken  Token
	peekToken Token
}

func NewParser(l *Lexer) *Parser {
	p := &Parser{l: l}
	// Read two tokens, so curToken and peekToken are both set
	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *Parser) ParseQuery() (*Query, error) {
	q := &Query{}

	// Parse MATCH
	if p.curToken.Type != TokenMatch {
		return nil, fmt.Errorf("expected MATCH keyword, got %v", p.curToken.Literal)
	}
	p.nextToken()

	matchClause, err := p.parseMatch()
	if err != nil {
		return nil, err
	}
	q.Match = matchClause

	// Optional WHERE
	if p.curToken.Type == TokenWhere {
		p.nextToken()
		whereClause, err := p.parseWhere()
		if err != nil {
			return nil, err
		}
		q.Where = whereClause
	}

	// Parse RETURN
	if p.curToken.Type != TokenReturn {
		return nil, fmt.Errorf("expected RETURN keyword, got %v", p.curToken.Literal)
	}
	p.nextToken()

	returnClause, err := p.parseReturn()
	if err != nil {
		return nil, err
	}
	q.Return = returnClause

	// Optional LIMIT
	if p.curToken.Type == TokenLimit {
		p.nextToken()
		limit, err := p.parseLimit()
		if err != nil {
			return nil, err
		}
		q.Limit = limit
	}

	if p.curToken.Type != TokenEOF {
		return nil, fmt.Errorf("unexpected token after query: %q", p.curToken.Literal)
	}

	return q, nil
}

func (p *Parser) parseMatch() (*MatchClause, error) {
	node1, err := p.parseNodePattern()
	if err != nil {
		return nil, err
	}

	pattern := &Pattern{Node1: node1}

	// Parse optional Edge and Node2
	// Look for -, <- or ->
	if p.curToken.Type == TokenDash || p.curToken.Type == TokenLeftArrow {
		isLeftArrow := p.curToken.Type == TokenLeftArrow
		p.nextToken() // consume - or <-

		// Optional edge properties [alias:Type]
		var edgePattern *EdgePattern
		if p.curToken.Type == TokenLBracket {
			ep, err := p.parseEdgePatternBody()
			if err != nil {
				return nil, err
			}
			edgePattern = ep
		} else {
			edgePattern = &EdgePattern{}
		}

		if isLeftArrow {
			edgePattern.Direction = DirInbound
			if p.curToken.Type != TokenDash {
				return nil, fmt.Errorf("expected - after edge body for inbound, got %v", p.curToken.Literal)
			}
			p.nextToken()
		} else {
			// currently pointing forward, so next should be -> or -
			switch p.curToken.Type {
			case TokenRightArrow:
				edgePattern.Direction = DirOutbound
				p.nextToken()
			case TokenDash:
				edgePattern.Direction = DirAny
				p.nextToken()
			default:
				return nil, fmt.Errorf("expected -> or - after edge body, got %v", p.curToken.Literal)
			}
		}

		pattern.Edge = edgePattern

		node2, err := p.parseNodePattern()
		if err != nil {
			return nil, err
		}
		pattern.Node2 = node2
	}

	return &MatchClause{Pattern: pattern}, nil
}

func (p *Parser) parseNodePattern() (*NodePattern, error) {
	if p.curToken.Type != TokenLParen {
		return nil, fmt.Errorf("expected ( for node pattern, got %v", p.curToken.Literal)
	}
	p.nextToken()

	np := &NodePattern{}

	if p.curToken.Type == TokenIdent {
		np.Alias = p.curToken.Literal
		p.nextToken()
	}

	if p.curToken.Type == TokenColon {
		p.nextToken()
		if p.curToken.Type != TokenIdent {
			return nil, fmt.Errorf("expected node type after :, got %v", p.curToken.Literal)
		}
		np.Type = strings.ToUpper(p.curToken.Literal)
		p.nextToken()
	}

	if p.curToken.Type != TokenRParen {
		return nil, fmt.Errorf("expected ) for node pattern, got %v", p.curToken.Literal)
	}
	p.nextToken()

	return np, nil
}

func (p *Parser) parseEdgePatternBody() (*EdgePattern, error) {
	if p.curToken.Type != TokenLBracket {
		return nil, fmt.Errorf("expected [ for edge pattern, got %v", p.curToken.Literal)
	}
	p.nextToken()

	ep := &EdgePattern{}

	if p.curToken.Type == TokenIdent {
		ep.Alias = p.curToken.Literal
		p.nextToken()
	}

	if p.curToken.Type == TokenColon {
		p.nextToken()
		if p.curToken.Type != TokenIdent {
			return nil, fmt.Errorf("expected edge type after :, got %v", p.curToken.Literal)
		}
		ep.Type = strings.ToUpper(p.curToken.Literal)
		p.nextToken()
	}

	if p.curToken.Type != TokenRBracket {
		return nil, fmt.Errorf("expected ] for edge pattern, got %v", p.curToken.Literal)
	}
	p.nextToken()

	return ep, nil
}

func (p *Parser) parseWhere() (*WhereClause, error) {
	wc := &WhereClause{}

	for {
		cond, err := p.parseCondition()
		if err != nil {
			return nil, err
		}
		wc.Conditions = append(wc.Conditions, cond)

		if p.curToken.Type == TokenAnd {
			p.nextToken()
			continue
		}
		if p.curToken.Type == TokenIdent {
			continue // implicit AND between adjacent conditions
		}
		break
	}

	return wc, nil
}

func (p *Parser) parseCondition() (*Condition, error) {
	if p.curToken.Type != TokenIdent {
		return nil, fmt.Errorf("expected identifier in where clause, got %v", p.curToken.Literal)
	}
	alias := p.curToken.Literal
	p.nextToken()

	var property string
	if p.curToken.Type == TokenDot {
		p.nextToken()
		if p.curToken.Type != TokenIdent {
			return nil, fmt.Errorf("expected property after dot, got %v", p.curToken.Literal)
		}
		property = p.curToken.Literal
		p.nextToken()
	}

	var operator string
	switch p.curToken.Type {
	case TokenEq, TokenNeq:
		operator = p.curToken.Literal
		p.nextToken()
	case TokenContains:
		operator = "CONTAINS"
		p.nextToken()
	default:
		return nil, fmt.Errorf("expected operator (=, !=, CONTAINS), got %v", p.curToken.Literal)
	}

	if p.curToken.Type != TokenString {
		return nil, fmt.Errorf("expected string value after operator, got %v", p.curToken.Literal)
	}
	val := p.curToken.Literal
	p.nextToken()

	return &Condition{
		Alias:    alias,
		Property: property,
		Operator: operator,
		Value:    val,
	}, nil
}

func (p *Parser) parseReturn() (*ReturnClause, error) {
	rc := &ReturnClause{}

	for {
		if p.curToken.Type != TokenIdent {
			return nil, fmt.Errorf("expected identifier in return clause, got %v", p.curToken.Literal)
		}
		alias := p.curToken.Literal
		p.nextToken()

		var property string
		if p.curToken.Type == TokenDot {
			p.nextToken()
			if p.curToken.Type != TokenIdent {
				return nil, fmt.Errorf("expected property after dot, got %v", p.curToken.Literal)
			}
			property = p.curToken.Literal
			p.nextToken()
		}

		rc.Items = append(rc.Items, &ReturnItem{
			Alias:    alias,
			Property: property,
		})

		if p.curToken.Type == TokenComma {
			p.nextToken()
			continue
		}
		break
	}

	return rc, nil
}

func (p *Parser) parseLimit() (int, error) {
	if p.curToken.Type != TokenNumber {
		return 0, fmt.Errorf("expected number after LIMIT, got %v", p.curToken.Literal)
	}
	val, err := strconv.Atoi(p.curToken.Literal)
	if err != nil {
		return 0, err
	}
	if val <= 0 {
		return 0, fmt.Errorf("LIMIT must be greater than 0")
	}
	p.nextToken()
	return val, nil
}
