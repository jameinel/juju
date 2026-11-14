// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"errors"
	"strconv"
	"strings"
)

type param struct {
	name     string
	position int
}

// rewrite only translates parameters, never SQL expressions. In particular,
// invalid empty IN lists must remain visible to the regression tests.
func rewrite(query string) (string, []param, error) {
	var out strings.Builder
	var params []param
	named := make(map[string]int)
	positional := 0
	native := false
	for i := 0; i < len(query); {
		start := i
		switch {
		case query[i] == '\'' || query[i] == '"':
			escape := query[i] == '\'' && i > 0 &&
				(query[i-1] == 'e' || query[i-1] == 'E') &&
				(i < 2 || !isIdentChar(query[i-2]))
			i = skipQuoted(query, i, query[i], escape)
		case strings.HasPrefix(query[i:], "--"):
			if end := strings.IndexByte(query[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(query)
			}
		case strings.HasPrefix(query[i:], "/*"):
			i = skipBlockComment(query, i)
		case query[i] == '$':
			if end, ok := skipDollarQuoted(query, i); ok {
				i = end
			} else {
				native = native || (i+1 < len(query) && isDigit(query[i+1]) &&
					(i == 0 || !isIdentChar(query[i-1])))
				i++
			}
		case query[i] == '?':
			positional++
			params = append(params, param{position: positional})
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(len(params)))
			i++
			continue
		case query[i] == '@':
			end, ok := scanSqlairName(query, i)
			if !ok {
				i++
				break
			}
			name := query[i+1 : end]
			index, exists := named[name]
			if !exists {
				params = append(params, param{name: name})
				index = len(params)
				named[name] = index
			}
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(index))
			i = end
			continue
		default:
			i++
		}
		out.WriteString(query[start:i])
	}
	if native && len(params) > 0 {
		return "", nil, errors.New("cannot mix PostgreSQL numbered parameters with positional or Sqlair parameters")
	}
	if len(params) == 0 {
		return query, nil, nil
	}
	return out.String(), params, nil
}

func skipQuoted(s string, start int, quote byte, escape bool) int {
	for i := start + 1; i < len(s); {
		switch {
		case escape && s[i] == '\\':
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

func skipBlockComment(s string, start int) int {
	depth := 0
	for i := start; i < len(s); {
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

func skipDollarQuoted(s string, start int) (int, bool) {
	if start > 0 && isIdentChar(s[start-1]) {
		return 0, false
	}
	i := start + 1
	for i < len(s) && s[i] != '$' {
		c := s[i]
		if !(isLetter(c) || c == '_' || c >= 0x80 || (i > start+1 && isDigit(c))) {
			return 0, false
		}
		i++
	}
	if i == len(s) {
		return 0, false
	}
	tag := s[start : i+1]
	end := strings.Index(s[i+1:], tag)
	if end < 0 {
		return len(s), true
	}
	return i + 1 + end + len(tag), true
}

func scanSqlairName(s string, start int) (int, bool) {
	const prefix = "@sqlair_"
	if (start > 0 && isIdentChar(s[start-1])) || !strings.HasPrefix(s[start:], prefix) {
		return 0, false
	}
	i := start + len(prefix)
	first := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == first || (i < len(s) && isIdentChar(s[i])) {
		return 0, false
	}
	return i, true
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentChar(c byte) bool {
	return isLetter(c) || isDigit(c) || c == '_' || c == '$' || c >= 0x80
}
