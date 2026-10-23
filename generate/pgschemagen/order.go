// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// position orders statements across the patches of a namespace. Statements
// that stay in place have phase 0; statements the ordering pass relocates are
// appended to a patch with phase 1, in the order they were relocated.
type position struct {
	patch, phase, idx int
}

func (p position) after(q position) bool {
	if p.patch != q.patch {
		return p.patch > q.patch
	}
	if p.phase != q.phase {
		return p.phase > q.phase
	}
	return p.idx > q.idx
}

type foreignKey struct {
	item       int
	text       string
	table      string
	target     string
	targetCols []string
}

type tableDef struct {
	pos     position
	stmt    *statement
	keys    [][]string
	fks     []foreignKey
	open    int
	closeAt int
}

type indexDef struct {
	pos   position
	table string
	cols  []string
}

type viewDef struct {
	name    string
	pos     position
	stmt    *statement
	sources []string
}

type relocated struct {
	pos    position
	reason string
	text   string
}

// orderPatches reorders PostgreSQL DDL so that every foreign key target and
// view source exists when it is referenced (D10). SQLite resolves these
// lazily, PostgreSQL at CREATE time. Foreign keys that refer forward are
// stripped from their CREATE TABLE and added with ALTER TABLE, and views
// that refer forward are moved, in both cases to the end of the patch that
// creates their last dependency. The number of patches is unchanged.
func orderPatches(patches []string) ([]string, error) {
	var (
		stmts   = make([][]statement, len(patches))
		tables  = make(map[string]*tableDef)
		indexes []indexDef
		views   = make(map[string]*viewDef)
		order   []string
	)
	for p, src := range patches {
		toks, err := lex(src)
		if err != nil {
			return nil, fmt.Errorf("patch %d: %w", p, err)
		}
		stmts[p] = splitStatements(toks, false)
		for i := range stmts[p] {
			stmt := &stmts[p][i]
			pos := position{patch: p, idx: i}
			switch {
			case stmt.isCreate("TABLE"):
				def, err := parseTableDef(stmt, pos)
				if err != nil {
					return nil, fmt.Errorf("patch %d: %w", p, err)
				}
				tables[createdName(*stmt)] = def
			case stmt.isCreate("INDEX"):
				if idx, ok := parseUniqueIndex(*stmt, pos); ok {
					indexes = append(indexes, idx)
				}
			case stmt.isCreate("VIEW"):
				name := createdName(*stmt)
				views[name] = &viewDef{name: name, pos: pos, stmt: stmt, sources: viewSources(stmt.body)}
				order = append(order, name)
			}
		}
	}

	var (
		moves = make(map[position]bool)
		added []relocated
		seq   int
	)
	tableNames := make([]string, 0, len(tables))
	for name := range tables {
		tableNames = append(tableNames, name)
	}
	sort.Slice(tableNames, func(i, j int) bool {
		return tables[tableNames[j]].pos.after(tables[tableNames[i]].pos)
	})
	for _, name := range tableNames {
		def := tables[name]
		var strip []int
		for _, fk := range def.fks {
			last, err := fkDependency(fk, tables, indexes)
			if err != nil {
				return nil, err
			}
			cyclic := fkInCycle(name, fk.target, tables)
			if !last.after(def.pos) && !cyclic {
				continue
			}
			if def.pos.after(last) {
				last = def.pos
			}
			strip = append(strip, fk.item)
			reason := fmt.Sprintf("foreign key of %s refers to %s, created later", name, fk.target)
			if cyclic {
				reason = fmt.Sprintf("foreign key of %s participates in a cycle through %s", name, fk.target)
			}
			added = append(added, relocated{
				pos:    position{patch: last.patch, phase: 1, idx: seq},
				reason: reason,
				text:   fmt.Sprintf("ALTER TABLE %s ADD %s;", quoteIfNeeded(name), fk.text),
			})
			seq++
		}
		if len(strip) > 0 {
			stripItems(def, strip)
		}
	}

	final := make(map[string]position)
	visiting := make(map[string]bool)
	var place func(name string) (position, error)
	place = func(name string) (position, error) {
		if pos, ok := final[name]; ok {
			return pos, nil
		}
		if t, ok := tables[name]; ok {
			return t.pos, nil
		}
		v, ok := views[name]
		if !ok {
			return position{}, fmt.Errorf("dangling reference to %q", name)
		}
		if visiting[name] {
			return position{}, fmt.Errorf("view cycle through %q", name)
		}
		visiting[name] = true
		defer delete(visiting, name)

		last := position{patch: -1}
		var lastName string
		for _, src := range v.sources {
			pos, err := place(src)
			if err != nil {
				return position{}, fmt.Errorf("view %q: %w", name, err)
			}
			if pos.after(last) {
				last, lastName = pos, src
			}
		}
		if !last.after(v.pos) {
			final[name] = v.pos
			return v.pos, nil
		}
		pos := position{patch: last.patch, phase: 1, idx: seq}
		seq++
		final[name] = pos
		moves[v.pos] = true
		added = append(added, relocated{
			pos:    pos,
			reason: fmt.Sprintf("view %s refers to %s, created later", name, lastName),
			text:   strings.TrimSpace(v.stmt.text()),
		})
		return pos, nil
	}
	for _, name := range order {
		if _, err := place(name); err != nil {
			return nil, err
		}
	}

	sort.Slice(added, func(i, j int) bool { return added[j].pos.after(added[i].pos) })
	out := make([]string, len(patches))
	for p := range stmts {
		var b strings.Builder
		for i, stmt := range stmts[p] {
			if moves[position{patch: p, idx: i}] {
				continue
			}
			b.WriteString(stmt.text())
		}
		for _, r := range added {
			if r.pos.patch != p {
				continue
			}
			text := strings.TrimRight(b.String(), "\n")
			b.Reset()
			b.WriteString(text)
			fmt.Fprintf(&b, "\n\n-- Moved by pgschemagen: %s.\n%s\n", r.reason, r.text)
		}
		out[p] = b.String()
	}
	return out, nil
}

func fkInCycle(source, target string, tables map[string]*tableDef) bool {
	if source == target {
		return false // Self references are valid inside CREATE TABLE.
	}
	seen := make(map[string]bool)
	var reaches func(string) bool
	reaches = func(name string) bool {
		if name == source {
			return true
		}
		if seen[name] || tables[name] == nil {
			return false
		}
		seen[name] = true
		for _, fk := range tables[name].fks {
			if reaches(fk.target) {
				return true
			}
		}
		return false
	}
	return reaches(target)
}

// fkDependency returns the position of the last statement a foreign key
// needs: its target table and, if the target columns are not a key of the
// table, the unique index on them.
func fkDependency(fk foreignKey, tables map[string]*tableDef, indexes []indexDef) (position, error) {
	target, ok := tables[fk.target]
	if !ok {
		return position{}, fmt.Errorf("foreign key of %s: dangling reference to %q", fk.table, fk.target)
	}
	if len(fk.targetCols) == 0 {
		return target.pos, nil
	}
	for _, key := range target.keys {
		if sameColumns(key, fk.targetCols) {
			return target.pos, nil
		}
	}
	for _, idx := range indexes {
		if idx.table == fk.target && sameColumns(idx.cols, fk.targetCols) {
			if idx.pos.after(target.pos) {
				return idx.pos, nil
			}
			return target.pos, nil
		}
	}
	return position{}, fmt.Errorf("foreign key of %s: no unique key on %s (%s)",
		fk.table, fk.target, strings.Join(fk.targetCols, ", "))
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func parseTableDef(stmt *statement, pos position) (*tableDef, error) {
	body := stmt.body
	nameIdx := createdNameIndex(body)
	open := nextSignificant(body, nameIdx+1)
	if open >= len(body) || !body[open].isPunct("(") {
		return nil, fmt.Errorf("unsupported CREATE TABLE form")
	}
	closeIdx, err := matchParen(body, open)
	if err != nil {
		return nil, err
	}
	def := &tableDef{pos: pos, stmt: stmt, open: open, closeAt: closeIdx}
	table := body[nameIdx].name()
	for i, item := range splitTopLevel(body[open+1 : closeIdx]) {
		item = trimTrivia(item)
		if len(item) == 0 {
			continue
		}
		sig := significantIndexes(item)
		first := item[sig[0]]
		switch {
		case first.is("CONSTRAINT"), first.is("FOREIGN"), first.is("PRIMARY"), first.is("UNIQUE"), first.is("CHECK"):
			k := 0
			if first.is("CONSTRAINT") {
				k = 2
			}
			kw := item[sig[k]]
			switch {
			case kw.is("FOREIGN"):
				fk, err := parseForeignKey(item, sig[k:])
				if err != nil {
					return nil, fmt.Errorf("table %s: %w", table, err)
				}
				fk.item, fk.table = i, table
				def.fks = append(def.fks, fk)
			case kw.is("PRIMARY"), kw.is("UNIQUE"):
				for _, s := range sig[k:] {
					if item[s].isPunct("(") {
						closeIdx, err := matchParen(item, s)
						if err != nil {
							return nil, err
						}
						def.keys = append(def.keys, columnNames(item[s+1:closeIdx]))
						break
					}
				}
			}
		default:
			if hasKeyword(item, "REFERENCES") {
				return nil, fmt.Errorf("table %s: column-level REFERENCES is not supported", table)
			}
			if hasKeyword(item, "PRIMARY") || hasKeyword(item, "UNIQUE") {
				def.keys = append(def.keys, []string{first.name()})
			}
		}
	}
	return def, nil
}

func parseForeignKey(item []token, sig []int) (foreignKey, error) {
	fk := foreignKey{text: render(item)}
	for k, s := range sig {
		if !item[s].is("REFERENCES") {
			continue
		}
		if k+1 >= len(sig) {
			break
		}
		fk.target = item[sig[k+1]].name()
		if k+2 < len(sig) && item[sig[k+2]].isPunct("(") {
			closeIdx, err := matchParen(item, sig[k+2])
			if err != nil {
				return fk, err
			}
			fk.targetCols = columnNames(item[sig[k+2]+1 : closeIdx])
		}
		return fk, nil
	}
	return fk, fmt.Errorf("foreign key without REFERENCES: %s", fk.text)
}

func columnNames(toks []token) []string {
	var names []string
	for _, part := range splitTopLevel(toks) {
		part = trimTrivia(part)
		if len(part) > 0 {
			names = append(names, part[0].name())
		}
	}
	return names
}

// parseUniqueIndex returns a unique, non-partial index on plain columns,
// which a foreign key can rely on.
func parseUniqueIndex(stmt statement, pos position) (indexDef, bool) {
	if kw := stmt.keywords(2); len(kw) < 2 || !kw[1].is("UNIQUE") {
		return indexDef{}, false
	}
	if hasKeyword(stmt.body, "WHERE") {
		return indexDef{}, false
	}
	sig := significantIndexes(stmt.body)
	for k, s := range sig {
		if !stmt.body[s].is("ON") || k+2 >= len(sig) || !stmt.body[sig[k+2]].isPunct("(") {
			continue
		}
		closeIdx, err := matchParen(stmt.body, sig[k+2])
		if err != nil {
			return indexDef{}, false
		}
		return indexDef{
			pos:   pos,
			table: stmt.body[sig[k+1]].name(),
			cols:  columnNames(stmt.body[sig[k+2]+1 : closeIdx]),
		}, true
	}
	return indexDef{}, false
}

// stripItems removes the given items from a CREATE TABLE definition.
func stripItems(def *tableDef, strip []int) {
	body := def.stmt.body
	items := splitTopLevel(body[def.open+1 : def.closeAt])
	var kept [][]token
	for i, item := range items {
		if !slices.Contains(strip, i) {
			kept = append(kept, item)
		}
	}
	last := items[len(items)-1]
	trailing := last[prevSignificant(last, len(last))+1:]

	out := slices.Clone(body[:def.open+1])
	for i, item := range kept {
		if i > 0 {
			out = append(out, token{kind: tokPunct, text: ","})
		}
		if i == len(kept)-1 {
			item = append(slices.Clone(item[:prevSignificant(item, len(item))+1]), trailing...)
		}
		out = append(out, item...)
	}
	out = append(out, body[def.closeAt:]...)
	def.stmt.body = out
}

// viewSources returns the tables and views a view selects from, excluding
// the names of its common table expressions.
func viewSources(body []token) []string {
	sig := significantIndexes(body)
	ctes := make(map[string]bool)
	for k := 0; k+2 < len(sig); k++ {
		if body[sig[k+1]].is("AS") && body[sig[k+2]].isPunct("(") && body[sig[k]].name() != "" {
			ctes[body[sig[k]].name()] = true
		}
	}

	var (
		sources  []string
		seen     = make(map[string]bool)
		fromList = make(map[int]bool)
		depth    int
	)
	add := func(k int) {
		if k >= len(sig) {
			return
		}
		name := body[sig[k]].name()
		if name == "" || ctes[name] || seen[name] {
			return
		}
		seen[name] = true
		sources = append(sources, name)
	}
	for k, s := range sig {
		t := body[s]
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			delete(fromList, depth)
			depth--
		case t.is("FROM"):
			fromList[depth] = true
			add(k + 1)
		case t.is("JOIN"):
			add(k + 1)
		case t.isPunct(",") && fromList[depth]:
			add(k + 1)
		case isAnyKeyword(t, fromListEnd):
			delete(fromList, depth)
		}
	}
	return sources
}

var fromListEnd = []string{
	"WHERE", "GROUP", "ORDER", "LIMIT", "HAVING", "UNION", "EXCEPT",
	"INTERSECT", "WINDOW", "JOIN", "ON", "LEFT", "RIGHT", "INNER", "CROSS",
	"FULL", "NATURAL",
}

func quoteIfNeeded(name string) string {
	return quoteIdent(name)
}
