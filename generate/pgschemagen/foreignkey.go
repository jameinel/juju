// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"slices"
	"strings"
)

// translateForeignKey handles references whose target key already exists.
// Forward references need a separate dependency-ordering pass.
func (tr *translator) translateForeignKey(table string, item []token) (string, string, error) {
	name := ""
	if starts(item, "CONSTRAINT") {
		if len(item) < 3 || item[1].name() == "" {
			return "", "", fmt.Errorf("invalid foreign key constraint name")
		}
		name = item[1].name()
		item = item[2:]
	}
	if !starts(item, "FOREIGN", "KEY") || len(item) < 4 {
		return "", "", fmt.Errorf("unsupported foreign key %q", render(item))
	}
	end, err := matchParen(item, 2)
	if err != nil {
		return "", "", err
	}
	local, err := identifierList(item[2 : end+1])
	if err != nil {
		return "", "", err
	}
	if err := tr.checkColumns(table, local); err != nil {
		return "", "", err
	}
	item = item[end+1:]
	if len(item) < 3 || !item[0].is("REFERENCES") || item[1].name() == "" {
		return "", "", fmt.Errorf("foreign key of %q requires a referenced table and columns", table)
	}
	target := item[1].name()
	end, err = matchParen(item, 2)
	if err != nil {
		return "", "", err
	}
	remote, err := identifierList(item[2 : end+1])
	if err != nil {
		return "", "", err
	}
	if len(local) != len(remote) {
		return "", "", fmt.Errorf("foreign key of %q has mismatched column counts", table)
	}
	if err := tr.checkColumns(target, remote); err != nil {
		return "", "", fmt.Errorf("foreign key of %q: %w", table, err)
	}
	if !slices.ContainsFunc(tr.keys[target], func(key []string) bool {
		return sameKeyColumns(key, remote)
	}) {
		return "", "", fmt.Errorf("foreign key of %q references %q without a matching unique key", table, target)
	}
	for i := range local {
		sourceCol := tr.tables[table][slices.IndexFunc(tr.tables[table], func(c column) bool { return c.name == local[i] })]
		targetCol := tr.tables[target][slices.IndexFunc(tr.tables[target], func(c column) bool { return c.name == remote[i] })]
		if sourceCol.pgType != targetCol.pgType {
			return "", "", fmt.Errorf("foreign key of %q has incompatible types for %q and %q.%q",
				table, local[i], target, remote[i])
		}
	}
	if name == "" {
		name = table + "_" + strings.Join(local, "_") + "_fkey"
	}
	name = shortIdent(name)
	if strings.HasSuffix(name, "_pkey") {
		return "", "", fmt.Errorf("foreign key constraint %q uses reserved _pkey suffix", name)
	}
	var clauses []string
	seen := make(map[string]bool)
	for rest := item[end+1:]; len(rest) > 0; {
		if len(rest) < 3 || !rest[0].is("ON") || !(rest[1].is("DELETE") || rest[1].is("UPDATE")) {
			return "", "", fmt.Errorf("unsupported foreign key clause %q", render(rest))
		}
		event := strings.ToUpper(rest[1].text)
		if seen[event] {
			return "", "", fmt.Errorf("foreign key repeats ON %s", event)
		}
		seen[event] = true
		action, n := "", 3
		switch {
		case rest[2].is("CASCADE"), rest[2].is("RESTRICT"):
			action = strings.ToUpper(rest[2].text)
		case len(rest) >= 4 && starts(rest[2:], "NO", "ACTION"):
			action, n = "NO ACTION", 4
		case len(rest) >= 4 && starts(rest[2:], "SET", "NULL"):
			action, n = "SET NULL", 4
		case len(rest) >= 4 && starts(rest[2:], "SET", "DEFAULT"):
			action, n = "SET DEFAULT", 4
		default:
			return "", "", fmt.Errorf("unsupported foreign key action %q", render(rest[2:]))
		}
		clauses = append(clauses, " ON "+event+" "+action)
		rest = rest[n:]
	}
	for i := range local {
		local[i], remote[i] = quoteIdent(local[i]), quoteIdent(remote[i])
	}
	return "CONSTRAINT " + quoteIdent(name) + " FOREIGN KEY (" + strings.Join(local, ", ") +
		") REFERENCES " + quoteIdent(target) + " (" + strings.Join(remote, ", ") + ")" +
		strings.Join(clauses, ""), name, nil
}

func sameKeyColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
