package cypher

import (
	"testing"
)

func TestLexer(t *testing.T) {
	input := `MATCH (n:FUNC)-[r:CALLS]->(m:FUNC) WHERE n.Name = "main" RETURN n, m.PkgPath LIMIT 10`
	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenMatch, "MATCH"},
		{TokenLParen, "("},
		{TokenIdent, "n"},
		{TokenColon, ":"},
		{TokenIdent, "FUNC"},
		{TokenRParen, ")"},
		{TokenDash, "-"},
		{TokenLBracket, "["},
		{TokenIdent, "r"},
		{TokenColon, ":"},
		{TokenIdent, "CALLS"},
		{TokenRBracket, "]"},
		{TokenRightArrow, "->"},
		{TokenLParen, "("},
		{TokenIdent, "m"},
		{TokenColon, ":"},
		{TokenIdent, "FUNC"},
		{TokenRParen, ")"},
		{TokenWhere, "WHERE"},
		{TokenIdent, "n"},
		{TokenDot, "."},
		{TokenIdent, "Name"},
		{TokenEq, "="},
		{TokenString, "main"},
		{TokenReturn, "RETURN"},
		{TokenIdent, "n"},
		{TokenComma, ","},
		{TokenIdent, "m"},
		{TokenDot, "."},
		{TokenIdent, "PkgPath"},
		{TokenLimit, "LIMIT"},
		{TokenNumber, "10"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()

		if tok.Type != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%v, got=%v", i, tt.expectedType, tok.Type)
		}

		if tok.Literal != tt.expectedLiteral {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

func TestLexer_Operators(t *testing.T) {
	input := `!= = CONTAINS AND <- - ->`
	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenNeq, "!="},
		{TokenEq, "="},
		{TokenContains, "CONTAINS"},
		{TokenAnd, "AND"},
		{TokenLeftArrow, "<-"},
		{TokenDash, "-"},
		{TokenRightArrow, "->"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()

		if tok.Type != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%v, got=%v", i, tt.expectedType, tok.Type)
		}

		if tok.Literal != tt.expectedLiteral {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

func TestLexer_SkipsCypherLineComments(t *testing.T) {
	input := "-- find funcs\nMATCH (n:FUNC) RETURN n"
	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TokenMatch, "MATCH"},
		{TokenLParen, "("},
		{TokenIdent, "n"},
		{TokenColon, ":"},
		{TokenIdent, "FUNC"},
		{TokenRParen, ")"},
		{TokenReturn, "RETURN"},
		{TokenIdent, "n"},
		{TokenEOF, ""},
	}

	l := NewLexer(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Fatalf("tests[%d] - tokentype wrong. expected=%v, got=%v", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}
