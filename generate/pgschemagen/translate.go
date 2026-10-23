// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// maxIdentLen is the longest identifier PostgreSQL keeps; longer names are
// silently truncated, which can make distinct names collide.
const maxIdentLen = 63

// translator converts SQLite DDL to PostgreSQL DDL, one patch at a time.
// It remembers the column types of the tables it has translated, so seed
// rows can be adapted to them.
type translator struct {
	tables map[string][]column
}

type column struct {
	name   string
	pgType string
}

func newTranslator() *translator {
	return &translator{tables: make(map[string][]column)}
}

// translatePatch translates one patch of SQLite DDL.
func (tr *translator) translatePatch(src string) (string, error) {
	toks, err := lex(src)
	if err != nil {
		return "", err
	}
	var (
		out        strings.Builder
		addLogLock bool
	)
	for _, stmt := range splitStatements(toks, true) {
		text, err := tr.translateStatement(stmt)
		if err != nil {
			return "", fmt.Errorf("%w in statement:\n%s", err, strings.TrimSpace(stmt.text()))
		}
		out.WriteString(text)
		if stmt.isCreate("TABLE") && createdName(stmt) == changeLogTable {
			addLogLock = true
		}
	}
	if addLogLock {
		out.WriteString(changeLogLock)
	}
	return out.String(), nil
}

// changeLogTable is the table whose id is drawn by changeLogLock rather than
// by an identity column.
const changeLogTable = "change_log"

// changeLogLock serialises change_log inserts per namespace (PostgreSQL
// schema) and draws ids only once the lock is held, so ids become visible in
// commit order, as the change stream requires. change_log.id has no identity
// default, because PostgreSQL evaluates defaults before BEFORE triggers, so
// the trigger could not tell a default id from an explicit one. Explicit ids
// are kept and move the sequence past them, as SQLite AUTOINCREMENT does.
const changeLogLock = `

-- Added by pgschemagen: serialise change_log inserts per namespace and
-- draw ids after taking the lock, so that ids become visible in commit
-- order. Explicit ids are kept and advance the sequence, matching SQLite
-- AUTOINCREMENT.
CREATE SEQUENCE change_log_id_seq OWNED BY change_log.id;

CREATE FUNCTION trg_change_log_serialise() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    seq regclass := pg_get_serial_sequence(quote_ident(TG_TABLE_SCHEMA) || '.' || quote_ident(TG_TABLE_NAME), 'id')::regclass;
    last_id bigint;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtext('juju.change_log'), hashtext(TG_TABLE_SCHEMA));
    IF NEW.id IS NULL THEN
        NEW.id := nextval(seq);
    ELSE
        EXECUTE format('SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM %s', seq) INTO last_id;
        IF NEW.id > last_id THEN
            PERFORM setval(seq, NEW.id);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_change_log_serialise
BEFORE INSERT ON change_log
FOR EACH ROW EXECUTE FUNCTION trg_change_log_serialise();
`

func (tr *translator) translateStatement(stmt statement) (string, error) {
	if len(stmt.body) == 0 {
		return stmt.text(), nil
	}
	var (
		body []token
		err  error
	)
	switch {
	case stmt.isCreate("TABLE"):
		body, err = tr.translateTable(stmt.body)
	case stmt.isCreate("TRIGGER"):
		var text string
		text, err = tr.translateTrigger(stmt.body)
		if err != nil {
			return "", err
		}
		return render(stmt.lead) + text, nil
	case stmt.isCreate("INDEX"):
		body, err = rewriteExpr(stmt.body)
		if err == nil {
			body = shortenCreatedName(body)
		}
	case stmt.keywords(1)[0].is("INSERT"):
		body, err = tr.translateInsert(stmt.body)
	default:
		body, err = rewriteExpr(stmt.body)
	}
	if err != nil {
		return "", err
	}
	stmt.body = body
	return stmt.text(), nil
}

// createdName returns the name of the object a CREATE statement creates.
func createdName(stmt statement) string {
	i := createdNameIndex(stmt.body)
	if i < 0 {
		return ""
	}
	return stmt.body[i].name()
}

// createdNameIndex returns the index of the name token in a CREATE
// statement, skipping modifiers and IF NOT EXISTS.
func createdNameIndex(body []token) int {
	seenObject := false
	for i, t := range body {
		if t.isTrivia() {
			continue
		}
		switch {
		case t.is("CREATE"), t.is("UNIQUE"), t.is("TEMP"), t.is("TEMPORARY"),
			t.is("IF"), t.is("NOT"), t.is("EXISTS"), t.is("OR"), t.is("REPLACE"):
			continue
		case !seenObject:
			seenObject = true
			continue
		}
		return i
	}
	return -1
}

func shortenCreatedName(body []token) []token {
	i := createdNameIndex(body)
	if i < 0 {
		return body
	}
	out := append([]token(nil), body...)
	out[i] = token{kind: tokIdent, text: shortIdent(out[i].name())}
	return out
}

// shortIdent returns name, or if it is longer than PostgreSQL keeps, a
// prefix of it with a hash of the whole name appended.
func shortIdent(name string) string {
	if len(name) <= maxIdentLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:4])
	return name[:maxIdentLen-len(suffix)] + suffix
}

// translateTable translates a CREATE TABLE statement: column types,
// identity columns, defaults and table options.
func (tr *translator) translateTable(body []token) ([]token, error) {
	nameIdx := createdNameIndex(body)
	open := nextSignificant(body, nameIdx+1)
	if nameIdx < 0 || open >= len(body) || !body[open].isPunct("(") {
		return nil, fmt.Errorf("unsupported CREATE TABLE form")
	}
	closeIdx, err := matchParen(body, open)
	if err != nil {
		return nil, err
	}
	table := body[nameIdx].name()

	out, err := rewriteExpr(body[:open+1])
	if err != nil {
		return nil, err
	}
	var cols []column
	for i, item := range splitTopLevel(body[open+1 : closeIdx]) {
		if i > 0 {
			out = append(out, token{kind: tokPunct, text: ","})
		}
		translated, col, err := translateTableItem(item, table != changeLogTable)
		if err != nil {
			return nil, err
		}
		if col != nil {
			cols = append(cols, *col)
		}
		out = append(out, translated...)
	}
	out = append(out, body[closeIdx])

	// Drop SQLite table options such as STRICT and WITHOUT ROWID.
	for _, t := range body[closeIdx+1:] {
		if t.isTrivia() {
			out = append(out, t)
			continue
		}
		if !t.is("STRICT") && !t.is("WITHOUT") && !t.is("ROWID") && !t.isPunct(",") {
			return nil, fmt.Errorf("unsupported table option %q", t.text)
		}
	}
	tr.tables[table] = cols
	return trimTrailingSpace(out), nil
}

func trimTrailingSpace(toks []token) []token {
	for len(toks) > 0 && toks[len(toks)-1].kind == tokSpace {
		toks = toks[:len(toks)-1]
	}
	return toks
}

var tableConstraintKeywords = []string{"CONSTRAINT", "PRIMARY", "UNIQUE", "FOREIGN", "CHECK"}

// columnConstraintKeywords end the type name of a column definition.
var columnConstraintKeywords = []string{
	"CONSTRAINT", "PRIMARY", "NOT", "NULL", "UNIQUE", "CHECK", "DEFAULT",
	"COLLATE", "REFERENCES", "GENERATED", "AS", "AUTOINCREMENT",
}

func isAnyKeyword(t token, kws []string) bool {
	return slices.ContainsFunc(kws, t.is)
}

// translateTableItem translates a column definition or table constraint. It
// returns the column for a column definition. A rowid alias column becomes an
// identity column only if identity is set; otherwise its value must be
// assigned by a trigger.
func translateTableItem(item []token, identity bool) ([]token, *column, error) {
	first := nextSignificant(item, 0)
	if first == len(item) {
		return item, nil, nil
	}
	if isAnyKeyword(item[first], tableConstraintKeywords) {
		out, err := rewriteExpr(item)
		if err != nil {
			return nil, nil, err
		}
		return dropPrimaryKeyName(out), nil, nil
	}

	name := item[first].name()
	typeStart := nextSignificant(item, first+1)
	typeEnd := typeStart
	for typeEnd < len(item) && (item[typeEnd].kind == tokIdent || item[typeEnd].kind == tokSpace) &&
		!isAnyKeyword(item[typeEnd], columnConstraintKeywords) {
		typeEnd++
	}
	typeEnd = prevSignificant(item, typeEnd) + 1
	if typeEnd <= typeStart {
		return nil, nil, fmt.Errorf("column %q has no type", name)
	}
	sqliteType := strings.ToUpper(render(item[typeStart:typeEnd]))
	pgType, ok := typeMap[sqliteType]
	if !ok {
		return nil, nil, fmt.Errorf("column %q has unsupported type %q", name, sqliteType)
	}

	rest := item[typeEnd:]
	if hasKeyword(rest, "AUTOINCREMENT") || (sqliteType == "INTEGER" && hasKeyword(rest, "PRIMARY")) {
		// INTEGER PRIMARY KEY is an alias of the SQLite rowid, so it is
		// assigned automatically, with or without AUTOINCREMENT.
		pgType = "BIGINT"
		if identity {
			pgType = "BIGINT GENERATED BY DEFAULT AS IDENTITY"
		}
		rest = dropKeyword(rest, "AUTOINCREMENT")
	}
	if pgType == "BOOLEAN" {
		rest = booleanDefaults(rest)
	}
	rest, err := rewriteExpr(rest)
	if err != nil {
		return nil, nil, err
	}

	out := append([]token(nil), item[:typeStart]...)
	out = append(out, token{kind: tokIdent, text: pgType})
	out = append(out, rest...)
	return out, &column{name: name, pgType: pgType}, nil
}

var typeMap = map[string]string{
	"INT":       "BIGINT",
	"INTEGER":   "BIGINT",
	"BIGINT":    "BIGINT",
	"TEXT":      "TEXT",
	"BOOLEAN":   "BOOLEAN",
	"DATETIME":  "TIMESTAMPTZ",
	"TIMESTAMP": "TIMESTAMPTZ",
	"DATE":      "DATE",
	"BLOB":      "BYTEA",
	"REAL":      "DOUBLE PRECISION",
}

func hasKeyword(toks []token, kw string) bool {
	for _, t := range toks {
		if t.is(kw) {
			return true
		}
	}
	return false
}

// dropKeyword removes the keyword kw and the whitespace before it.
func dropKeyword(toks []token, kw string) []token {
	var out []token
	for _, t := range toks {
		if t.is(kw) {
			out = trimTrailingSpace(out)
			continue
		}
		out = append(out, t)
	}
	return out
}

// dropPrimaryKeyName removes "CONSTRAINT <name>" before PRIMARY KEY, so the
// key gets PostgreSQL's default <table>_pkey name, which error mapping
// relies on.
func dropPrimaryKeyName(item []token) []token {
	i := nextSignificant(item, 0)
	if i == len(item) || !item[i].is("CONSTRAINT") {
		return item
	}
	j := nextSignificant(item, nextSignificant(item, i+1)+1)
	if j == len(item) || !item[j].is("PRIMARY") {
		return item
	}
	return append(append([]token(nil), item[:i]...), item[j:]...)
}

// booleanDefaults rewrites DEFAULT 0 / DEFAULT 1 to FALSE / TRUE.
func booleanDefaults(toks []token) []token {
	out := append([]token(nil), toks...)
	for i, t := range out {
		if !t.is("DEFAULT") {
			continue
		}
		j := nextSignificant(out, i+1)
		if j < len(out) {
			out[j] = booleanLiteral(out[j])
		}
	}
	return out
}

func booleanLiteral(t token) token {
	if t.kind != tokNumber {
		return t
	}
	switch t.text {
	case "0":
		return token{kind: tokIdent, text: "FALSE"}
	case "1":
		return token{kind: tokIdent, text: "TRUE"}
	}
	return t
}

// translateInsert translates seed rows, turning 0 and 1 into FALSE and TRUE
// for boolean columns.
func (tr *translator) translateInsert(body []token) ([]token, error) {
	body, err := rewriteExpr(body)
	if err != nil {
		return nil, err
	}
	sig := significantIndexes(body)
	if len(sig) < 4 || !body[sig[1]].is("INTO") {
		return body, nil
	}
	cols, ok := tr.tables[body[sig[2]].name()]
	if !ok {
		return body, nil
	}

	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.name
	}
	k := 3
	if body[sig[k]].isPunct("(") {
		closeIdx, err := matchParen(body, sig[k])
		if err != nil {
			return nil, err
		}
		names = nil
		for _, part := range splitTopLevel(body[sig[k]+1 : closeIdx]) {
			names = append(names, trimTrivia(part)[0].name())
		}
		for k < len(sig) && sig[k] <= closeIdx {
			k++
		}
	}
	if k >= len(sig) || !body[sig[k]].is("VALUES") {
		return body, nil
	}
	isBool := make([]bool, len(names))
	for i, n := range names {
		for _, c := range cols {
			if c.name == n && c.pgType == "BOOLEAN" {
				isBool[i] = true
			}
		}
	}

	out := append([]token(nil), body...)
	for i := sig[k] + 1; i < len(out); i++ {
		if !out[i].isPunct("(") {
			continue
		}
		closeIdx, err := matchParen(out, i)
		if err != nil {
			return nil, err
		}
		pos, depth := 0, 0
		for j := i + 1; j < closeIdx; j++ {
			switch t := out[j]; {
			case t.isPunct("("):
				depth++
			case t.isPunct(")"):
				depth--
			case t.isPunct(",") && depth == 0:
				pos++
			case depth == 0 && pos < len(isBool) && isBool[pos]:
				out[j] = booleanLiteral(t)
			}
		}
		i = closeIdx
	}
	return out, nil
}

func significantIndexes(toks []token) []int {
	var idx []int
	for i, t := range toks {
		if !t.isTrivia() {
			idx = append(idx, i)
		}
	}
	return idx
}

// unsupportedFunctions are SQLite functions with no direct translation. A
// patch using one needs an override.
var unsupportedFunctions = []string{
	"GROUP_CONCAT", "JULIANDAY", "UNIXEPOCH", "TYPEOF", "PRINTF", "INSTR",
	"JSON", "JSON_EXTRACT", "JSON_EACH", "GLOB", "DATE", "TIME",
}

// rewriteExpr applies the dialect rewrites that are valid anywhere in a
// statement: reserved identifiers, functions and operators.
func rewriteExpr(toks []token) ([]token, error) {
	var out []token
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tokQuotedIdent:
			if t.text[0] == '`' {
				t = token{kind: tokQuotedIdent, text: quoteIdent(t.name())}
			}
			out = append(out, t)
			continue
		case tokPunct:
			if t.text == "==" {
				t.text = "="
			}
			out = append(out, t)
			continue
		case tokIdent:
		default:
			out = append(out, t)
			continue
		}

		next := nextSignificant(toks, i+1)
		isCall := next < len(toks) && toks[next].isPunct("(")
		prev := prevSignificant(toks, i)
		qualified := prev >= 0 && toks[prev].isPunct(".")
		switch {
		case t.is("user") && !qualified:
			out = append(out, token{kind: tokQuotedIdent, text: `"user"`})
		case isCall && (t.is("DATETIME") || t.is("STRFTIME")):
			closeIdx, err := matchParen(toks, next)
			if err != nil {
				return nil, err
			}
			if !callsNow(t.text, toks[next+1:closeIdx]) {
				return nil, fmt.Errorf("unsupported %s call", t.text)
			}
			out = append(out, token{kind: tokIdent, text: "now"},
				token{kind: tokPunct, text: "("}, token{kind: tokPunct, text: ")"})
			i = closeIdx
		case isCall && t.is("IIF"):
			closeIdx, err := matchParen(toks, next)
			if err != nil {
				return nil, err
			}
			args := splitTopLevel(toks[next+1 : closeIdx])
			if len(args) != 3 {
				return nil, fmt.Errorf("IIF needs 3 arguments")
			}
			var rewritten [3][]token
			for k, arg := range args {
				if rewritten[k], err = rewriteExpr(trimTrivia(arg)); err != nil {
					return nil, err
				}
			}
			out = append(out, token{kind: tokIdent, text: "CASE WHEN "})
			out = append(out, rewritten[0]...)
			out = append(out, token{kind: tokIdent, text: " THEN "})
			out = append(out, rewritten[1]...)
			out = append(out, token{kind: tokIdent, text: " ELSE "})
			out = append(out, rewritten[2]...)
			out = append(out, token{kind: tokIdent, text: " END"})
			i = closeIdx
		case isCall && t.is("IFNULL"):
			out = append(out, token{kind: tokIdent, text: "COALESCE"})
		case isCall && isAnyKeyword(t, unsupportedFunctions):
			return nil, fmt.Errorf("unsupported function %s", t.text)
		default:
			out = append(out, t)
		}
	}
	return out, nil
}

func callsNow(fn string, args []token) bool {
	parts := splitTopLevel(args)
	if strings.EqualFold(fn, "STRFTIME") {
		format := trimTrivia(parts[0])
		if len(format) != 1 || format[0].text != "'%Y-%m-%d %H:%M:%f'" {
			return false
		}
		parts = parts[1:]
	}
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for i, part := range parts {
		part = trimTrivia(part)
		want := "'now'"
		if i == 1 {
			want = "'utc'"
		}
		if len(part) != 1 || part[0].kind != tokString || !strings.EqualFold(part[0].text, want) {
			return false
		}
	}
	return true
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
