// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"strconv"
	"strings"
)

// param describes the source of one PostgreSQL parameter ($1, $2, ...) in a
// rewritten query.
type param struct {
	// name is the sqlair parameter name, without the leading "@". It is empty
	// for a positional "?" placeholder.
	name string
	// position is the 1-based index among the "?" placeholders. It is zero for
	// named parameters.
	position int
}

// rewrite converts "?" and "@sqlair_N" placeholders in query to "$N". A named
// placeholder that is repeated maps to a single "$N". Placeholders inside
// string literals, quoted identifiers, comments and dollar-quoted strings are
// left untouched. The returned params slice has one entry per "$N", in order.
//
// sqlair renders an empty slice input as "IN ()", which SQLite accepts and
// PostgreSQL rejects. "IN ()" is rewritten to "= ANY ('{}')" and "NOT IN ()"
// to "<> ALL ('{}')", which keep the SQLite results, including for a NULL
// left operand. The rewrite assumes the IN operand is not itself the right
// side of a comparison, as in "a = b IN ()", because "=" does not chain.
//
// A query that contains nothing to rewrite is returned unchanged with no
// params.
func rewrite(query string) (string, []param) {
	var (
		sb      strings.Builder
		params  []param
		named   = map[string]int{}
		qcount  int
		i       int
		changed bool
	)
	n := len(query)

	for i < n {
		c := query[i]
		switch {
		case c == '\'':
			// An E'...' string honours backslash escapes.
			escapes := i > 0 && (query[i-1] == 'E' || query[i-1] == 'e') &&
				(i < 2 || !isIdentChar(query[i-2]))
			end := skipQuoted(query, i, '\'', escapes)
			sb.WriteString(query[i:end])
			i = end
		case c == '"':
			end := skipQuoted(query, i, '"', false)
			sb.WriteString(query[i:end])
			i = end
		case c == '-' && i+1 < n && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				end = n
			} else {
				end += i
			}
			sb.WriteString(query[i:end])
			i = end
		case c == '/' && i+1 < n && query[i+1] == '*':
			end := skipBlockComment(query, i)
			sb.WriteString(query[i:end])
			i = end
		case c == '$':
			if end, ok := skipDollarQuoted(query, i); ok {
				sb.WriteString(query[i:end])
				i = end
				continue
			}
			sb.WriteByte(c)
			i++
		case c == '?':
			qcount++
			params = append(params, param{position: qcount})
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(len(params)))
			changed = true
			i++
		case c == '@':
			end, ok := scanSqlairName(query, i)
			if !ok {
				sb.WriteByte(c)
				i++
				continue
			}
			name := query[i+1 : end]
			idx, seen := named[name]
			if !seen {
				params = append(params, param{name: name})
				idx = len(params)
				named[name] = idx
			}
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(idx))
			changed = true
			i = end
		case c == '(':
			if end, ok := skipEmptyParens(query, i); ok {
				if out, ok := rewriteEmptyIn(sb.String()); ok {
					sb.Reset()
					sb.WriteString(out)
					changed = true
					i = end
					continue
				}
			}
			sb.WriteByte(c)
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}

	if !changed {
		return query, nil
	}
	return sb.String(), params
}

// skipQuoted returns the index just past the quoted section starting at
// start. A doubled quote character is an escaped quote. If backslash is true
// a backslash escapes the following character. An unterminated section runs
// to the end of the input.
func skipQuoted(s string, start int, quote byte, backslash bool) int {
	i := start + 1
	for i < len(s) {
		switch {
		case backslash && s[i] == '\\':
			i += 2
		case s[i] == quote:
			if i+1 < len(s) && s[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// skipBlockComment returns the index just past the block comment starting at
// start. PostgreSQL block comments nest.
func skipBlockComment(s string, start int) int {
	depth := 0
	i := start
	for i < len(s) {
		switch {
		case strings.HasPrefix(s[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(s[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(s)
}

// skipDollarQuoted returns the index just past a dollar-quoted string that
// starts at start, and true, if there is one. "$1" style parameters and "$"
// inside identifiers are not dollar quotes.
func skipDollarQuoted(s string, start int) (int, bool) {
	if start > 0 && isIdentChar(s[start-1]) {
		return 0, false
	}
	i := start + 1
	for i < len(s) && s[i] != '$' {
		c := s[i]
		first := i == start+1
		if !(c == '_' || isLetter(c) || c >= 0x80 || (!first && isDigit(c))) {
			return 0, false
		}
		i++
	}
	if i >= len(s) {
		return 0, false
	}
	tag := s[start : i+1]
	end := strings.Index(s[i+1:], tag)
	if end < 0 {
		return len(s), true
	}
	return i + 1 + end + len(tag), true
}

// scanSqlairName reports whether a sqlair placeholder, "@sqlair_" followed by
// digits, starts at start. It returns the index just past it. Other uses of
// "@", such as the PostgreSQL operators "@>" and "@@", are not placeholders.
func scanSqlairName(s string, start int) (int, bool) {
	const prefix = "@sqlair_"
	if start > 0 && isIdentChar(s[start-1]) {
		return 0, false
	}
	if !strings.HasPrefix(s[start:], prefix) {
		return 0, false
	}
	i := start + len(prefix)
	digits := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == digits || (i < len(s) && isIdentChar(s[i])) {
		return 0, false
	}
	return i, true
}

// skipEmptyParens reports whether the "(" at start is followed by only
// whitespace and a ")". It returns the index just past the ")".
func skipEmptyParens(s string, start int) (int, bool) {
	i := start + 1
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if i < len(s) && s[i] == ')' {
		return i + 1, true
	}
	return 0, false
}

// rewriteEmptyIn returns prefix with a trailing "IN" or "NOT IN" keyword
// replaced by the PostgreSQL form of a comparison with an empty list. It
// returns false if prefix does not end with the IN keyword.
func rewriteEmptyIn(prefix string) (string, bool) {
	inStart, inEnd, ok := lastWord(prefix, len(prefix))
	if !ok || !strings.EqualFold(prefix[inStart:inEnd], "IN") {
		return "", false
	}
	notStart, notEnd, ok := lastWord(prefix, inStart)
	if ok && strings.EqualFold(prefix[notStart:notEnd], "NOT") {
		return prefix[:notStart] + "<> ALL ('{}')", true
	}
	return prefix[:inStart] + "= ANY ('{}')", true
}

// lastWord returns the bounds of the keyword that ends s[:end], ignoring
// trailing whitespace. It returns false if s[:end] does not end with a word
// made only of letters, such as when it ends with a quoted identifier.
func lastWord(s string, end int) (int, int, bool) {
	for end > 0 && isSpace(s[end-1]) {
		end--
	}
	start := end
	for start > 0 && isLetter(s[start-1]) {
		start--
	}
	if start == end || (start > 0 && isIdentChar(s[start-1])) {
		return 0, 0, false
	}
	return start, end, true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isIdentChar(c byte) bool {
	return isLetter(c) || isDigit(c) || c == '_' || c == '$' || c >= 0x80
}
