package policy

import (
	"fmt"
	"strings"
)

// Parse parses a policy expression into an AST. It accepts only the safe grammar:
//
//	expr       := or
//	or         := and ("or" and)*
//	and        := not ("and" not)*
//	not        := "not" not | comparison
//	comparison := primary ( (op) primary | "in" "(" list ")" )?
//	primary    := "(" expr ")" | auth "." ("uid"|"role") "(" ")" | column | literal
//	literal    := string | number | "true" | "false" | "null"
func Parse(src string) (Node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tEOF {
		return nil, fmt.Errorf("unexpected trailing input at %d", p.cur().pos)
	}
	return node, nil
}

type parser struct {
	toks []token
	i    int
}

func (p *parser) cur() token  { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) isKeyword(kw string) bool {
	t := p.cur()
	return t.kind == tIdent && strings.EqualFold(t.str, kw)
}

func (p *parser) parseOr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("or") {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("and") {
		p.next()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseNot() (Node, error) {
	if p.isKeyword("not") {
		p.next()
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Not{X: x}, nil
	}
	return p.parseComparison()
}

var compareOps = map[string]bool{"=": true, "<>": true, "<": true, "<=": true, ">": true, ">=": true}

func (p *parser) parseComparison() (Node, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.cur().kind == tOp && compareOps[p.cur().str] {
		op := p.next().str
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &Binary{Op: op, Left: left, Right: right}, nil
	}
	if p.isKeyword("in") {
		p.next()
		if p.next().kind != tLParen {
			return nil, fmt.Errorf("expected ( after in")
		}
		var elems []Node
		for {
			e, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
			if p.cur().kind == tComma {
				p.next()
				continue
			}
			break
		}
		if p.next().kind != tRParen {
			return nil, fmt.Errorf("expected ) to close in list")
		}
		if len(elems) == 0 {
			return nil, fmt.Errorf("in list must not be empty")
		}
		return &In{Left: left, Elems: elems}, nil
	}
	return left, nil
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.cur()
	switch {
	case t.kind == tLParen:
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.next().kind != tRParen {
			return nil, fmt.Errorf("expected )")
		}
		return inner, nil
	case t.kind == tString:
		p.next()
		return &Literal{Value: t.str}, nil
	case t.kind == tNumber:
		p.next()
		return &Literal{Value: t.num}, nil
	case t.kind == tIdent:
		return p.parseIdentLike()
	default:
		return nil, fmt.Errorf("unexpected token at %d", t.pos)
	}
}

func (p *parser) parseIdentLike() (Node, error) {
	t := p.next()
	switch strings.ToLower(t.str) {
	case "true":
		return &Literal{Value: true}, nil
	case "false":
		return &Literal{Value: false}, nil
	case "null":
		return &Literal{IsNull: true}, nil
	case "auth":
		return p.parseAuthCall()
	case "and", "or", "not", "in":
		return nil, fmt.Errorf("unexpected keyword %q at %d", t.str, t.pos)
	default:
		return &Column{Name: t.str}, nil
	}
}

func (p *parser) parseAuthCall() (Node, error) {
	if p.next().kind != tDot {
		return nil, fmt.Errorf("expected .fn after auth")
	}
	fnTok := p.next()
	if fnTok.kind != tIdent {
		return nil, fmt.Errorf("expected auth function name")
	}
	fn := strings.ToLower(fnTok.str)
	if fn != "uid" && fn != "role" {
		return nil, fmt.Errorf("unknown auth function %q (allowed: uid, role)", fnTok.str)
	}
	if p.next().kind != tLParen {
		return nil, fmt.Errorf("expected ( after auth.%s", fn)
	}
	if p.next().kind != tRParen {
		return nil, fmt.Errorf("expected ) after auth.%s(", fn)
	}
	return &Auth{Fn: fn}, nil
}
