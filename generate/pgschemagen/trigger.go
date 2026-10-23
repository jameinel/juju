// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"strings"
)

// sqliteTrigger is a parsed SQLite CREATE TRIGGER statement.
type sqliteTrigger struct {
	name   string
	timing string
	event  []token
	table  []token
	when   []token
	body   []token
}

// translateTrigger turns a SQLite trigger into a PL/pgSQL trigger function
// and a trigger that executes it. The WHEN condition moves into the
// function, because PostgreSQL does not allow subqueries in WHEN.
func (tr *translator) translateTrigger(body []token) (string, error) {
	trg, err := parseTrigger(body)
	if err != nil {
		return "", err
	}

	var ret string
	switch {
	case trg.timing == "AFTER":
		ret = "NULL"
	case trg.timing == "BEFORE" && trg.event[0].is("DELETE"):
		ret = "OLD"
	case trg.timing == "BEFORE":
		ret = "NEW"
	default:
		return "", fmt.Errorf("unsupported trigger timing %q", trg.timing)
	}

	var stmts []string
	for _, part := range splitBody(trg.body) {
		s, err := translateTriggerStatement(part)
		if err != nil {
			return "", err
		}
		if s != "" {
			stmts = append(stmts, s)
		}
	}

	indent := "    "
	var fn strings.Builder
	fn.WriteString("BEGIN\n")
	if len(trg.when) > 0 {
		when, err := rewriteExpr(trg.when)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&fn, "    IF %s THEN\n", strings.TrimSpace(render(when)))
		indent = "        "
	}
	for _, s := range stmts {
		fn.WriteString(indentLines(s, indent))
		fn.WriteString("\n")
	}
	if len(trg.when) > 0 {
		fn.WriteString("    END IF;\n")
	}
	fmt.Fprintf(&fn, "    RETURN %s;\nEND;\n", ret)
	fnText, err := normaliseSQL(fn.String())
	if err != nil {
		return "", err
	}

	name := shortIdent(trg.name)
	event, err := rewriteExpr(trg.event)
	if err != nil {
		return "", err
	}
	table, err := rewriteExpr(trg.table)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger
LANGUAGE plpgsql AS $$
%[2]s$$;

CREATE TRIGGER %[1]s
%[3]s %[4]s ON %[5]s
FOR EACH ROW EXECUTE FUNCTION %[1]s();`,
		name, fnText, trg.timing,
		strings.Join(strings.Fields(render(event)), " "),
		strings.TrimSpace(render(table)),
	), nil
}

func parseTrigger(body []token) (sqliteTrigger, error) {
	var trg sqliteTrigger
	i := createdNameIndex(body)
	if i < 0 {
		return trg, fmt.Errorf("trigger has no name")
	}
	trg.name = body[i].name()

	i = nextSignificant(body, i+1)
	switch {
	case i >= len(body):
		return trg, fmt.Errorf("trigger %q has no event", trg.name)
	case body[i].is("BEFORE"), body[i].is("AFTER"):
		trg.timing = strings.ToUpper(body[i].text)
		i = nextSignificant(body, i+1)
	case body[i].is("INSTEAD"):
		return trg, fmt.Errorf("INSTEAD OF triggers are not supported")
	default:
		// SQLite defaults to BEFORE.
		trg.timing = "BEFORE"
	}

	start := i
	for i < len(body) && !body[i].is("ON") {
		i++
	}
	if i == len(body) {
		return trg, fmt.Errorf("trigger %q has no ON clause", trg.name)
	}
	trg.event = trimTrivia(body[start:i])
	if len(trg.event) == 0 {
		return trg, fmt.Errorf("trigger %q has no event", trg.name)
	}

	i = nextSignificant(body, i+1)
	if i >= len(body) {
		return trg, fmt.Errorf("trigger %q has no table", trg.name)
	}
	trg.table = body[i : i+1]
	i = nextSignificant(body, i+1)

	if i < len(body) && body[i].is("FOR") {
		// FOR EACH ROW
		for range 3 {
			i = nextSignificant(body, i+1)
		}
	}
	if i < len(body) && body[i].is("WHEN") {
		start := i + 1
		for i < len(body) && !body[i].is("BEGIN") {
			i++
		}
		trg.when = trimTrivia(body[start:i])
	}
	if i >= len(body) || !body[i].is("BEGIN") {
		return trg, fmt.Errorf("trigger %q has no BEGIN", trg.name)
	}
	end := prevSignificant(body, len(body))
	if end <= i || !body[end].is("END") {
		return trg, fmt.Errorf("trigger %q has no END", trg.name)
	}
	trg.body = body[i+1 : end]
	return trg, nil
}

// splitBody splits a trigger body on its statement separators.
func splitBody(body []token) [][]token {
	var (
		parts [][]token
		start int
	)
	for i, t := range body {
		if t.isPunct(";") {
			parts = append(parts, body[start:i])
			start = i + 1
		}
	}
	return append(parts, body[start:])
}

// translateTriggerStatement translates one statement of a trigger body to
// PL/pgSQL, including its terminating semicolon. It returns "" for an empty
// statement.
func translateTriggerStatement(part []token) (string, error) {
	lead := part[:nextSignificant(part, 0)]
	stmt := trimTrivia(part)
	if len(stmt) == 0 {
		return strings.TrimSpace(render(part)), nil
	}
	comments := strings.TrimSpace(render(lead))
	if comments != "" {
		comments += "\n"
	}

	if !stmt[0].is("SELECT") {
		out, err := rewriteExpr(stmt)
		if err != nil {
			return "", err
		}
		return comments + render(out) + ";", nil
	}

	raise := nextSignificant(stmt, 1)
	if raise == len(stmt) || !stmt[raise].is("RAISE") {
		// A SELECT whose result is discarded.
		out, err := rewriteExpr(stmt[1:])
		if err != nil {
			return "", err
		}
		return comments + "PERFORM" + render(out) + ";", nil
	}

	open := nextSignificant(stmt, raise+1)
	closeIdx, err := matchParen(stmt, open)
	if err != nil {
		return "", err
	}
	args := splitTopLevel(stmt[open+1 : closeIdx])
	if len(args) != 2 {
		return "", fmt.Errorf("unsupported RAISE form %q", render(stmt[raise:closeIdx+1]))
	}
	mode := trimTrivia(args[0])
	if len(mode) != 1 || (!mode[0].is("FAIL") && !mode[0].is("ABORT")) {
		return "", fmt.Errorf("unsupported RAISE mode %q", render(args[0]))
	}
	msg := trimTrivia(args[1])
	if len(msg) != 1 || msg[0].kind != tokString {
		return "", fmt.Errorf("RAISE message must be a string literal")
	}
	raiseStmt := "RAISE EXCEPTION USING MESSAGE = " + msg[0].text + ";"

	rest, err := rewriteExpr(trimTrivia(stmt[closeIdx+1:]))
	if err != nil {
		return "", err
	}
	switch {
	case len(rest) == 0:
		return comments + raiseStmt, nil
	case rest[0].is("WHERE"):
		cond := strings.TrimSpace(render(rest[1:]))
		return fmt.Sprintf("%sIF %s THEN\n    %s\nEND IF;", comments, cond, raiseStmt), nil
	case rest[0].is("FROM"):
		return fmt.Sprintf("%sIF EXISTS (SELECT 1 %s) THEN\n    %s\nEND IF;",
			comments, render(rest), raiseStmt), nil
	}
	return "", fmt.Errorf("unsupported RAISE form %q", render(stmt))
}

// indentLines re-indents a block so its least indented line starts at
// indent.
func indentLines(s, indent string) string {
	lines := strings.Split(strings.Trim(s, "\n"), "\n")
	minIndent := -1
	for k, l := range lines {
		if k == 0 || strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	for k, l := range lines {
		l = strings.TrimRight(l, " \t")
		switch {
		case l == "":
		case k == 0:
			l = indent + strings.TrimLeft(l, " \t")
		default:
			l = indent + strings.ReplaceAll(l[min(minIndent, len(l)):], "\t", "    ")
		}
		lines[k] = l
	}
	return strings.Join(lines, "\n")
}
