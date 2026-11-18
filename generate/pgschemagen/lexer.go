// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type tokenKind int

const (
	tokSpace tokenKind = iota
	tokComment
	tokIdent
	tokQuotedIdent
	tokString
	tokDollarString
	tokNumber
	tokPunct
)

// Concatenating token text reproduces the input, including literal contents.
type token struct {
	kind tokenKind
	text string
}

func (t token) isTrivia() bool { return t.kind == tokSpace || t.kind == tokComment }
func (t token) is(s string) bool {
	return t.kind == tokIdent && strings.EqualFold(t.text, s)
}
func (t token) isPunct(s string) bool { return t.kind == tokPunct && t.text == s }
func (t token) name() string {
	switch t.kind {
	case tokIdent:
		return strings.ToLower(t.text)
	case tokQuotedIdent:
		q := t.text[:1]
		return strings.ReplaceAll(t.text[1:len(t.text)-1], q+q, q)
	}
	return ""
}

// lex is based on the PoC lexer. Input validation, nested comments and dollar
// delimiter scanning are deliberately stricter than that reference.
func lex(src string) ([]token, error) {
	if !utf8.ValidString(src) || strings.ContainsRune(src, 0) {
		return nil, fmt.Errorf("SQL contains invalid UTF-8 or NUL")
	}
	var toks []token
	for i := 0; i < len(src); {
		start := i
		var kind tokenKind
		switch c := src[i]; {
		case strings.IndexByte(" \t\n\r", c) >= 0:
			for i < len(src) && strings.IndexByte(" \t\n\r", src[i]) >= 0 {
				i++
			}
			kind = tokSpace
		case strings.HasPrefix(src[i:], "--"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
			kind = tokComment
		case strings.HasPrefix(src[i:], "/*"):
			i += 2
			depth := 1
			for i < len(src) && depth > 0 {
				switch {
				case strings.HasPrefix(src[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(src[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("unterminated comment at offset %d", start)
			}
			kind = tokComment
		case c == '\'' || c == '"' || c == '`':
			end, err := scanQuoted(src, i, c)
			if err != nil {
				return nil, err
			}
			i = end
			kind = tokString
			if c != '\'' {
				kind = tokQuotedIdent
			}
		case c == '$':
			j := i + 1
			if j < len(src) && isIdentStart(src[j]) {
				for j < len(src) && isIdentPart(src[j]) {
					j++
				}
			}
			if j == len(src) || src[j] != '$' {
				return nil, fmt.Errorf("unsupported parameter or dollar delimiter at offset %d", i)
			}
			tag := src[i : j+1]
			end := strings.Index(src[j+1:], tag)
			if end < 0 {
				return nil, fmt.Errorf("unterminated dollar-quoted string at offset %d", i)
			}
			i = j + 1 + end + len(tag)
			kind = tokDollarString
		case c >= '0' && c <= '9':
			for i < len(src) && src[i] >= '0' && src[i] <= '9' {
				i++
			}
			if i < len(src) && src[i] == '.' {
				i++
				for i < len(src) && src[i] >= '0' && src[i] <= '9' {
					i++
				}
			}
			if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
				i++
				if i < len(src) && (src[i] == '+' || src[i] == '-') {
					i++
				}
				digits := i
				for i < len(src) && src[i] >= '0' && src[i] <= '9' {
					i++
				}
				if i == digits {
					return nil, fmt.Errorf("invalid number at offset %d", start)
				}
			}
			if i < len(src) && (isIdentPart(src[i]) || src[i] == '.') {
				return nil, fmt.Errorf("unsupported numeric literal at offset %d", start)
			}
			kind = tokNumber
		case isIdentStart(c):
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			kind = tokIdent
		default:
			if strings.IndexByte("(),;.+-*/%=<>!|:", c) < 0 {
				return nil, fmt.Errorf("unsupported SQL character %q at offset %d", c, i)
			}
			i++
			for _, op := range []string{"!=", "<>", "<=", ">=", "||", "==", "::"} {
				if strings.HasPrefix(src[start:], op) {
					i = start + len(op)
					break
				}
			}
			kind = tokPunct
		}
		toks = append(toks, token{kind, src[start:i]})
	}
	return toks, nil
}

func scanQuoted(src string, i int, quote byte) (int, error) {
	for j := i + 1; j < len(src); j++ {
		if src[j] != quote {
			continue
		}
		if j+1 < len(src) && src[j+1] == quote {
			j++
			continue
		}
		return j + 1, nil
	}
	return 0, fmt.Errorf("unterminated quoted text at offset %d", i)
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

func render(toks []token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.text)
	}
	return b.String()
}

func significant(toks []token) []token {
	var out []token
	for _, t := range toks {
		if !t.isTrivia() {
			out = append(out, t)
		}
	}
	return out
}

func nextSignificant(toks []token, i int) int {
	for i < len(toks) && toks[i].isTrivia() {
		i++
	}
	return i
}

func trimTrivia(toks []token) []token {
	start, end := nextSignificant(toks, 0), len(toks)
	for end > start && toks[end-1].isTrivia() {
		end--
	}
	return toks[start:end]
}

func matchParen(toks []token, i int) (int, error) {
	if i < 0 || i >= len(toks) || !toks[i].isPunct("(") {
		return 0, fmt.Errorf("expected opening parenthesis")
	}
	depth := 0
	for j := i; j < len(toks); j++ {
		switch {
		case toks[j].isPunct("("):
			depth++
		case toks[j].isPunct(")"):
			depth--
			if depth == 0 {
				return j, nil
			}
		}
	}
	return 0, fmt.Errorf("unbalanced parenthesis")
}

func splitTopLevel(toks []token) ([][]token, error) {
	var parts [][]token
	depth, start := 0, 0
	for i, t := range toks {
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unexpected closing parenthesis")
			}
		case t.isPunct(",") && depth == 0:
			parts = append(parts, toks[start:i])
			start = i + 1
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parenthesis")
	}
	parts = append(parts, toks[start:])
	for _, part := range parts {
		if len(trimTrivia(part)) == 0 {
			return nil, fmt.Errorf("empty comma-separated item")
		}
	}
	return parts, nil
}
