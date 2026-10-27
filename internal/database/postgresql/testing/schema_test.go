// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	stdtesting "testing"

	"github.com/jackc/pgx/v5"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/postgresql"
	coretesting "github.com/juju/juju/internal/testing"
)

type schemaSuite struct {
	coretesting.BaseSuite
}

func TestSchemaSuite(t *stdtesting.T) {
	tc.Run(t, &schemaSuite{})
}

func (s *schemaSuite) TestSchemaPID(c *tc.C) {
	tests := []struct {
		name string
		pid  int
		ok   bool
	}{
		{name: "juju-test-123-4", pid: 123, ok: true},
		{name: "juju-test-123-4-controller", pid: 123, ok: true},
		{name: "juju-test-123-4-", pid: 123, ok: true},
		{name: "juju-test-123", ok: false},
		{name: "juju-test-123-", ok: false},
		{name: "juju-test-abc-4", ok: false},
		{name: "juju-test-123-4x", ok: false},
		{name: "juju_test_123_4", ok: false},
		{name: "juju_controller", ok: false},
		{name: "juju-test-99999999999999999999-1", ok: false},
	}
	for _, test := range tests {
		pid, ok := schemaPID(test.name)
		c.Check(ok, tc.Equals, test.ok, tc.Commentf("%q", test.name))
		c.Check(pid, tc.Equals, test.pid, tc.Commentf("%q", test.name))
	}
}

func (s *schemaSuite) TestQuoteLiteral(c *tc.C) {
	c.Check(quoteLiteral(`plain`), tc.Equals, `'plain'`)
	c.Check(quoteLiteral(`it's`), tc.Equals, `'it''s'`)
	c.Check(quoteLiteral(`back\slash`), tc.Equals, `'back\slash'`)
}

func (s *schemaSuite) TestWithSessionTimeouts(c *tc.C) {
	const all = "lock_timeout=30s&statement_timeout=60s&" +
		"idle_in_transaction_session_timeout=60s"
	tests := []struct {
		conn string
		want string
	}{
		{conn: "", want: ""},
		{
			conn: "postgres://u:p%3Fw@h:5432/db",
			want: "postgres://u:p%3Fw@h:5432/db?" + all,
		},
		{
			conn: "postgresql://h/db?sslmode=disable",
			want: "postgresql://h/db?sslmode=disable&" + all,
		},
		{conn: "postgres://h/db?", want: "postgres://h/db?" + all},
		{
			conn: "postgres://h/db?statement_timeout=5s",
			want: "postgres://h/db?statement_timeout=5s&lock_timeout=30s&" +
				"idle_in_transaction_session_timeout=60s",
		},
		{conn: "postgres://h/db?bad=%zz", want: "postgres://h/db?bad=%zz"},
		{
			conn: "host=h dbname=db",
			want: "host=h dbname=db lock_timeout=30s statement_timeout=60s " +
				"idle_in_transaction_session_timeout=60s",
		},
		{
			conn: "host=h lock_timeout = 1s",
			want: "host=h lock_timeout = 1s statement_timeout=60s " +
				"idle_in_transaction_session_timeout=60s",
		},
	}
	for _, test := range tests {
		c.Check(withSessionTimeouts(test.conn), tc.Equals, test.want,
			tc.Commentf("%q", test.conn))
	}
}

func (s *schemaSuite) TestURLSetsSessionTimeouts(c *tc.C) {
	admin := OpenAdmin(c)

	for name, want := range map[string]string{
		"lock_timeout":                        "30s",
		"statement_timeout":                   "1min",
		"idle_in_transaction_session_timeout": "1min",
	} {
		var got string
		err := admin.QueryRowContext(c.Context(), "SHOW "+name).Scan(&got)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(got, tc.Equals, want, tc.Commentf("%s", name))
	}
}

func (s *schemaSuite) TestNewSchemaName(c *tc.C) {
	_ = OpenAdmin(c)

	first := NewSchemaName(c, "")
	second := NewSchemaName(c, "controller")
	prefix := NewSchemaPrefix(c)

	c.Check(first, tc.Matches, fmt.Sprintf(`juju-test-%d-[0-9]+`, os.Getpid()))
	c.Check(second, tc.Matches, fmt.Sprintf(`juju-test-%d-[0-9]+-controller`, os.Getpid()))
	c.Check(prefix, tc.Matches, fmt.Sprintf(`juju-test-%d-[0-9]+-`, os.Getpid()))
	c.Check(second, tc.Not(tc.Equals), first+"-controller")

	pid, ok := schemaPID(prefix + "model")
	c.Check(ok, tc.IsTrue)
	c.Check(pid, tc.Equals, os.Getpid())
}

func (s *schemaSuite) TestCreateSchema(c *tc.C) {
	admin := OpenAdmin(c)
	name := NewSchemaName(c, "it's")

	CreateSchema(c, admin, name)

	var comment string
	err := admin.QueryRowContext(c.Context(), `
SELECT obj_description(oid, 'pg_namespace') FROM pg_namespace WHERE nspname = $1`,
		name).Scan(&comment)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(comment, tc.Equals, fmt.Sprintf("pid=%d started=%d test=%s",
		os.Getpid(), processStarted.Unix(), c.TestName()))
}

func (s *schemaSuite) TestSweepStaleSchemas(c *tc.C) {
	admin := OpenAdmin(c)

	// PIDs above the Linux maximum are never alive, so concurrent sweeps in
	// other processes agree with this one about the dead and self schemas.
	const (
		deadPID = 4194305
		selfPID = 4194306
	)
	live := fmt.Sprintf("juju-test-1-%d-live", os.Getpid())
	dead := fmt.Sprintf("juju-test-%d-1-dead", deadPID)
	self := fmt.Sprintf("juju-test-%d-1-self", selfPID)
	other := fmt.Sprintf("other-test-%d", os.Getpid())
	for _, name := range []string{live, dead, self, other} {
		quoted := pgx.Identifier{name}.Sanitize()
		_, err := admin.ExecContext(c.Context(), "CREATE SCHEMA "+quoted)
		c.Assert(err, tc.ErrorIsNil)
		c.Cleanup(func() {
			_, err := admin.ExecContext(context.Background(),
				"DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
			c.Check(err, tc.ErrorIsNil)
		})
	}

	sweepStaleSchemas(c, admin, selfPID, func(pid int) bool {
		return pid != deadPID && processAlive(pid)
	})

	for name, want := range map[string]bool{
		live:  true,
		dead:  false,
		self:  false,
		other: true,
	} {
		var exists bool
		err := admin.QueryRowContext(c.Context(),
			`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
			name).Scan(&exists)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(exists, tc.Equals, want, tc.Commentf("%q", name))
	}
}

func (s *schemaSuite) TestOutsideDependentsIgnoresObjectsInside(c *tc.C) {
	admin := OpenAdmin(c)
	name := NewSchemaName(c, "inside")
	CreateSchema(c, admin, name)

	// Each object below depends only on the schema, on objects inside it,
	// or on built-in objects. Some get dependents outside the schema's own
	// catalog rows: TOAST tables in pg_toast, triggers, rules, column
	// defaults, policies and default privileges.
	execf(c, admin, `
CREATE TYPE %[1]s.mood AS ENUM ('sad', 'happy');
CREATE DOMAIN %[1]s.positive AS integer CHECK (VALUE > 0);
CREATE TYPE %[1]s.pair AS (a integer, b text);
CREATE SEQUENCE %[1]s.counter;
CREATE TABLE %[1]s.parent (
    id serial PRIMARY KEY,
    ident integer GENERATED ALWAYS AS IDENTITY,
    name text UNIQUE,
    doubled integer GENERATED ALWAYS AS (id * 2) STORED,
    next integer DEFAULT nextval('%[1]s.counter'),
    mood %[1]s.mood,
    size %[1]s.positive CHECK (size < 100),
    pair %[1]s.pair
);
CREATE TABLE %[1]s.child (
    id integer PRIMARY KEY,
    parent_id integer REFERENCES %[1]s.parent (id) ON DELETE CASCADE
);
CREATE INDEX child_parent ON %[1]s.child (parent_id);
CREATE TABLE %[1]s.heir () INHERITS (%[1]s.child);
CREATE TABLE %[1]s.ranged (id integer) PARTITION BY RANGE (id);
CREATE INDEX ranged_id ON %[1]s.ranged (id);
CREATE TABLE %[1]s.ranged_low PARTITION OF %[1]s.ranged
    FOR VALUES FROM (0) TO (10);
CREATE FUNCTION %[1]s.touch() RETURNS trigger LANGUAGE plpgsql
    AS 'BEGIN RETURN NEW; END';
CREATE TRIGGER touch BEFORE UPDATE ON %[1]s.child
    FOR EACH ROW EXECUTE FUNCTION %[1]s.touch();
CREATE FUNCTION %[1]s.add(integer, integer) RETURNS integer
    LANGUAGE sql IMMUTABLE AS 'SELECT $1 + $2';
CREATE AGGREGATE %[1]s.total(integer) (SFUNC = %[1]s.add, STYPE = integer);
CREATE PROCEDURE %[1]s.noop() LANGUAGE sql AS 'SELECT 1';
CREATE OPERATOR %[1]s.### (
    LEFTARG = integer, RIGHTARG = integer, FUNCTION = %[1]s.add);
CREATE VIEW %[1]s.parent_names AS SELECT name FROM %[1]s.parent;
CREATE MATERIALIZED VIEW %[1]s.totals AS
    SELECT %[1]s.total(id) FROM %[1]s.child;
CREATE RULE keep AS ON DELETE TO %[1]s.parent DO INSTEAD NOTHING;
ALTER TABLE %[1]s.child ENABLE ROW LEVEL SECURITY;
CREATE POLICY visible ON %[1]s.child USING (%[1]s.add(id, 0) > 0);
CREATE STATISTICS %[1]s.child_stats ON id, parent_id FROM %[1]s.child;
CREATE COLLATION %[1]s.bytes FROM "C";
CREATE TEXT SEARCH CONFIGURATION %[1]s.words (COPY = english);
ALTER DEFAULT PRIVILEGES IN SCHEMA %[1]s GRANT SELECT ON TABLES TO PUBLIC;
`, pgx.Identifier{name}.Sanitize())

	deps, err := outsideDependents(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(deps, tc.HasLen, 0)
}

func (s *schemaSuite) TestOutsideDependents(c *tc.C) {
	admin := OpenAdmin(c)
	inside := NewSchemaName(c, "inside")
	outside := NewSchemaName(c, "outside")
	// The cleanups run in reverse, dropping the outside schema first, so
	// they find no outside dependents.
	CreateSchema(c, admin, inside)
	CreateSchema(c, admin, outside)
	in := pgx.Identifier{inside}.Sanitize()
	out := pgx.Identifier{outside}.Sanitize()

	execf(c, admin, `
CREATE TYPE %[1]s.mood AS ENUM ('sad', 'happy');
CREATE SEQUENCE %[1]s.counter;
CREATE TABLE %[1]s.t (id integer PRIMARY KEY);
CREATE TABLE %[1]s.ranged (id integer) PARTITION BY RANGE (id);
CREATE FUNCTION %[1]s.touch() RETURNS trigger LANGUAGE plpgsql
    AS 'BEGIN RETURN NEW; END';
`, in)

	tests := []struct {
		about  string
		create string
		want   []string
	}{{
		about:  "view",
		create: `CREATE VIEW %[2]s.v AS SELECT id FROM %[1]s.t`,
		want: []string{
			`rule _RETURN on view %[2]s.v depends on column id of table %[1]s.t`,
		},
	}, {
		about:  "foreign key",
		create: `CREATE TABLE %[2]s.c (t_id integer REFERENCES %[1]s.t (id))`,
		want: []string{
			`constraint c_t_id_fkey on table %[2]s.c depends on column id of table %[1]s.t`,
			`constraint c_t_id_fkey on table %[2]s.c depends on index %[1]s.t_pkey`,
		},
	}, {
		about: "trigger",
		create: `
CREATE TABLE %[2]s.c (id integer);
CREATE TRIGGER touch BEFORE UPDATE ON %[2]s.c
    FOR EACH ROW EXECUTE FUNCTION %[1]s.touch()`,
		want: []string{
			`trigger touch on table %[2]s.c depends on function %[1]s.touch()`,
		},
	}, {
		about:  "column default",
		create: `CREATE TABLE %[2]s.c (id integer DEFAULT nextval('%[1]s.counter'))`,
		want: []string{
			`default value for column id of table %[2]s.c depends on sequence %[1]s.counter`,
		},
	}, {
		about:  "column type",
		create: `CREATE TABLE %[2]s.c (m %[1]s.mood)`,
		want: []string{
			`column m of table %[2]s.c depends on type %[1]s.mood`,
		},
	}, {
		about:  "function argument type",
		create: `CREATE FUNCTION %[2]s.f(%[1]s.mood) RETURNS integer LANGUAGE sql AS 'SELECT 1'`,
		want: []string{
			`function %[2]s.f(%[1]s.mood) depends on type %[1]s.mood`,
		},
	}, {
		about:  "inheritance",
		create: `CREATE TABLE %[2]s.heir () INHERITS (%[1]s.t)`,
		want: []string{
			`table %[2]s.heir depends on table %[1]s.t`,
		},
	}, {
		about: "partition",
		create: `CREATE TABLE %[2]s.low PARTITION OF %[1]s.ranged
    FOR VALUES FROM (0) TO (10)`,
		want: []string{
			`table %[2]s.low depends on table %[1]s.ranged`,
		},
	}}
	for _, test := range tests {
		c.Logf("test: %s", test.about)
		var want []string
		for _, w := range test.want {
			want = append(want, fmt.Sprintf(w, in, out))
		}

		// The transaction is rolled back, leaving the outside schema empty
		// for the next case.
		func() {
			tx, err := admin.BeginTx(c.Context(), nil)
			c.Assert(err, tc.ErrorIsNil)
			defer func() { _ = tx.Rollback() }()

			execf(c, tx, test.create, in, out)
			deps, err := outsideDependents(c.Context(), tx, inside)
			c.Check(err, tc.ErrorIsNil)
			c.Check(deps, tc.DeepEquals, want)
		}()
	}
}

func (s *schemaSuite) TestDropSchemaReportsOutsideDependents(c *tc.C) {
	admin := OpenAdmin(c)
	inside := NewSchemaName(c, "inside")
	outside := NewSchemaName(c, "outside")
	CreateSchema(c, admin, inside)
	CreateSchema(c, admin, outside)
	execf(c, admin, `
CREATE TABLE %[1]s.t (id integer);
CREATE VIEW %[2]s.v AS SELECT id FROM %[1]s.t;
`, pgx.Identifier{inside}.Sanitize(), pgx.Identifier{outside}.Sanitize())

	var r fakeReporter
	dropSchema(&r, admin, inside)

	c.Check(r.errors, tc.DeepEquals, []string{fmt.Sprintf(
		"dropping schema %q with CASCADE also drops these objects outside "+
			"it, which the test should drop first:\n"+
			"rule _RETURN on view %s.v depends on column id of table %s.t",
		inside, pgx.Identifier{outside}.Sanitize(),
		pgx.Identifier{inside}.Sanitize())})
	c.Check(schemaExists(c, admin, inside), tc.IsFalse)
	c.Check(schemaExists(c, admin, outside), tc.IsTrue)
	var view sql.NullString
	err := admin.QueryRowContext(c.Context(), `SELECT to_regclass($1)::text`,
		pgx.Identifier{outside, "v"}.Sanitize()).Scan(&view)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(view.Valid, tc.IsFalse)
}

func (s *schemaSuite) TestDropSchemaWithoutOutsideDependents(c *tc.C) {
	admin := OpenAdmin(c)
	name := NewSchemaName(c, "")
	CreateSchema(c, admin, name)
	execf(c, admin, `
CREATE TABLE %[1]s.t (id integer);
CREATE VIEW %[1]s.v AS SELECT id FROM %[1]s.t;
`, pgx.Identifier{name}.Sanitize())

	var r fakeReporter
	dropSchema(&r, admin, name)
	dropSchema(&r, admin, NewSchemaName(c, "missing"))

	c.Check(r.errors, tc.HasLen, 0)
	c.Check(schemaExists(c, admin, name), tc.IsFalse)
}

func (s *schemaSuite) TestDropSchemaReportsErrors(c *tc.C) {
	db, err := postgresql.Open("postgres://nobody@127.0.0.1:1/none")
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(db.Close(), tc.ErrorIsNil)

	var r fakeReporter
	dropSchema(&r, db, "juju-test-1-1")

	c.Check(r.errors, tc.DeepEquals, []string{
		`listing objects outside schema "juju-test-1-1" that depend on it: ` +
			`sql: database is closed`,
		`dropping schema "juju-test-1-1": sql: database is closed`,
	})
}

type fakeReporter struct {
	errors []string
}

func (r *fakeReporter) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execf runs SQL formatted with quoted identifiers. With no arguments, the
// driver sends it as a simple query, which may hold several statements.
func execf(c *tc.C, db execer, format string, args ...any) {
	_, err := db.ExecContext(c.Context(), fmt.Sprintf(format, args...))
	c.Assert(err, tc.ErrorIsNil)
}

func schemaExists(c *tc.C, admin *sql.DB, name string) bool {
	var exists bool
	err := admin.QueryRowContext(c.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
		name).Scan(&exists)
	c.Assert(err, tc.ErrorIsNil)
	return exists
}
