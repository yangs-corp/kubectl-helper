package main

import (
	"fmt"
	"strings"
	"unicode"
)

type logMatcher func(string) bool

type filterExpr interface {
	match(string) bool
}

type termExpr string

func (e termExpr) match(line string) bool {
	return strings.Contains(line, string(e))
}

type andExpr struct {
	left  filterExpr
	right filterExpr
}

func (e andExpr) match(line string) bool {
	return e.left.match(line) && e.right.match(line)
}

type orExpr struct {
	left  filterExpr
	right filterExpr
}

func (e orExpr) match(line string) bool {
	return e.left.match(line) || e.right.match(line)
}

type filterTokenKind int

const (
	filterTokenEOF filterTokenKind = iota
	filterTokenTerm
	filterTokenAnd
	filterTokenOr
	filterTokenLParen
	filterTokenRParen
)

type filterToken struct {
	kind  filterTokenKind
	value string
}

func compileLogMatcher(query string) (logMatcher, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return func(string) bool { return true }, nil
	}
	if !usesFilterExpressionSyntax(query) {
		return func(line string) bool { return strings.Contains(line, query) }, nil
	}

	tokens, err := lexFilterExpression(query)
	if err != nil {
		return nil, err
	}
	parser := filterExprParser{tokens: tokens}
	expr, err := parser.parse()
	if err != nil {
		return nil, err
	}
	return expr.match, nil
}

func usesFilterExpressionSyntax(query string) bool {
	inQuote := false
	escaped := false
	for _, r := range query {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inQuote:
			escaped = true
		case r == '"':
			return true
		case !inQuote && (r == '(' || r == ')'):
			return true
		}
	}
	for _, field := range strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || r == '(' || r == ')'
	}) {
		if isFilterOperator(field, "AND") || isFilterOperator(field, "OR") {
			return true
		}
	}
	return false
}

func lexFilterExpression(query string) ([]filterToken, error) {
	var tokens []filterToken
	for i := 0; i < len(query); {
		r := rune(query[i])
		switch {
		case unicode.IsSpace(r):
			i++
		case query[i] == '(':
			tokens = append(tokens, filterToken{kind: filterTokenLParen, value: "("})
			i++
		case query[i] == ')':
			tokens = append(tokens, filterToken{kind: filterTokenRParen, value: ")"})
			i++
		case query[i] == '"':
			value, next, err := readQuotedFilterTerm(query, i+1)
			if err != nil {
				return nil, err
			}
			if value == "" {
				return nil, fmt.Errorf("empty quoted term")
			}
			tokens = append(tokens, filterToken{kind: filterTokenTerm, value: value})
			i = next
		default:
			start := i
			for i < len(query) && !unicode.IsSpace(rune(query[i])) && query[i] != '(' && query[i] != ')' {
				i++
			}
			value := query[start:i]
			switch {
			case isFilterOperator(value, "AND"):
				tokens = append(tokens, filterToken{kind: filterTokenAnd, value: value})
			case isFilterOperator(value, "OR"):
				tokens = append(tokens, filterToken{kind: filterTokenOr, value: value})
			default:
				tokens = append(tokens, filterToken{kind: filterTokenTerm, value: value})
			}
		}
	}
	tokens = append(tokens, filterToken{kind: filterTokenEOF})
	return tokens, nil
}

func readQuotedFilterTerm(query string, start int) (string, int, error) {
	var b strings.Builder
	escaped := false
	for i := start; i < len(query); i++ {
		ch := query[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return b.String(), i + 1, nil
		}
		b.WriteByte(ch)
	}
	return "", len(query), fmt.Errorf("unterminated quoted term")
}

func isFilterOperator(value, operator string) bool {
	return strings.EqualFold(value, operator)
}

type filterExprParser struct {
	tokens []filterToken
	pos    int
}

func (p *filterExprParser) parse() (filterExpr, error) {
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if tok := p.peek(); tok.kind != filterTokenEOF {
		return nil, fmt.Errorf("unexpected %q", tok.value)
	}
	return expr, nil
}

func (p *filterExprParser) parseOr() (filterExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.match(filterTokenOr) {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orExpr{left: left, right: right}
	}
	return left, nil
}

func (p *filterExprParser) parseAnd() (filterExpr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.match(filterTokenAnd) {
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = andExpr{left: left, right: right}
	}
	return left, nil
}

func (p *filterExprParser) parsePrimary() (filterExpr, error) {
	tok := p.peek()
	switch tok.kind {
	case filterTokenTerm:
		p.advance()
		return termExpr(tok.value), nil
	case filterTokenLParen:
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.match(filterTokenRParen) {
			return nil, fmt.Errorf("missing closing )")
		}
		return expr, nil
	default:
		return nil, fmt.Errorf("expected term")
	}
}

func (p *filterExprParser) match(kind filterTokenKind) bool {
	if p.peek().kind != kind {
		return false
	}
	p.advance()
	return true
}

func (p *filterExprParser) peek() filterToken {
	if p.pos >= len(p.tokens) {
		return filterToken{kind: filterTokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *filterExprParser) advance() filterToken {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}
