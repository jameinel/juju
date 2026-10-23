// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/canonical/sqlair"
	"github.com/juju/tc"

	coreschema "github.com/juju/juju/core/database/schema"
	"github.com/juju/juju/internal/database/drivererrors"
	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type postgresqlSuite struct {
	coretesting.BaseSuite
}

func TestPostgreSQLSuite(t *testing.T) {
	tc.Run(t, &postgresqlSuite{})
}

func (s *postgresqlSuite) TestBackendSelectors(c *tc.C) {
	for _, selectDDL := range []func(Backend) (*coreschema.Schema, error){
		ControllerDDLForBackend, ModelDDLForBackend,
	} {
		for backend, dialect := range map[Backend]coreschema.Dialect{
			BackendDqlite: coreschema.SQLite, BackendPostgreSQL: coreschema.PostgreSQL,
		} {
			ddl, err := selectDDL(backend)
			c.Assert(err, tc.ErrorIsNil)
			c.Check(ddl.Dialect(), tc.Equals, dialect)
			c.Check(ddl.Len() > 0, tc.IsTrue)
		}
		for _, backend := range []Backend{"", "unknown"} {
			ddl, err := selectDDL(backend)
			c.Check(err, tc.ErrorMatches, `unknown database backend .*`)
			c.Check(ddl, tc.IsNil)
		}
	}
	c.Check(ControllerDDL().Dialect(), tc.Equals, coreschema.SQLite)
	c.Check(ModelDDL().Dialect(), tc.Equals, coreschema.SQLite)
}

func (s *postgresqlSuite) TestController(c *tc.C) {
	s.compareSchema(c, "controller", ControllerDDL(), ControllerDDLForBackend)
}

func (s *postgresqlSuite) TestModel(c *tc.C) {
	s.compareSchema(c, "model", ModelDDL(), ModelDDLForBackend)
}

func (s *postgresqlSuite) compareSchema(
	c *tc.C, namespace string, sqliteDDL *coreschema.Schema,
	selectDDL func(Backend) (*coreschema.Schema, error),
) {
	admin := pgtesting.OpenAdmin(c)
	url := pgtesting.URL()
	name := pgtesting.NewSchemaName(c, namespace)
	pgtesting.CreateSchema(c, admin, name)
	pg, err := postgresql.Open(url, postgresql.WithSearchPath(name))
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { c.Check(pg.Close(), tc.ErrorIsNil) })
	sq, err := sql.Open("sqlite3", "file:"+c.MkDir()+"/schema.db?_foreign_keys=1")
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { c.Check(sq.Close(), tc.ErrorIsNil) })
	pgDDL, err := selectDDL(BackendPostgreSQL)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(pgDDL.Len(), tc.Equals, sqliteDDL.Len())
	started := time.Now().UTC().Truncate(time.Second)
	for db, ddl := range map[*sql.DB]*coreschema.Schema{sq: sqliteDDL, pg: pgDDL} {
		runner := schemaTestRunner{db}
		changes, err := ddl.Ensure(c.Context(), runner)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(changes, tc.Equals, coreschema.ChangeSet{Current: 0, Post: ddl.Len()})
		changes, err = ddl.Ensure(c.Context(), runner)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(changes, tc.Equals, coreschema.ChangeSet{Current: ddl.Len(), Post: ddl.Len()})
		// Verify bookkeeping detects edits, not merely an existing version.
		wrong := coreschema.NewForDialect(ddl.Dialect(), coreschema.MakePatch("SELECT 1"))
		_, err = wrong.Ensure(c.Context(), runner)
		c.Check(err, tc.ErrorMatches, `.*number of computed hashes.*`)
		wrong.Add(ddl.Patches()[1:]...)
		_, err = wrong.Ensure(c.Context(), runner)
		c.Check(err, tc.ErrorMatches, `.*hash mismatch for version 1.*`)
		rollback := coreschema.NewForDialect(ddl.Dialect(), ddl.Patches()...)
		rollback.Add(coreschema.MakePatch("CREATE TABLE schema_rollback_probe (n INT)"),
			coreschema.MakePatch("INSERT INTO schema_missing_table VALUES (1)"))
		_, err = rollback.Ensure(c.Context(), runner)
		c.Check(err, tc.ErrorMatches, `.*failed to apply patch.*`)
		var versions int
		c.Assert(db.QueryRowContext(c.Context(), "SELECT COUNT(*) FROM schema").Scan(&versions), tc.ErrorIsNil)
		c.Check(versions, tc.Equals, ddl.Len())
	}

	for _, kind := range []string{"table", "view", "index", "trigger"} {
		sqliteQuery := fmt.Sprintf(`SELECT name FROM sqlite_master WHERE type = '%s'
AND name NOT LIKE 'sqlite_%%' ORDER BY name`, kind)
		pgQuery := map[string]string{
			"table": `SELECT tablename FROM pg_tables WHERE schemaname = current_schema()`,
			"view":  `SELECT viewname FROM pg_views WHERE schemaname = current_schema()`,
			"index": `SELECT c.relname FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind = 'i'
AND NOT EXISTS (SELECT 1 FROM pg_constraint k
WHERE k.conindid = c.oid AND k.contype IN ('p', 'u', 'x'))`,
			"trigger": `SELECT t.tgname FROM pg_trigger t
JOIN pg_class c ON c.oid = t.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND NOT t.tgisinternal`,
		}[kind]
		sqNames := schemaObjectNames(c, sq, sqliteQuery)
		pgNames := schemaObjectNames(c, pg, pgQuery)
		slices.Sort(sqNames)
		slices.Sort(pgNames)
		extra := 0
		if kind == "trigger" {
			extra = 1 // The per-namespace change_log serialisation trigger.
		}
		c.Logf("%s %s: SQLite %d, PostgreSQL %d", namespace, kind, len(sqNames), len(pgNames))
		c.Check(len(pgNames), tc.Equals, len(sqNames)+extra)
		if kind == "table" || kind == "view" {
			c.Check(pgNames, tc.DeepEquals, sqNames)
			for _, table := range sqNames {
				if table == "schema" {
					continue
				}
				query := fmt.Sprintf(`SELECT * FROM "%s"`, table)
				if kind == "view" {
					query += " LIMIT 0"
				}
				c.Check(schemaRows(c, pg, query, started), tc.DeepEquals, schemaRows(c, sq, query, started),
					tc.Commentf("%s %s", kind, table))
				if kind == "table" {
					var sqliteFKs, pgFKs int
					err := sq.QueryRowContext(c.Context(),
						`SELECT COUNT(*) FROM pragma_foreign_key_list(?)`, table).Scan(&sqliteFKs)
					c.Assert(err, tc.ErrorIsNil)
					err = pg.QueryRowContext(c.Context(), `SELECT COALESCE(SUM(cardinality(k.conkey)), 0)
FROM pg_constraint k JOIN pg_class t ON t.oid = k.conrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE k.contype = 'f' AND n.nspname = current_schema() AND t.relname = $1`, table).Scan(&pgFKs)
					c.Assert(err, tc.ErrorIsNil)
					c.Check(pgFKs, tc.Equals, sqliteFKs, tc.Commentf("%s foreign keys", table))
				}
			}
		}
	}
	if namespace == "controller" {
		s.checkChangeLog(c, pg)
		s.checkChangeLogCommitOrder(c, pg)
	} else {
		s.checkModelTriggers(c, pg)
	}
}

func (s *postgresqlSuite) checkChangeLog(c *tc.C, db *sql.DB) {
	for _, query := range []string{
		`INSERT INTO controller_config ("key", value) VALUES ('schemagen', 'a')`,
		`UPDATE controller_config SET value = 'b' WHERE "key" = 'schemagen'`,
		`DELETE FROM controller_config WHERE "key" = 'schemagen'`,
	} {
		_, err := db.ExecContext(c.Context(), query)
		c.Assert(err, tc.ErrorIsNil)
	}
	rows, err := db.QueryContext(c.Context(), `SELECT edit_type_id, changed FROM change_log
WHERE namespace_id = (SELECT id FROM change_log_namespace WHERE namespace = 'controller_config')
AND changed = 'schemagen' ORDER BY id`)
	c.Assert(err, tc.ErrorIsNil)
	defer rows.Close()
	var edits []int
	for rows.Next() {
		var edit int
		var changed string
		c.Assert(rows.Scan(&edit, &changed), tc.ErrorIsNil)
		c.Check(changed, tc.Equals, "schemagen")
		edits = append(edits, edit)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	c.Check(edits, tc.DeepEquals, []int{1, 2, 4})
	_, err = db.ExecContext(c.Context(), `INSERT INTO secret_backend (uuid, name, backend_type_id, origin_id)
VALUES ('built-in', 'internal', 0, 0), ('custom', 'custom', 2, 1)`)
	c.Assert(err, tc.ErrorIsNil)
	_, err = db.ExecContext(c.Context(), `UPDATE secret_backend SET name = 'changed' WHERE name = 'internal'`)
	c.Check(drivererrors.IsPGConstraintTrigger(err), tc.IsTrue, tc.Commentf("%v", err))
	_, err = db.ExecContext(c.Context(), `DELETE FROM secret_backend WHERE uuid = 'built-in'`)
	c.Check(drivererrors.IsPGConstraintTrigger(err), tc.IsTrue, tc.Commentf("%v", err))
	_, err = db.ExecContext(c.Context(), `UPDATE secret_backend SET name = 'changed' WHERE uuid = 'custom'`)
	c.Assert(err, tc.ErrorIsNil)
	result, err := db.ExecContext(c.Context(), `DELETE FROM secret_backend WHERE uuid = 'custom'`)
	c.Assert(err, tc.ErrorIsNil)
	deleted, err := result.RowsAffected()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(deleted, tc.Equals, int64(1))
}

func (s *postgresqlSuite) checkModelTriggers(c *tc.C, db *sql.DB) {
	_, err := db.ExecContext(c.Context(), `INSERT INTO sequence (namespace, value) VALUES ('test', 1)`)
	c.Assert(err, tc.ErrorIsNil)
	_, err = db.ExecContext(c.Context(), `UPDATE sequence SET value = 2 WHERE namespace = 'test'`)
	c.Assert(err, tc.ErrorIsNil)
	_, err = db.ExecContext(c.Context(), `UPDATE sequence SET value = 1 WHERE namespace = 'test'`)
	c.Check(drivererrors.IsPGConstraintTrigger(err), tc.IsTrue, tc.Commentf("%v", err))
	var value int
	err = db.QueryRowContext(c.Context(), `SELECT value FROM sequence WHERE namespace = 'test'`).Scan(&value)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(value, tc.Equals, 2)

	_, err = db.ExecContext(c.Context(), `INSERT INTO "constraint" (uuid) VALUES ('test')`)
	c.Assert(err, tc.ErrorIsNil)
	for _, table := range []struct{ table, column string }{
		{"constraint_tag", "tag"}, {"constraint_zone", "zone"}, {"constraint_space", "space"},
	} {
		for _, name := range []string{"z", "a", "m"} {
			var query string
			if table.column == "space" {
				_, err := db.ExecContext(c.Context(), `INSERT INTO space (uuid, name) VALUES ($1, $1)`, name)
				c.Assert(err, tc.ErrorIsNil)
				query = `INSERT INTO constraint_space (constraint_uuid, space, "exclude") VALUES ('test', $1, FALSE)`
			} else {
				query = fmt.Sprintf(`INSERT INTO %s (constraint_uuid, %s) VALUES ('test', $1)`,
					table.table, table.column)
			}
			_, err := db.ExecContext(c.Context(), query, name)
			c.Assert(err, tc.ErrorIsNil)
		}
		names := schemaObjectNames(c, db, fmt.Sprintf(`SELECT %s FROM %s ORDER BY rowid`, table.column, table.table))
		c.Check(names, tc.DeepEquals, []string{"z", "a", "m"})
	}
}

func (s *postgresqlSuite) checkChangeLogCommitOrder(c *tc.C, db *sql.DB) {
	insert := `INSERT INTO change_log (edit_type_id, namespace_id, changed)
VALUES (1, (SELECT MIN(id) FROM change_log_namespace), $1) RETURNING id`
	first, err := db.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	defer first.Rollback()
	var firstID int64
	c.Assert(first.QueryRowContext(c.Context(), insert, "first").Scan(&firstID), tc.ErrorIsNil)

	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()
	second, err := db.BeginTx(ctx, nil)
	c.Assert(err, tc.ErrorIsNil)
	defer second.Rollback()
	var pid int
	c.Assert(second.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid), tc.ErrorIsNil)
	done := make(chan struct{})
	var secondID int64
	var insertErr error
	go func() {
		defer close(done)
		insertErr = second.QueryRowContext(ctx, insert, "second").Scan(&secondID)
	}()
	defer func() {
		cancel()
		_ = first.Rollback()
		<-done
	}()
	for {
		var waiting bool
		err := db.QueryRowContext(c.Context(), `SELECT EXISTS (
SELECT 1 FROM pg_locks WHERE pid = $1 AND locktype = 'advisory' AND NOT granted)`, pid).Scan(&waiting)
		c.Assert(err, tc.ErrorIsNil)
		if waiting {
			break
		}
		select {
		case <-done:
			c.Fatalf("second insert finished before the first committed: %v", insertErr)
		case <-c.Context().Done():
			c.Fatal(c.Context().Err())
		default:
		}
	}
	var visible int
	c.Assert(db.QueryRowContext(c.Context(), `SELECT COUNT(*) FROM change_log
WHERE changed IN ('first', 'second')`).Scan(&visible), tc.ErrorIsNil)
	c.Check(visible, tc.Equals, 0)
	c.Assert(first.Commit(), tc.ErrorIsNil)
	<-done
	c.Assert(insertErr, tc.ErrorIsNil)
	c.Check(secondID > firstID, tc.IsTrue)
	c.Assert(second.Commit(), tc.ErrorIsNil)
	c.Assert(db.QueryRowContext(c.Context(), `SELECT COUNT(*) FROM change_log
WHERE changed IN ('first', 'second')`).Scan(&visible), tc.ErrorIsNil)
	c.Check(visible, tc.Equals, 2)
}

func schemaObjectNames(c *tc.C, db *sql.DB, query string) []string {
	rows, err := db.QueryContext(c.Context(), query)
	c.Assert(err, tc.ErrorIsNil)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		c.Assert(rows.Scan(&name), tc.ErrorIsNil)
		names = append(names, name)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	return names
}

func schemaRows(c *tc.C, db *sql.DB, query string, started time.Time) []string {
	rows, err := db.QueryContext(c.Context(), query)
	c.Assert(err, tc.ErrorIsNil, tc.Commentf("%s", query))
	defer rows.Close()
	cols, err := rows.Columns()
	c.Assert(err, tc.ErrorIsNil)
	var result []string
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		c.Assert(rows.Scan(dest...), tc.ErrorIsNil)
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
			if timestamp, ok := v.(time.Time); ok {
				// Seed timestamps use the database's current time, so
				// compare their meaning rather than their exact value.
				c.Check(timestamp.Before(started), tc.IsFalse)
				c.Check(timestamp.After(time.Now()), tc.IsFalse)
				values[i] = "<current timestamp>"
			}
		}
		result = append(result, fmt.Sprintf("%#v", values))
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	slices.Sort(result)
	return result
}

type schemaTestRunner struct {
	db *sql.DB
}

func (r schemaTestRunner) StdTxn(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r schemaTestRunner) Txn(ctx context.Context, fn func(context.Context, *sqlair.TX) error) error {
	return fmt.Errorf("schema tests only use StdTxn")
}

func (r schemaTestRunner) Dying() <-chan struct{} {
	return nil
}
