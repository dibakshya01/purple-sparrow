package policy

import (
	"fmt"
	"strconv"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString
	tNumber
	tOp     // = <> < <= > >=
	tLParen // (
	tRParen // )
	tComma  // ,
	tDot    // .
)

type token struct {
	kind tokKind
	str  string  // ident text, string value, or operator
	num  float64 // for tNumber
	pos  int
}

// lex tokenizes a policy expression. It rejects any character outside the small
// grammar so malformed input fails fast rather than reaching SQL.
func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen, pos: i})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, pos: i})
			i++
		case c == ',':
			toks = append(toks, token{kind: tComma, pos: i})
			i++
		case c == '.':
			toks = append(toks, token{kind: tDot, pos: i})
			i++
		case c == '\'':
			s, next, err := lexString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tString, str: s, pos: i})
			i = next
		case c == '=':
			toks = append(toks, token{kind: tOp, str: "=", pos: i})
			i++
		case c == '<':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tOp, str: "<=", pos: i})
				i += 2
			} else if i+1 < len(src) && src[i+1] == '>' {
				toks = append(toks, token{kind: tOp, str: "<>", pos: i})
				i += 2
			} else {
				toks = append(toks, token{kind: tOp, str: "<", pos: i})
				i++
			}
		case c == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tOp, str: ">=", pos: i})
				i += 2
			} else {
				toks = append(toks, token{kind: tOp, str: ">", pos: i})
				i++
			}
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tOp, str: "<>", pos: i})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected %q at %d (did you mean !=?)", c, i)
			}
		case c == '-' || (c >= '0' && c <= '9'):
			n, next, err := lexNumber(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tNumber, num: n, pos: i})
			i = next
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, token{kind: tIdent, str: src[i:j], pos: i})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at %d", c, i)
		}
	}
	toks = append(toks, token{kind: tEOF, pos: len(src)})
	return toks, nil
}

func lexString(src string, i int) (string, int, error) {
	// src[i] == '\''. SQL-style doubled '' escapes a quote.
	var b strings.Builder
	j := i + 1
	for j < len(src) {
		if src[j] == '\'' {
			if j+1 < len(src) && src[j+1] == '\'' {
				b.WriteByte('\'')
				j += 2
				continue
			}
			return b.String(), j + 1, nil
		}
		b.WriteByte(src[j])
		j++
	}
	return "", 0, fmt.Errorf("unterminated string literal at %d", i)
}

func lexNumber(src string, i int) (float64, int, error) {
	j := i
	if src[j] == '-' {
		j++
	}
	for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
		j++
	}
	n, err := strconv.ParseFloat(src[i:j], 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid number %q at %d", src[i:j], i)
	}
	return n, j, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
