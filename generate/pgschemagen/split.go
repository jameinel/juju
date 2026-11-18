// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import "fmt"

type statement struct {
	lead, body []token
	terminated bool
}

func (s statement) text() string {
	out := render(s.lead) + render(s.body)
	if s.terminated {
		out += ";"
	}
	return out
}

// splitStatements preserves SQLite trigger bodies and opaque PostgreSQL dollar
// strings. Unlike the PoC splitter, it rejects incomplete statements.
func splitStatements(toks []token, sqlite bool) ([]statement, error) {
	var (
		out              []statement
		cur              statement
		trigger, inBody  bool
		ended            bool
		depth, caseDepth int
		prefix           []token
	)
	for _, t := range toks {
		if len(cur.body) == 0 && t.isTrivia() {
			cur.lead = append(cur.lead, t)
			continue
		}
		if ended && !t.isTrivia() && !t.isPunct(";") {
			return nil, fmt.Errorf("unexpected token %q after trigger END", t.text)
		}
		if t.isPunct(";") && (!trigger || ended) {
			if depth != 0 || caseDepth != 0 {
				return nil, fmt.Errorf("unbalanced statement")
			}
			cur.terminated = true
			out = append(out, cur)
			cur = statement{}
			trigger, inBody, ended = false, false, false
			prefix = nil
			continue
		}
		cur.body = append(cur.body, t)
		if !t.isTrivia() && len(prefix) < 3 {
			prefix = append(prefix, t)
			if sqlite && (starts(prefix, "CREATE", "TRIGGER") ||
				starts(prefix, "CREATE", "TEMP", "TRIGGER") ||
				starts(prefix, "CREATE", "TEMPORARY", "TRIGGER")) {
				trigger = true
			}
		}
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unexpected closing parenthesis")
			}
		case trigger && !inBody && t.is("BEGIN"):
			inBody = true
		case t.is("CASE"):
			caseDepth++
		case t.is("END"):
			if caseDepth > 0 {
				caseDepth--
			} else if trigger && inBody && depth == 0 {
				ended = true
			} else {
				return nil, fmt.Errorf("unexpected END")
			}
		}
	}
	if len(cur.body) > 0 {
		return nil, fmt.Errorf("unterminated SQL statement or trigger")
	}
	if len(cur.lead) > 0 {
		out = append(out, cur)
	}
	return out, nil
}
