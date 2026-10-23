// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

// statement is one SQL statement. lead holds the whitespace and comments
// before it, body runs from its first significant token up to, but not
// including, the terminating semicolon.
type statement struct {
	lead       []token
	body       []token
	terminated bool
}

func (s statement) text() string {
	out := render(s.lead) + render(s.body)
	if s.terminated {
		out += ";"
	}
	return out
}

// keywords returns up to n leading significant tokens of the body.
func (s statement) keywords(n int) []token {
	var out []token
	for _, t := range s.body {
		if len(out) == n {
			break
		}
		if !t.isTrivia() {
			out = append(out, t)
		}
	}
	return out
}

// isCreate reports whether the statement is CREATE [modifiers] <object>,
// where modifiers are UNIQUE, TEMP or TEMPORARY.
func (s statement) isCreate(object string) bool {
	kw := s.keywords(4)
	if len(kw) < 2 || !kw[0].is("CREATE") {
		return false
	}
	for _, t := range kw[1:] {
		if t.is(object) {
			return true
		}
		if !t.is("UNIQUE") && !t.is("TEMP") && !t.is("TEMPORARY") && !t.is("OR") && !t.is("REPLACE") {
			return false
		}
	}
	return false
}

// splitStatements splits a script into statements. In SQLite mode, trigger
// bodies (BEGIN ... END) are kept whole. A final statement with an empty body
// holds any trailing whitespace and comments.
func splitStatements(toks []token, sqlite bool) []statement {
	var (
		stmts     []statement
		cur       statement
		inTrigger bool
		inBody    bool
		caseDepth int
		sawEnd    bool
	)
	for _, t := range toks {
		if len(cur.body) == 0 && t.isTrivia() {
			cur.lead = append(cur.lead, t)
			continue
		}
		if t.isPunct(";") && (!inTrigger || sawEnd) {
			cur.terminated = true
			stmts = append(stmts, cur)
			cur = statement{}
			inTrigger, inBody, caseDepth, sawEnd = false, false, 0, false
			continue
		}
		cur.body = append(cur.body, t)
		if len(cur.body) == 1 {
			continue
		}
		if sqlite && !inTrigger && cur.isCreate("TRIGGER") {
			inTrigger = true
		}
		if !inTrigger || t.isTrivia() {
			continue
		}
		switch {
		case !inBody && t.is("BEGIN"):
			inBody = true
		case inBody && t.is("CASE"):
			caseDepth++
		case inBody && t.is("END"):
			if caseDepth > 0 {
				caseDepth--
			} else {
				sawEnd = true
			}
		}
	}
	if len(cur.lead) > 0 || len(cur.body) > 0 {
		stmts = append(stmts, cur)
	}
	return stmts
}
