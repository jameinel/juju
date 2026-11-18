// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	domainschema "github.com/juju/juju/domain/schema"
)

type column struct {
	name, pgType, declaredType string
}

type translator struct {
	tables    map[string][]column
	keys      map[string][][]string
	relations map[string]string
}

func newTranslator() *translator {
	return &translator{
		tables: make(map[string][]column), keys: make(map[string][][]string),
		relations: make(map[string]string),
	}
}

// translatePatch accepts only forms whose translation is implemented. In
// particular, dependency relocation and trigger/identity semantics must not be
// replaced with the PoC's permissive unknown-statement pass-through.
func (tr *translator) translatePatch(src string) (string, error) {
	toks, err := lex(src)
	if err != nil {
		return "", err
	}
	stmts, err := splitStatements(toks, true)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, stmt := range stmts {
		sig := significant(stmt.body)
		if len(sig) == 0 {
			out.WriteString(stmt.text())
			continue
		}
		for _, tok := range sig {
			// SQLite case-insensitive lookup also applies to quoted names.
			// Reject these until all reference forms can preserve that lookup.
			if tok.kind == tokQuotedIdent && tok.name() != strings.ToLower(tok.name()) {
				return "", fmt.Errorf("mixed-case quoted identifiers are not implemented: %s", tok.text)
			}
		}
		var text string
		switch {
		case starts(sig, "CREATE", "TABLE"):
			text, err = tr.translateTable(sig)
		case starts(sig, "CREATE", "INDEX"), starts(sig, "CREATE", "UNIQUE", "INDEX"):
			text, err = tr.translateIndex(sig)
		case starts(sig, "INSERT", "INTO"):
			text, err = tr.translateInsert(sig)
		case starts(sig, "ALTER", "TABLE"):
			text, err = tr.translateAlter(sig)
		default:
			return "", fmt.Errorf("unsupported statement form: %s", preview(stmt.body))
		}
		if err != nil {
			return "", fmt.Errorf("%w in statement: %s", err, preview(stmt.body))
		}
		out.WriteString(render(stmt.lead))
		// Comments inside rewritten syntax are retained, not lexically rewritten.
		for _, t := range stmt.body {
			if t.kind == tokComment {
				out.WriteString(t.text)
				out.WriteByte('\n')
			}
		}
		out.WriteString(text)
		out.WriteString(";")
	}
	return normaliseSQL(out.String())
}

func starts(toks []token, words ...string) bool {
	if len(toks) < len(words) {
		return false
	}
	for i, word := range words {
		if !toks[i].is(word) {
			return false
		}
	}
	return true
}

func preview(toks []token) string {
	text := strings.TrimSpace(render(toks))
	if len(text) > 160 {
		text = text[:160] + "..."
	}
	return text
}

func (tr *translator) translateTable(t []token) (string, error) {
	if len(t) < 5 || t[2].name() == "" || !t[3].isPunct("(") {
		return "", fmt.Errorf("unsupported CREATE TABLE form")
	}
	name := t[2].name()
	if err := tr.addRelation(name, "table"); err != nil {
		return "", err
	}
	closeAt, err := matchParen(t, 3)
	if err != nil {
		return "", err
	}
	options := t[closeAt+1:]
	if len(options) > 0 && !starts(options, "STRICT") {
		return "", fmt.Errorf("unsupported table options %q", render(options))
	}
	if len(options) > 1 {
		return "", fmt.Errorf("unsupported table options %q", render(options))
	}
	items, err := splitTopLevel(t[4:closeAt])
	if err != nil {
		return "", err
	}
	var cols []column
	var definitions []string
	var constraints, foreignKeys [][]token
	var keys [][]string
	constraintNames := make(map[string]bool)
	seen := make(map[string]bool)
	for _, item := range items {
		key := item
		if starts(key, "CONSTRAINT") && len(key) >= 3 {
			key = key[2:]
		}
		if starts(key, "FOREIGN") {
			foreignKeys = append(foreignKeys, item)
			continue
		}
		if starts(item, "CONSTRAINT") || starts(item, "PRIMARY") || starts(item, "UNIQUE") {
			constraints = append(constraints, item)
			continue
		}
		def, col, err := tr.translateColumn(name, item)
		if err != nil {
			return "", err
		}
		if seen[col.name] {
			return "", fmt.Errorf("duplicate column %q", col.name)
		}
		seen[col.name] = true
		cols = append(cols, col)
		definitions = append(definitions, def)
		for i := 2; i < len(item); i++ {
			if starts(item[i:], "PRIMARY", "KEY") {
				keys = append(keys, []string{col.name})
				constraintNames[name+"_pkey"] = true
			} else if item[i].is("UNIQUE") {
				keys = append(keys, []string{col.name})
				constraintNames[shortIdent(name+"_"+col.name+"_key")] = true
			}
		}
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("table %q has no columns", name)
	}
	for _, item := range constraints {
		key := item
		if starts(key, "CONSTRAINT") && len(key) >= 3 {
			key = key[2:]
		}
		if starts(key, "PRIMARY", "KEY") {
			names, err := identifierList(key[2:])
			if err != nil {
				return "", err
			}
			if len(names) == 1 && slices.ContainsFunc(cols, func(col column) bool {
				return col.name == names[0] && col.declaredType == "INTEGER"
			}) {
				return "", fmt.Errorf("INTEGER PRIMARY KEY allocation is not implemented")
			}
		}
		def, err := tr.translateKey(name, item, seen)
		if err != nil {
			return "", err
		}
		n := 1
		if starts(key, "PRIMARY", "KEY") {
			n = 2
		}
		names, err := identifierList(key[n:])
		if err != nil {
			return "", err
		}
		keys = append(keys, names)
		// translateKey emits a quoted constraint name as its second token.
		tokens, err := lex(def)
		if err != nil {
			return "", err
		}
		constraintNames[significant(tokens)[1].name()] = true
		definitions = append(definitions, def)
	}
	tr.tables[name] = cols
	tr.keys[name] = keys
	for _, item := range foreignKeys {
		def, constraintName, err := tr.translateForeignKey(name, item)
		if err != nil {
			return "", err
		}
		if constraintNames[constraintName] {
			return "", fmt.Errorf("duplicate constraint %q on table %q", constraintName, name)
		}
		constraintNames[constraintName] = true
		definitions = append(definitions, def)
	}
	return "CREATE TABLE " + quoteIdent(name) + " (\n    " + strings.Join(definitions, ",\n    ") + "\n)", nil
}

func (tr *translator) translateColumn(table string, item []token) (string, column, error) {
	if len(item) < 2 || item[0].name() == "" {
		return "", column{}, fmt.Errorf("unsupported column definition %q", render(item))
	}
	col := column{name: item[0].name(), declaredType: strings.ToUpper(item[1].text)}
	if len(col.name) > 63 {
		return "", col, fmt.Errorf("column identifier %q exceeds PostgreSQL length limit", col.name)
	}
	types := map[string]string{
		"INT": "BIGINT", "INTEGER": "BIGINT", "TEXT": "TEXT", "BLOB": "BYTEA",
		"BOOLEAN": "BOOLEAN", "DATETIME": "TIMESTAMPTZ", "TIMESTAMP": "TIMESTAMPTZ",
	}
	if _, ok := types[col.declaredType]; !ok || item[1].kind != tokIdent {
		return "", col, fmt.Errorf("column %q has unsupported type %q", col.name, item[1].text)
	}
	storageType := domainschema.ColumnStorageType(table, col.name, item[1].text)
	var ok bool
	if col.pgType, ok = types[storageType]; !ok {
		return "", col, fmt.Errorf("column %q has unsupported type %q", col.name, item[1].text)
	}
	var def strings.Builder
	def.WriteString(quoteIdent(col.name) + " " + col.pgType)
	seen := make(map[string]bool)
	for i := 2; i < len(item); {
		kw := strings.ToUpper(item[i].text)
		if seen[kw] {
			return "", col, fmt.Errorf("column %q repeats %s", col.name, kw)
		}
		seen[kw] = true
		switch {
		case starts(item[i:], "NOT", "NULL"):
			def.WriteString(" NOT NULL")
			i += 2
		case item[i].is("NULL"):
			def.WriteString(" NULL")
			i++
		case starts(item[i:], "PRIMARY", "KEY"):
			if item[1].is("INTEGER") {
				return "", col, fmt.Errorf("INTEGER PRIMARY KEY allocation is not implemented")
			}
			if err := tr.addRelation(table+"_pkey", "primary key"); err != nil {
				return "", col, err
			}
			def.WriteString(" PRIMARY KEY")
			i += 2
		case item[i].is("UNIQUE"):
			name := shortIdent(table + "_" + col.name + "_key")
			if err := tr.addRelation(name, "unique key"); err != nil {
				return "", col, err
			}
			def.WriteString(" CONSTRAINT " + quoteIdent(name) + " UNIQUE")
			i++
		case item[i].is("DEFAULT"):
			if i+1 == len(item) {
				return "", col, fmt.Errorf("missing default for column %q", col.name)
			}
			end := i + 2
			if item[i+1].isPunct("(") {
				at, err := matchParen(item, i+1)
				if err != nil {
					return "", col, err
				}
				end = at + 1
			} else if item[i+1].isPunct("-") || item[i+1].isPunct("+") {
				end++
			}
			if end > len(item) {
				return "", col, fmt.Errorf("missing default operand")
			}
			value, err := scalar(item[i+1:end], col.pgType)
			if err != nil {
				return "", col, err
			}
			def.WriteString(" DEFAULT " + value)
			i = end
		default:
			return "", col, fmt.Errorf("unsupported column clause %q", item[i].text)
		}
	}
	if seen["NOT"] && seen["NULL"] {
		return "", col, fmt.Errorf("conflicting nullability for column %q", col.name)
	}
	return def.String(), col, nil
}

func (tr *translator) translateKey(table string, item []token, columns map[string]bool) (string, error) {
	name := ""
	if starts(item, "CONSTRAINT") && len(item) >= 3 && item[1].name() != "" {
		name = item[1].name()
		item = item[2:]
	}
	primary := starts(item, "PRIMARY", "KEY")
	n := 1
	if primary {
		n = 2
	} else if !starts(item, "UNIQUE") {
		return "", fmt.Errorf("unsupported table constraint %q", render(item))
	}
	names, err := identifierList(item[n:])
	if err != nil {
		return "", err
	}
	for _, col := range names {
		if !columns[col] {
			return "", fmt.Errorf("key refers to unknown column %q", col)
		}
	}
	kind := "UNIQUE"
	if primary {
		name, kind = table+"_pkey", "PRIMARY KEY"
	} else if name == "" {
		name = table + "_" + strings.Join(names, "_") + "_key"
	}
	if !primary && strings.HasSuffix(name, "_pkey") {
		return "", fmt.Errorf("unique constraint %q uses reserved _pkey suffix", name)
	}
	if !primary {
		name = shortIdent(name)
	}
	if err := tr.addRelation(name, kind); err != nil {
		return "", err
	}
	for i := range names {
		names[i] = quoteIdent(names[i])
	}
	return "CONSTRAINT " + quoteIdent(name) + " " + kind + " (" + strings.Join(names, ", ") + ")", nil
}

func identifierList(t []token) ([]string, error) {
	if len(t) < 3 || !t[0].isPunct("(") || !t[len(t)-1].isPunct(")") {
		return nil, fmt.Errorf("expected parenthesised column list")
	}
	items, err := splitTopLevel(t[1 : len(t)-1])
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range items {
		if len(item) != 1 || item[0].name() == "" || slices.Contains(names, item[0].name()) {
			return nil, fmt.Errorf("invalid or duplicate column in list")
		}
		names = append(names, item[0].name())
	}
	return names, nil
}

func (tr *translator) translateIndex(t []token) (string, error) {
	i, prefix := 2, "CREATE INDEX "
	if starts(t, "CREATE", "UNIQUE", "INDEX") {
		i, prefix = 3, "CREATE UNIQUE INDEX "
	}
	if len(t) < i+5 || t[i].name() == "" || !t[i+1].is("ON") || t[i+2].name() == "" {
		return "", fmt.Errorf("unsupported CREATE INDEX form")
	}
	name, table := shortIdent(t[i].name()), t[i+2].name()
	if strings.HasSuffix(name, "_pkey") {
		return "", fmt.Errorf("index %q uses reserved _pkey suffix", name)
	}
	cols, err := identifierList(t[i+3:])
	if err != nil {
		return "", err
	}
	if err := tr.checkColumns(table, cols); err != nil {
		return "", err
	}
	if err := tr.addRelation(name, "index"); err != nil {
		return "", err
	}
	if starts(t, "CREATE", "UNIQUE", "INDEX") {
		tr.keys[table] = append(tr.keys[table], slices.Clone(cols))
	}
	for j := range cols {
		cols[j] = quoteIdent(cols[j])
	}
	return prefix + quoteIdent(name) + " ON " + quoteIdent(table) + " (" + strings.Join(cols, ", ") + ")", nil
}

func (tr *translator) translateInsert(t []token) (string, error) {
	if len(t) < 5 || t[2].name() == "" {
		return "", fmt.Errorf("unsupported INSERT form")
	}
	table := t[2].name()
	cols, ok := tr.tables[table]
	if !ok {
		return "", fmt.Errorf("INSERT refers to unknown table %q", table)
	}
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = col.name
	}
	at := 3
	explicit := t[at].isPunct("(")
	if explicit {
		closeAt, err := matchParen(t, at)
		if err != nil {
			return "", err
		}
		names, err = identifierList(t[at : closeAt+1])
		if err != nil {
			return "", err
		}
		at = closeAt + 1
	}
	if err := tr.checkColumns(table, names); err != nil {
		return "", err
	}
	if at >= len(t) || !t[at].is("VALUES") {
		return "", fmt.Errorf("only literal INSERT VALUES is implemented")
	}
	rows, err := splitTopLevel(t[at+1:])
	if err != nil {
		return "", err
	}
	var values []string
	for _, row := range rows {
		if len(row) < 3 || !row[0].isPunct("(") || !row[len(row)-1].isPunct(")") {
			return "", fmt.Errorf("unsupported VALUES row")
		}
		items, err := splitTopLevel(row[1 : len(row)-1])
		if err != nil {
			return "", err
		}
		if len(items) != len(names) {
			return "", fmt.Errorf("VALUES has %d values for %d columns", len(items), len(names))
		}
		var fields []string
		for i, item := range items {
			col := cols[slices.IndexFunc(cols, func(col column) bool { return col.name == names[i] })]
			value, err := scalar(item, col.pgType)
			if err != nil {
				return "", fmt.Errorf("column %q: %w", col.name, err)
			}
			fields = append(fields, value)
		}
		values = append(values, "("+strings.Join(fields, ", ")+")")
	}
	out := "INSERT INTO " + quoteIdent(table)
	if explicit {
		for i := range names {
			names[i] = quoteIdent(names[i])
		}
		out += " (" + strings.Join(names, ", ") + ")"
	}
	return out + " VALUES " + strings.Join(values, ", "), nil
}

func (tr *translator) translateAlter(t []token) (string, error) {
	if len(t) < 7 || t[2].name() == "" || !starts(t[3:], "ADD", "COLUMN") {
		return "", fmt.Errorf("only ALTER TABLE ADD COLUMN is implemented")
	}
	table := t[2].name()
	cols, ok := tr.tables[table]
	if !ok {
		return "", fmt.Errorf("ALTER refers to unknown table %q", table)
	}
	item := t[5:]
	for _, tok := range item {
		if tok.is("PRIMARY") || tok.is("UNIQUE") {
			return "", fmt.Errorf("ADD COLUMN with keys is not implemented")
		}
	}
	def, col, err := tr.translateColumn(table, item)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(cols, func(c column) bool { return c.name == col.name }) {
		return "", fmt.Errorf("duplicate column %q", col.name)
	}
	tr.tables[table] = append(cols, col)
	return "ALTER TABLE " + quoteIdent(table) + " ADD COLUMN " + def, nil
}

func (tr *translator) checkColumns(table string, names []string) error {
	cols, ok := tr.tables[table]
	if !ok {
		return fmt.Errorf("unknown table %q", table)
	}
	for _, name := range names {
		if !slices.ContainsFunc(cols, func(col column) bool { return col.name == name }) {
			return fmt.Errorf("table %q has no column %q", table, name)
		}
	}
	return nil
}

func (tr *translator) addRelation(name, kind string) error {
	if len(name) > 63 || name == "" {
		return fmt.Errorf("unsupported identifier length for %q", name)
	}
	if previous, ok := tr.relations[name]; ok {
		return fmt.Errorf("%s %q collides with %s", kind, name, previous)
	}
	tr.relations[name] = kind
	return nil
}

// scalar translates only literal values and the exact clocks used by the H2
// schema. It does not substitute tokens inside arbitrary expressions.
func scalar(t []token, pgType string) (string, error) {
	for len(t) > 1 && t[0].isPunct("(") {
		end, err := matchParen(t, 0)
		if err != nil || end != len(t)-1 {
			break
		}
		t = t[1:end]
	}
	if len(t) == 0 {
		return "", fmt.Errorf("empty scalar")
	}
	if len(t) == 1 && t[0].is("NULL") {
		return "NULL", nil
	}
	if pgType == "TIMESTAMPTZ" {
		if len(t) == 1 && t[0].is("CURRENT_TIMESTAMP") {
			return "date_trunc('second', statement_timestamp())", nil
		}
		if len(t) >= 4 && t[1].isPunct("(") && t[len(t)-1].isPunct(")") {
			args, err := splitTopLevel(t[2 : len(t)-1])
			if err == nil {
				literal := func(i int, want string) bool {
					return i < len(args) && len(args[i]) == 1 &&
						args[i][0].kind == tokString && strings.EqualFold(args[i][0].text, want)
				}
				if t[0].is("DATETIME") && (len(args) == 1 || len(args) == 2 && literal(1, "'utc'")) &&
					literal(0, "'now'") {
					return "date_trunc('second', statement_timestamp())", nil
				}
				if t[0].is("STRFTIME") && len(args) == 3 && len(args[0]) == 1 &&
					args[0][0].kind == tokString && args[0][0].text == "'%Y-%m-%d %H:%M:%f'" &&
					literal(1, "'now'") && literal(2, "'utc'") {
					return "date_trunc('milliseconds', statement_timestamp())", nil
				}
			}
		}
	}
	if pgType == "BOOLEAN" {
		if len(t) == 1 {
			switch {
			case t[0].is("TRUE"), t[0].kind == tokNumber && t[0].text == "1":
				return "TRUE", nil
			case t[0].is("FALSE"), t[0].kind == tokNumber && t[0].text == "0":
				return "FALSE", nil
			}
		}
		return "", fmt.Errorf("unsupported Boolean literal %q", render(t))
	}
	if pgType == "BIGINT" {
		text := render(t)
		if len(t) == 1 && t[0].kind == tokNumber ||
			len(t) == 2 && (t[0].isPunct("-") || t[0].isPunct("+")) && t[1].kind == tokNumber {
			if _, err := strconv.ParseInt(text, 10, 64); err == nil {
				return text, nil
			}
		}
		return "", fmt.Errorf("unsupported integer literal %q", text)
	}
	if len(t) == 1 && t[0].kind == tokString && (pgType == "TEXT" || pgType == "TIMESTAMPTZ") {
		if strings.ContainsRune(t[0].text, '\\') {
			return "", fmt.Errorf("backslash string semantics are not implemented")
		}
		return t[0].text, nil
	}
	if pgType == "BYTEA" && len(t) == 2 && t[0].is("X") && t[1].kind == tokString {
		text := t[1].text[1 : len(t[1].text)-1]
		if _, err := hex.DecodeString(text); err != nil {
			return "", fmt.Errorf("invalid blob literal: %w", err)
		}
		return "decode('" + strings.ToLower(text) + "', 'hex')", nil
	}
	return "", fmt.Errorf("unsupported %s scalar %q", pgType, render(t))
}

func shortIdent(name string) string {
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:4])
	prefix := name[:63-len(suffix)]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix + suffix
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func normaliseSQL(src string) (string, error) {
	toks, err := lex(src)
	if err != nil {
		return "", err
	}
	for i := range toks {
		t := &toks[i]
		if t.kind == tokSpace {
			lines := strings.Split(t.text, "\n")
			for j := range lines[:len(lines)-1] {
				lines[j] = strings.TrimRight(lines[j], " \t\r")
			}
			t.text = strings.Join(lines, "\n")
			if i == len(toks)-1 {
				t.text = strings.TrimRight(t.text, " \t\r\n")
			}
		} else if t.kind == tokComment && strings.HasPrefix(t.text, "--") {
			t.text = strings.TrimRight(t.text, " \t\r")
		}
	}
	return strings.TrimRight(render(toks), "\n") + "\n", nil
}
