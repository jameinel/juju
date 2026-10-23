// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"strings"
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

// token is a lexical token. Concatenating the text of all tokens reproduces
// the input exactly, so rewrites preserve layout and comments.
type token struct {
	kind tokenKind
	text string
}

// isTrivia reports whether the token is whitespace or a comment.
func (t token) isTrivia() bool {
	return t.kind == tokSpace || t.kind == tokComment
}

// is reports whether the token is the unquoted keyword or identifier kw,
// compared case-insensitively.
func (t token) is(kw string) bool {
	return t.kind == tokIdent && strings.EqualFold(t.text, kw)
}

// isPunct reports whether the token is the punctuation p.
func (t token) isPunct(p string) bool {
	return t.kind == tokPunct && t.text == p
}

// name returns the identifier the token names, without quotes, or "" if the
// token is not an identifier.
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

// lex splits SQL text into tokens. It understands both SQLite and the subset
// of PostgreSQL syntax that pgschemagen emits (dollar-quoted strings).
func lex(src string) ([]token, error) {
	var toks []token
	for i := 0; i < len(src); {
		c := src[i]
		start := i
		var kind tokenKind
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
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
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment at offset %d", i)
			}
			i += end + 4
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
		case c == '$' && i+1 < len(src) && (src[i+1] == '$' || isIdentStart(src[i+1])):
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			if j >= len(src) || src[j] != '$' {
				i = j
				kind = tokPunct
				break
			}
			tag := src[i : j+1]
			end := strings.Index(src[j+1:], tag)
			if end < 0 {
				return nil, fmt.Errorf("unterminated dollar-quoted string at offset %d", i)
			}
			i = j + 1 + end + len(tag)
			kind = tokDollarString
		case c >= '0' && c <= '9':
			for i < len(src) && (isIdentPart(src[i]) || src[i] == '.') {
				i++
			}
			kind = tokNumber
		case isIdentStart(c):
			for i < len(src) && isIdentPart(src[i]) {
				i++
			}
			kind = tokIdent
		default:
			i++
			for _, op := range []string{"!=", "<>", "<=", ">=", "||", "==", "::"} {
				if strings.HasPrefix(src[start:], op) {
					i = start + len(op)
					break
				}
			}
			kind = tokPunct
		}
		toks = append(toks, token{kind: kind, text: src[start:i]})
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
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// render concatenates the text of the tokens.
func render(toks []token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.text)
	}
	return b.String()
}

// nextSignificant returns the index of the first non-trivia token at or after
// i, or len(toks).
func nextSignificant(toks []token, i int) int {
	for i < len(toks) && toks[i].isTrivia() {
		i++
	}
	return i
}

// prevSignificant returns the index of the last non-trivia token before i, or
// -1.
func prevSignificant(toks []token, i int) int {
	i--
	for i >= 0 && toks[i].isTrivia() {
		i--
	}
	return i
}

// matchParen returns the index of the ")" that closes the "(" at i.
func matchParen(toks []token, i int) (int, error) {
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

// splitTopLevel splits toks on commas that are not nested in parentheses.
func splitTopLevel(toks []token) [][]token {
	var parts [][]token
	depth, start := 0, 0
	for i, t := range toks {
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth--
		case t.isPunct(",") && depth == 0:
			parts = append(parts, toks[start:i])
			start = i + 1
		}
	}
	return append(parts, toks[start:])
}

// trimTrivia removes leading and trailing whitespace and comments.
func trimTrivia(toks []token) []token {
	start := nextSignificant(toks, 0)
	end := prevSignificant(toks, len(toks)) + 1
	if start >= end {
		return nil
	}
	return toks[start:end]
}
