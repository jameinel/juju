// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type schemaSuite struct {
	coretesting.BaseSuite

	placement postgresql.Placement
}

func TestSchemaSuite(t *testing.T) {
	tc.Run(t, &schemaSuite{})
}

func (s *schemaSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)

	admin := pgtesting.OpenAdmin(c)
	s.placement = postgresql.Placement{
		URL:          pgtesting.URL(),
		SchemaPrefix: pgtesting.NewSchemaPrefix(c),
	}
	pgtesting.DropSchemasOnCleanup(c, admin, s.placement.SchemaPrefix)
}

func (s *schemaSuite) TestSchemaLifecycle(c *tc.C) {
	admin, err := s.placement.OpenAdmin()
	c.Assert(err, tc.ErrorIsNil)
	defer admin.Close()

	name := s.placement.SchemaName("0b6c6d2e-1c55-4a0c-8f4f-2a3c1e9d7b10")
	c.Cleanup(func() {
		_ = postgresql.DropSchema(context.Background(), admin, name)
	})

	exists, err := postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)

	err = postgresql.CreateSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	exists, err = postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsTrue)

	err = postgresql.CreateSchema(c.Context(), admin, name)
	c.Check(err, tc.ErrorIs, postgresql.ErrSchemaExists)

	err = postgresql.EnsureSchema(c.Context(), admin, name)
	c.Check(err, tc.ErrorIsNil)

	db, err := s.placement.OpenNamespace("0b6c6d2e-1c55-4a0c-8f4f-2a3c1e9d7b10")
	c.Assert(err, tc.ErrorIsNil)
	defer db.Close()
	_, err = db.ExecContext(c.Context(), "CREATE TABLE t (id INT PRIMARY KEY)")
	c.Assert(err, tc.ErrorIsNil)
	var schema string
	err = db.QueryRowContext(c.Context(),
		"SELECT table_schema FROM information_schema.tables WHERE table_name = 't' AND table_schema = current_schema()",
	).Scan(&schema)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(schema, tc.Equals, name)

	err = postgresql.DropSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	exists, err = postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)

	err = postgresql.DropSchema(c.Context(), admin, name)
	c.Check(err, tc.ErrorIsNil)
}

func (s *schemaSuite) TestEnsureSchemaConcurrent(c *tc.C) {
	admin, err := s.placement.OpenAdmin()
	c.Assert(err, tc.ErrorIsNil)
	defer admin.Close()

	name := s.placement.SchemaName("concurrent")
	c.Cleanup(func() {
		_ = postgresql.DropSchema(context.Background(), admin, name)
	})

	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			errs <- postgresql.EnsureSchema(c.Context(), admin, name)
		}()
	}
	for range n {
		c.Check(<-errs, tc.ErrorIsNil)
	}
}

func (s *schemaSuite) TestSchemaInitialised(c *tc.C) {
	admin, err := s.placement.OpenAdmin()
	c.Assert(err, tc.ErrorIsNil)
	defer admin.Close()

	name := s.placement.SchemaName("initialised")
	other := s.placement.SchemaName("other")
	quoted := postgresql.QuoteIdentifier(name)

	check := func(expected bool) {
		c.Helper()
		initialised, err := postgresql.SchemaInitialised(c.Context(), admin, name)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(initialised, tc.Equals, expected)
	}
	exec := func(stmt string) {
		c.Helper()
		_, err := admin.ExecContext(c.Context(), stmt)
		c.Assert(err, tc.ErrorIsNil)
	}

	// A missing schema is not initialised.
	check(false)

	// Nor is an empty one.
	err = postgresql.CreateSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	check(false)

	// A view named schema is not the bookkeeping table.
	exec("CREATE VIEW " + quoted + `."schema" AS SELECT 1 AS version`)
	check(false)
	exec("DROP VIEW " + quoted + `."schema"`)

	// A bookkeeping table in another schema does not count.
	err = postgresql.CreateSchema(c.Context(), admin, other)
	c.Assert(err, tc.ErrorIsNil)
	exec("CREATE TABLE " + postgresql.QuoteIdentifier(other) + `."schema" (version BIGINT)`)
	exec("INSERT INTO " + postgresql.QuoteIdentifier(other) + `."schema" VALUES (1)`)
	check(false)

	// The bookkeeping table without a version row is not initialised.
	exec("CREATE TABLE " + quoted + `."schema" (version BIGINT)`)
	check(false)

	exec("INSERT INTO " + quoted + `."schema" VALUES (1)`)
	check(true)
}

func (s *schemaSuite) TestDeleteSchemaDropsJujuObjects(c *tc.C) {
	admin := s.openAdmin(c)

	name := s.placement.SchemaName("delete")
	// Neighbours share the name's prefix or the object names, and must not
	// be touched.
	neighbours := []string{name + "x", s.placement.SchemaName("other")}
	for _, schemaName := range append([]string{name}, neighbours...) {
		createJujuObjects(c, admin, schemaName)
	}
	before := make(map[string][]string)
	for _, neighbour := range neighbours {
		before[neighbour] = schemaObjects(c, admin, neighbour)
	}
	c.Assert(schemaObjects(c, admin, name), tc.DeepEquals, before[neighbours[0]])

	err := postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
	c.Assert(err, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, name, false)
	for _, neighbour := range neighbours {
		c.Check(schemaObjects(c, admin, neighbour), tc.DeepEquals, before[neighbour])
	}

	// Deleting it again, or deleting one never created, succeeds.
	err = postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
	c.Check(err, tc.ErrorIsNil)
	never := s.placement.SchemaName("never")
	err = postgresql.DeleteSchema(c.Context(), admin, never, time.Minute)
	c.Check(err, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, never, false)
}

func (s *schemaSuite) TestDeleteSchemaEmpty(c *tc.C) {
	admin := s.openAdmin(c)

	name := s.placement.SchemaName("empty")
	err := postgresql.CreateSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)

	err = postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
	c.Assert(err, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, name, false)
}

func (s *schemaSuite) TestDeleteSchemaUnexpectedObject(c *tc.C) {
	admin := s.openAdmin(c)

	tests := []struct {
		about  string
		stmt   string
		detail string
	}{{
		about:  "enum type",
		stmt:   "CREATE TYPE %s.mood AS ENUM ('ok')",
		detail: `dropping schema: .*: type .*mood depends on schema .*`,
	}, {
		about:  "materialized view",
		stmt:   "CREATE MATERIALIZED VIEW %[1]s.mv AS SELECT id FROM %[1]s.parent",
		detail: `dropping tables: .*: materialized view .*mv depends on table .*parent`,
	}, {
		about:  "procedure",
		stmt:   "CREATE PROCEDURE %s.p() LANGUAGE sql AS 'SELECT 1'",
		detail: `dropping schema: .*: (function|procedure) .*p\(\) depends on schema .*`,
	}, {
		about:  "aggregate",
		stmt:   "CREATE AGGREGATE %s.total(INT) (SFUNC = int4pl, STYPE = INT)",
		detail: `dropping schema: .*: function .*total\(integer\) depends on schema .*`,
	}}
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		name := s.placement.SchemaName(fmt.Sprintf("unexpected-%d", i))
		createJujuObjects(c, admin, name)
		exec(c, admin, fmt.Sprintf(test.stmt, postgresql.QuoteIdentifier(name)))
		before := schemaObjects(c, admin, name)

		err := postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
		c.Check(pgCode(err), tc.Equals, "2BP01")
		c.Check(err, tc.ErrorMatches, `deleting schema ".*": `+test.detail)
		c.Check(schemaObjects(c, admin, name), tc.DeepEquals, before)
	}
}

func (s *schemaSuite) TestDeleteSchemaOutsideDependent(c *tc.C) {
	admin := s.openAdmin(c)

	tests := []struct {
		about  string
		stmts  []string
		detail string
	}{{
		about:  "view",
		stmts:  []string{"CREATE VIEW %[2]s.v AS SELECT id FROM %[1]s.parent"},
		detail: `dropping tables: .*: view .*\.v depends on table .*parent`,
	}, {
		about:  "foreign key",
		stmts:  []string{"CREATE TABLE %[2]s.t (parent_id BIGINT REFERENCES %[1]s.parent (id))"},
		detail: `dropping tables: .*: constraint t_parent_id_fkey on table .*\.t depends on table .*parent`,
	}, {
		about: "trigger",
		stmts: []string{
			"CREATE TABLE %[2]s.t (id INT)",
			"CREATE TRIGGER t_touch BEFORE INSERT ON %[2]s.t FOR EACH ROW EXECUTE FUNCTION %[1]s.touch()",
		},
		detail: `dropping functions: .*: trigger t_touch on table .*\.t depends on function .*touch\(\)`,
	}, {
		about:  "sequence default",
		stmts:  []string{"CREATE TABLE %[2]s.t (n BIGINT DEFAULT nextval('%[1]s.counter'))"},
		detail: `dropping sequences: .*: default value for column n of table .*\.t depends on sequence .*counter`,
	}}
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		name := s.placement.SchemaName(fmt.Sprintf("dependency-%d", i))
		outside := s.placement.SchemaName(fmt.Sprintf("outside-%d", i))
		createJujuObjects(c, admin, name)
		err := postgresql.CreateSchema(c.Context(), admin, outside)
		c.Assert(err, tc.ErrorIsNil)
		for _, stmt := range test.stmts {
			exec(c, admin, fmt.Sprintf(stmt,
				postgresql.QuoteIdentifier(name), postgresql.QuoteIdentifier(outside)))
		}
		before := schemaObjects(c, admin, name)
		beforeOutside := schemaObjects(c, admin, outside)

		err = postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
		c.Check(pgCode(err), tc.Equals, "2BP01")
		c.Check(err, tc.ErrorMatches, `deleting schema ".*": `+test.detail)
		c.Check(schemaObjects(c, admin, name), tc.DeepEquals, before)
		c.Check(schemaObjects(c, admin, outside), tc.DeepEquals, beforeOutside)

		// Once the dependent has gone, a retry succeeds.
		err = postgresql.DropSchema(c.Context(), admin, outside)
		c.Assert(err, tc.ErrorIsNil)
		err = postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
		c.Check(err, tc.ErrorIsNil)
		s.checkSchemaExists(c, admin, name, false)
	}
}

func (s *schemaSuite) TestDeleteSchemaLockTimeout(c *tc.C) {
	admin := s.openAdmin(c)

	name := s.placement.SchemaName("locked")
	createJujuObjects(c, admin, name)
	before := schemaObjects(c, admin, name)
	blocker, _ := lockTable(c, admin, name, "child")

	// The test session's own lock_timeout is far longer, so a quick failure
	// shows that the delete set its own.
	start := time.Now()
	err := postgresql.DeleteSchema(c.Context(), admin, name, 200*time.Millisecond)
	c.Check(pgCode(err), tc.Equals, "55P03")
	c.Check(time.Since(start) < 10*time.Second, tc.IsTrue)
	c.Check(schemaObjects(c, admin, name), tc.DeepEquals, before)

	c.Assert(blocker.Rollback(), tc.ErrorIsNil)
	err = postgresql.DeleteSchema(c.Context(), admin, name, 200*time.Millisecond)
	c.Assert(err, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, name, false)
}

func (s *schemaSuite) TestDeleteSchemaWaitsForLock(c *tc.C) {
	admin := s.openAdmin(c)

	name := s.placement.SchemaName("wait")
	createJujuObjects(c, admin, name)
	blocker, blockerPID := lockTable(c, admin, name, "child")

	done := make(chan error, 1)
	go func() {
		done <- postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
	}()
	waitForBlocked(c, admin, blockerPID)
	s.checkSchemaExists(c, admin, name, true)

	c.Assert(blocker.Rollback(), tc.ErrorIsNil)
	c.Check(<-done, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, name, false)
}

func (s *schemaSuite) TestDeleteSchemaCancelled(c *tc.C) {
	admin := s.openAdmin(c)

	name := s.placement.SchemaName("cancel")
	createJujuObjects(c, admin, name)
	before := schemaObjects(c, admin, name)
	blocker, blockerPID := lockTable(c, admin, name, "child")

	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- postgresql.DeleteSchema(ctx, admin, name, time.Minute)
	}()
	waitForBlocked(c, admin, blockerPID)
	cancel()
	c.Check(<-done, tc.ErrorIs, context.Canceled)
	c.Check(schemaObjects(c, admin, name), tc.DeepEquals, before)

	// The cancelled session's server backend can still be waiting for the
	// lock; it rolls back once it notices that its client has gone.
	c.Assert(blocker.Rollback(), tc.ErrorIsNil)
	err := postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
	c.Assert(err, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, name, false)
}

func (s *schemaSuite) TestDeleteSchemaConcurrentlyDeleted(c *tc.C) {
	admin := s.openAdmin(c)

	name := s.placement.SchemaName("concurrent-delete")
	createJujuObjects(c, admin, name)

	// Another session drops the schema, and its objects stay locked until it
	// commits. The delete must wait, then find everything gone.
	other, err := admin.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = other.Rollback() }()
	var otherPID int
	err = other.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&otherPID)
	c.Assert(err, tc.ErrorIsNil)
	err = postgresql.DropSchema(c.Context(), other, name)
	c.Assert(err, tc.ErrorIsNil)

	done := make(chan error, 1)
	go func() {
		done <- postgresql.DeleteSchema(c.Context(), admin, name, time.Minute)
	}()
	waitForBlocked(c, admin, otherPID)

	c.Assert(other.Commit(), tc.ErrorIsNil)
	c.Check(<-done, tc.ErrorIsNil)
	s.checkSchemaExists(c, admin, name, false)
}

func (s *schemaSuite) openAdmin(c *tc.C) *sql.DB {
	admin, err := s.placement.OpenAdmin()
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = admin.Close() })
	return admin
}

func (s *schemaSuite) checkSchemaExists(c *tc.C, admin *sql.DB, name string, expected bool) {
	c.Helper()
	exists, err := postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.Equals, expected)
}

// createJujuObjects creates the schema holding one of each kind of object
// that Juju's DDL creates: tables with an identity column, an owned and an
// unowned sequence, a foreign key, an index, a trigger and its function, an
// overloaded function, and a view on a view.
func createJujuObjects(c *tc.C, admin *sql.DB, name string) {
	err := postgresql.CreateSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)

	q := postgresql.QuoteIdentifier(name)
	for _, stmt := range []string{
		"CREATE TABLE " + q + ".parent (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name TEXT UNIQUE)",
		"CREATE SEQUENCE " + q + ".counter",
		"CREATE TABLE " + q + ".child (" +
			"id BIGSERIAL PRIMARY KEY, " +
			"parent_id BIGINT NOT NULL REFERENCES " + q + ".parent (id), " +
			"n BIGINT DEFAULT nextval('" + q + ".counter'))",
		"CREATE INDEX child_parent ON " + q + ".child (parent_id)",
		"CREATE FUNCTION " + q + ".touch() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER child_touch BEFORE INSERT ON " + q + ".child FOR EACH ROW EXECUTE FUNCTION " + q + ".touch()",
		"CREATE FUNCTION " + q + ".add(a INT, b INT) RETURNS INT LANGUAGE sql AS 'SELECT a + b'",
		"CREATE FUNCTION " + q + ".add(a BIGINT, b BIGINT) RETURNS BIGINT LANGUAGE sql AS 'SELECT a + b'",
		"CREATE VIEW " + q + ".parent_names AS SELECT id, name FROM " + q + ".parent",
		"CREATE VIEW " + q + ".named AS SELECT name, " + q + ".add(1, 2) AS n FROM " + q + ".parent_names",
		"INSERT INTO " + q + ".parent (name) VALUES ('a')",
		"INSERT INTO " + q + ".child (parent_id) SELECT id FROM " + q + ".parent",
	} {
		exec(c, admin, stmt)
	}
}

// schemaObjects lists the schema's relations, functions, types and
// triggers, without its name, so that schemas can be compared.
func schemaObjects(c *tc.C, admin *sql.DB, name string) []string {
	rows, err := admin.QueryContext(c.Context(), `
SELECT 'relation ' || c.relkind::text || ' ' || c.relname
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = $1
UNION ALL
SELECT 'function ' || p.prokind::text || ' ' || p.proname || '(' || pg_catalog.oidvectortypes(p.proargtypes) || ')'
FROM pg_catalog.pg_proc AS p
JOIN pg_catalog.pg_namespace AS n ON n.oid = p.pronamespace
WHERE n.nspname = $1
UNION ALL
SELECT 'type ' || t.typtype::text || ' ' || t.typname
FROM pg_catalog.pg_type AS t
JOIN pg_catalog.pg_namespace AS n ON n.oid = t.typnamespace
WHERE n.nspname = $1
UNION ALL
SELECT 'trigger ' || g.tgname || ' on ' || c.relname
FROM pg_catalog.pg_trigger AS g
JOIN pg_catalog.pg_class AS c ON c.oid = g.tgrelid
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND NOT g.tgisinternal
ORDER BY 1`, name)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = rows.Close() }()

	var objects []string
	for rows.Next() {
		var object string
		c.Assert(rows.Scan(&object), tc.ErrorIsNil)
		objects = append(objects, object)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	return objects
}

// lockTable starts a transaction holding a lock on the table, which is
// rolled back when the test finishes, and returns it with its backend PID.
func lockTable(c *tc.C, admin *sql.DB, schemaName, table string) (*sql.Tx, int) {
	tx, err := admin.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = tx.Rollback() })

	var pid int
	err = tx.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&pid)
	c.Assert(err, tc.ErrorIsNil)
	_, err = tx.ExecContext(c.Context(), "LOCK TABLE "+
		postgresql.QuoteIdentifier(schemaName)+"."+postgresql.QuoteIdentifier(table)+
		" IN ACCESS SHARE MODE")
	c.Assert(err, tc.ErrorIsNil)
	return tx, pid
}

// waitForBlocked waits until another session is waiting on a lock held by
// the blocker backend.
func waitForBlocked(c *tc.C, admin *sql.DB, blocker int) {
	for {
		var waiting bool
		err := admin.QueryRowContext(c.Context(),
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))",
			blocker,
		).Scan(&waiting)
		c.Assert(err, tc.ErrorIsNil)
		if waiting {
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-c.Context().Done():
			c.Fatal("timed out waiting for a session to block")
		}
	}
}

func exec(c *tc.C, db *sql.DB, stmt string) {
	c.Helper()
	_, err := db.ExecContext(c.Context(), stmt)
	c.Assert(err, tc.ErrorIsNil)
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
