package cypher

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type TokenType int

const (
	TokenIllegal TokenType = iota
	TokenEOF

	// Identifiers and literals
	TokenIdent
	TokenString
	TokenNumber

	// Keywords
	TokenMatch
	TokenWhere
	TokenReturn
	TokenLimit
	TokenContains
	TokenAnd

	// Operators and punctuation
	TokenEq         // =
	TokenNeq        // !=
	TokenLParen     // (
	TokenRParen     // )
	TokenLBracket   // [
	TokenRBracket   // ]
	TokenColon      // :
	TokenDot        // .
	TokenComma      // ,
	TokenDash       // -
	TokenRightArrow // ->
	TokenLeftArrow  // <-
)

type Token struct {
	Type    TokenType
	Literal string
}

type Lexer struct {
	input        string
	position     int
	readPosition int
	ch           rune
}

func NewLexer(input string) *Lexer {
	l := &Lexer{input: input}
	l.readChar()
	return l
}

func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.ch = 0
	} else {
		r, width := utf8.DecodeRuneInString(l.input[l.readPosition:])
		l.ch = r
		l.position = l.readPosition
		l.readPosition += width
		return
	}
	l.position = l.readPosition
	l.readPosition++
}

func (l *Lexer) peekChar() rune {
	if l.readPosition >= len(l.input) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.input[l.readPosition:])
	return r
}

func (l *Lexer) NextToken() Token {
	var tok Token

	l.skipIgnored()

	switch l.ch {
	case '=':
		tok = Token{Type: TokenEq, Literal: string(l.ch)}
	case '!':
		if l.peekChar() == '=' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenNeq, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch)}
		}
	case '(':
		tok = Token{Type: TokenLParen, Literal: string(l.ch)}
	case ')':
		tok = Token{Type: TokenRParen, Literal: string(l.ch)}
	case '[':
		tok = Token{Type: TokenLBracket, Literal: string(l.ch)}
	case ']':
		tok = Token{Type: TokenRBracket, Literal: string(l.ch)}
	case ':':
		tok = Token{Type: TokenColon, Literal: string(l.ch)}
	case '.':
		tok = Token{Type: TokenDot, Literal: string(l.ch)}
	case ',':
		tok = Token{Type: TokenComma, Literal: string(l.ch)}
	case '-':
		if l.peekChar() == '>' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenRightArrow, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenDash, Literal: string(l.ch)}
		}
	case '<':
		if l.peekChar() == '-' {
			ch := l.ch
			l.readChar()
			tok = Token{Type: TokenLeftArrow, Literal: string(ch) + string(l.ch)}
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch)}
		}
	case '"', '\'':
		literal, ok := l.readString(l.ch)
		if !ok {
			tok = Token{Type: TokenIllegal, Literal: literal}
		} else {
			tok.Type = TokenString
			tok.Literal = literal
		}
	case 0:
		tok.Literal = ""
		tok.Type = TokenEOF
	default:
		if isLetter(l.ch) {
			tok.Literal = l.readIdentifier()
			tok.Type = lookupIdent(tok.Literal)
			return tok
		} else if isDigit(l.ch) {
			tok.Type = TokenNumber
			tok.Literal = l.readNumber()
			return tok
		} else {
			tok = Token{Type: TokenIllegal, Literal: string(l.ch)}
		}
	}

	l.readChar()
	return tok
}

func (l *Lexer) readIdentifier() string {
	position := l.position
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
	return l.input[position:l.position]
}

func (l *Lexer) readString(quote rune) (string, bool) {
	position := l.position + 1
	for {
		l.readChar()
		if l.ch == quote {
			break
		}
		if l.ch == 0 {
			return l.input[position:l.position], false
		}
	}
	return l.input[position:l.position], true
}

func (l *Lexer) readNumber() string {
	position := l.position
	for isDigit(l.ch) {
		l.readChar()
	}
	return l.input[position:l.position]
}

func (l *Lexer) skipIgnored() {
	for {
		for l.ch == ' ' || l.ch == '\t' || l.ch == '\n' || l.ch == '\r' {
			l.readChar()
		}

		if l.isLineCommentStart() {
			for l.ch != '\n' && l.ch != '\r' && l.ch != 0 {
				l.readChar()
			}
			continue
		}

		return
	}
}

func (l *Lexer) isLineCommentStart() bool {
	if l.ch != '-' || l.peekChar() != '-' {
		return false
	}
	for i := l.position - 1; i >= 0; i-- {
		switch l.input[i] {
		case '\n', '\r':
			return true
		case ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

func isLetter(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func isDigit(ch rune) bool {
	return unicode.IsDigit(ch)
}

func lookupIdent(ident string) TokenType {
	switch strings.ToUpper(ident) {
	case "MATCH":
		return TokenMatch
	case "WHERE":
		return TokenWhere
	case "RETURN":
		return TokenReturn
	case "LIMIT":
		return TokenLimit
	case "CONTAINS":
		return TokenContains
	case "AND":
		return TokenAnd
	default:
		return TokenIdent
	}
}
