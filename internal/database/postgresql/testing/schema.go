// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package testing provides helpers for tests that use a real PostgreSQL.
//
// Tests opt in by setting JUJU_POSTGRESQL_TEST_URL. Each test works in its
// own schema named juju-test-<pid>-<n>[-<suffix>]. The prefix never collides
// with production schema names, and the hyphens exercise identifier quoting.
// A schema created by CreateSchema records the owning process and test in its
// comment.
//
// When the test finishes, its schemas are dropped with CASCADE, removing
// whatever the test left in them. The test fails if the drop also removes
// objects outside its schemas, such as a view in another schema that reads
// one of its tables.
//
// A test run that panics or is killed leaves its schemas behind. The first
// OpenAdmin call in each process sweeps them: it drops every test schema
// whose process is no longer running, and every schema carrying this
// process's PID, which can only belong to an earlier process that had the
// same PID. The sweep relies on every test process using a database sharing
// one PID namespace, so they must all run on the same host.
package testing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/postgresql"
)

const (
	// URLEnvVar names the environment variable holding the URL of the
	// PostgreSQL that opt-in tests use.
	URLEnvVar = "JUJU_POSTGRESQL_TEST_URL"

	// SchemaNamePrefix starts the name of every test schema.
	SchemaNamePrefix = "juju-test-"

	// maxIdentifierLength is the longest identifier PostgreSQL keeps.
	// Longer names are silently truncated.
	maxIdentifierLength = 63
)

var (
	schemaNameRE = regexp.MustCompile(`^juju-test-([0-9]+)-[0-9]+(-.*)?$`)

	processStarted = time.Now()
	schemaCounter  atomic.Uint64

	sweepOnce sync.Once
	swept     atomic.Bool
)

// sessionTimeouts bound how long a test session waits for a lock, runs a
// statement or sits idle in a transaction. A blocked test then fails with a
// PostgreSQL error instead of waiting for the go test timeout. The slowest
// expected statement, a model schema apply, takes a few seconds.
var sessionTimeouts = []struct{ name, value string }{
	{name: "lock_timeout", value: "30s"},
	{name: "statement_timeout", value: "60s"},
	{name: "idle_in_transaction_session_timeout", value: "60s"},
}

// URL returns the PostgreSQL URL for tests, or "" when tests should not use
// PostgreSQL. It adds each of sessionTimeouts that the URL does not set.
func URL() string {
	return withSessionTimeouts(os.Getenv(URLEnvVar))
}

// withSessionTimeouts appends the missing sessionTimeouts to a connection
// string in URL or keyword/value form, without rewriting the rest of it.
// The driver passes unrecognised parameters to the server as settings. A
// string it cannot parse is returned unchanged for the driver to report,
// because the driver redacts the password from its errors.
func withSessionTimeouts(conn string) string {
	if conn == "" {
		return ""
	}
	if !strings.HasPrefix(conn, "postgres://") &&
		!strings.HasPrefix(conn, "postgresql://") {
		for _, t := range sessionTimeouts {
			if !keywordSetRE(t.name).MatchString(conn) {
				conn += " " + t.name + "=" + t.value
			}
		}
		return conn
	}

	_, rawQuery, hasQuery := strings.Cut(conn, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return conn
	}
	sep := "&"
	if !hasQuery {
		sep = "?"
	} else if rawQuery == "" {
		sep = ""
	}
	for _, t := range sessionTimeouts {
		if !query.Has(t.name) {
			conn += sep + t.name + "=" + t.value
			sep = "&"
		}
	}
	return conn
}

func keywordSetRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(name) + `\s*=`)
}

// OpenAdmin opens a connection to the test PostgreSQL with the default
// search path, for creating and dropping schemas. It skips the test when
// URLEnvVar is not set, and closes the connection when the test finishes.
// The first call in each process sweeps schemas left by earlier processes.
func OpenAdmin(c *tc.C) *sql.DB {
	url := URL()
	if url == "" {
		c.Skip(URLEnvVar + " is not set")
	}

	admin, err := postgresql.Open(url)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = admin.Close() })

	sweepOnce.Do(func() {
		defer swept.Store(true)

		if _, err := os.Stat("/proc/self"); err != nil {
			c.Logf("not sweeping stale PostgreSQL test schemas: %v", err)
			return
		}
		sweepStaleSchemas(c, admin, os.Getpid(), processAlive)
	})
	return admin
}

// NewSchemaName returns a test schema name that is unique to this process.
// A non-empty suffix is appended after a hyphen. OpenAdmin must be called
// first, so that the sweep cannot drop the schema.
func NewSchemaName(c *tc.C, suffix string) string {
	name := nextSchemaBase(c)
	if suffix != "" {
		name += "-" + suffix
	}
	checkSchemaNameLength(c, name)
	return name
}

// NewSchemaPrefix returns a prefix for code under test that names its own
// schemas. Every schema named with the prefix is a test schema. OpenAdmin
// must be called first, so that the sweep cannot drop the schemas.
func NewSchemaPrefix(c *tc.C) string {
	prefix := nextSchemaBase(c) + "-"
	checkSchemaNameLength(c, prefix)
	return prefix
}

// CreateSchema creates the named schema, records the owning process and
// test in its comment, and drops the schema when the test finishes. The
// test fails if the drop removes objects outside the schema.
func CreateSchema(c *tc.C, admin *sql.DB, name string) {
	quoted := pgx.Identifier{name}.Sanitize()
	_, err := admin.ExecContext(c.Context(), "CREATE SCHEMA "+quoted)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() {
		dropSchema(c, admin, name)
	})

	comment := fmt.Sprintf("pid=%d started=%d test=%s",
		os.Getpid(), processStarted.Unix(), c.TestName())
	_, err = admin.ExecContext(c.Context(),
		fmt.Sprintf("COMMENT ON SCHEMA %s IS %s", quoted, quoteLiteral(comment)))
	c.Assert(err, tc.ErrorIsNil)
}

// DropSchemasOnCleanup drops every schema whose name starts with prefix
// when the test finishes. It is for schemas created by code under test. The
// test fails if a drop removes objects outside the schema being dropped,
// including objects in another of the dropped schemas.
func DropSchemasOnCleanup(c *tc.C, admin *sql.DB, prefix string) {
	if !strings.HasPrefix(prefix, SchemaNamePrefix) {
		c.Fatalf("schema prefix %q does not start with %q", prefix, SchemaNamePrefix)
	}
	c.Cleanup(func() {
		names, err := schemaNames(context.Background(), admin)
		if err != nil {
			c.Errorf("listing test schemas: %v", err)
			return
		}
		for _, name := range names {
			if strings.HasPrefix(name, prefix) {
				dropSchema(c, admin, name)
			}
		}
	})
}

func nextSchemaBase(c *tc.C) string {
	if !swept.Load() {
		c.Fatalf("OpenAdmin must be called before naming PostgreSQL test schemas")
	}
	return fmt.Sprintf("%s%d-%d", SchemaNamePrefix, os.Getpid(), schemaCounter.Add(1))
}

func checkSchemaNameLength(c *tc.C, name string) {
	if len(name) > maxIdentifierLength {
		c.Fatalf("PostgreSQL test schema name %q is longer than %d bytes",
			name, maxIdentifierLength)
	}
}

// sweepStaleSchemas drops test schemas whose process is not alive, and test
// schemas carrying self as their PID. It logs failures rather than failing
// the test.
func sweepStaleSchemas(c *tc.C, admin *sql.DB, self int, alive func(int) bool) {
	names, err := schemaNames(c.Context(), admin)
	if err != nil {
		c.Logf("listing PostgreSQL test schemas to sweep: %v", err)
		return
	}
	for _, name := range names {
		pid, ok := schemaPID(name)
		if !ok || (pid != self && alive(pid)) {
			continue
		}
		_, err := admin.ExecContext(c.Context(),
			"DROP SCHEMA IF EXISTS "+pgx.Identifier{name}.Sanitize()+" CASCADE")
		if err != nil {
			c.Logf("dropping stale PostgreSQL test schema %q: %v", name, err)
		}
	}
}

// schemaPID returns the PID recorded in a test schema name.
func schemaPID(name string) (int, bool) {
	m := schemaNameRE.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return pid, true
}

// processAlive reports whether a process with the PID exists. It answers
// true when it cannot tell, so that the sweep keeps the schema.
func processAlive(pid int) bool {
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return !errors.Is(err, fs.ErrNotExist)
}

func schemaNames(ctx context.Context, admin *sql.DB) ([]string, error) {
	rows, err := admin.QueryContext(ctx,
		`SELECT nspname FROM pg_namespace WHERE starts_with(nspname, $1)`,
		SchemaNamePrefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// reporter fails a test. *tc.C implements it.
type reporter interface {
	Errorf(format string, args ...any)
}

// dropSchema drops a test schema and everything in it with CASCADE, so that
// the database is clean for later tests. It fails the test if the drop also
// removes objects outside the schema. A test that makes objects elsewhere
// depend on its schema must remove them before it finishes.
func dropSchema(r reporter, admin *sql.DB, name string) {
	ctx := context.Background()
	outside, err := outsideDependents(ctx, admin, name)
	if err != nil {
		r.Errorf("listing objects outside schema %q that depend on it: %v",
			name, err)
	} else if len(outside) > 0 {
		r.Errorf("dropping schema %q with CASCADE also drops these objects "+
			"outside it, which the test should drop first:\n%s",
			name, strings.Join(outside, "\n"))
	}

	_, err = admin.ExecContext(ctx,
		"DROP SCHEMA IF EXISTS "+pgx.Identifier{name}.Sanitize()+" CASCADE")
	if err != nil {
		r.Errorf("dropping schema %q: %v", name, err)
	}
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// outsideDependents describes each dependency of an object outside the named
// schema on the schema or an object inside it. Dropping the schema with
// CASCADE drops those outside objects too.
//
// An object is inside when its catalog row names the schema, or when it
// belongs to a relation or operator family that does. Objects in catalogs
// not listed count as outside, so a gap reports too much rather than too
// little. Internal dependents are skipped: they are part of the object they
// depend on and are dropped even without CASCADE, like a table's TOAST
// table in pg_toast. A chain of dependents that leaves the schema has a
// first link outside it, so only direct dependents need checking.
func outsideDependents(ctx context.Context, db queryer, name string) ([]string, error) {
	rows, err := db.QueryContext(ctx, outsideDependentsQuery, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var deps []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, err
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

const outsideDependentsQuery = `
WITH target AS (
    SELECT oid FROM pg_namespace WHERE nspname = $1
),
inside_rel AS (
    SELECT c.oid FROM pg_class c JOIN target t ON c.relnamespace = t.oid
),
inside_opf AS (
    SELECT f.oid FROM pg_opfamily f JOIN target t ON f.opfnamespace = t.oid
),
inside (classid, objid) AS (
    SELECT 'pg_namespace'::regclass, oid FROM target
    UNION ALL SELECT 'pg_class'::regclass, oid FROM inside_rel
    UNION ALL SELECT 'pg_opfamily'::regclass, oid FROM inside_opf
    UNION ALL SELECT 'pg_type'::regclass, x.oid
        FROM pg_type x JOIN target t ON x.typnamespace = t.oid
    UNION ALL SELECT 'pg_proc'::regclass, x.oid
        FROM pg_proc x JOIN target t ON x.pronamespace = t.oid
    UNION ALL SELECT 'pg_constraint'::regclass, x.oid
        FROM pg_constraint x JOIN target t ON x.connamespace = t.oid
    UNION ALL SELECT 'pg_collation'::regclass, x.oid
        FROM pg_collation x JOIN target t ON x.collnamespace = t.oid
    UNION ALL SELECT 'pg_conversion'::regclass, x.oid
        FROM pg_conversion x JOIN target t ON x.connamespace = t.oid
    UNION ALL SELECT 'pg_operator'::regclass, x.oid
        FROM pg_operator x JOIN target t ON x.oprnamespace = t.oid
    UNION ALL SELECT 'pg_opclass'::regclass, x.oid
        FROM pg_opclass x JOIN target t ON x.opcnamespace = t.oid
    UNION ALL SELECT 'pg_statistic_ext'::regclass, x.oid
        FROM pg_statistic_ext x JOIN target t ON x.stxnamespace = t.oid
    UNION ALL SELECT 'pg_ts_config'::regclass, x.oid
        FROM pg_ts_config x JOIN target t ON x.cfgnamespace = t.oid
    UNION ALL SELECT 'pg_ts_dict'::regclass, x.oid
        FROM pg_ts_dict x JOIN target t ON x.dictnamespace = t.oid
    UNION ALL SELECT 'pg_ts_parser'::regclass, x.oid
        FROM pg_ts_parser x JOIN target t ON x.prsnamespace = t.oid
    UNION ALL SELECT 'pg_ts_template'::regclass, x.oid
        FROM pg_ts_template x JOIN target t ON x.tmplnamespace = t.oid
    UNION ALL SELECT 'pg_default_acl'::regclass, x.oid
        FROM pg_default_acl x JOIN target t ON x.defaclnamespace = t.oid
    UNION ALL SELECT 'pg_trigger'::regclass, x.oid
        FROM pg_trigger x JOIN inside_rel r ON x.tgrelid = r.oid
    UNION ALL SELECT 'pg_rewrite'::regclass, x.oid
        FROM pg_rewrite x JOIN inside_rel r ON x.ev_class = r.oid
    UNION ALL SELECT 'pg_attrdef'::regclass, x.oid
        FROM pg_attrdef x JOIN inside_rel r ON x.adrelid = r.oid
    UNION ALL SELECT 'pg_policy'::regclass, x.oid
        FROM pg_policy x JOIN inside_rel r ON x.polrelid = r.oid
    UNION ALL SELECT 'pg_amop'::regclass, x.oid
        FROM pg_amop x JOIN inside_opf f ON x.amopfamily = f.oid
    UNION ALL SELECT 'pg_amproc'::regclass, x.oid
        FROM pg_amproc x JOIN inside_opf f ON x.amprocfamily = f.oid
)
-- A description is NULL when another session has just dropped the object.
SELECT dep FROM (
    SELECT DISTINCT
        pg_describe_object(d.classid, d.objid, d.objsubid) || ' depends on ' ||
        pg_describe_object(d.refclassid, d.refobjid, d.refobjsubid) AS dep
    FROM pg_depend d
    JOIN inside ref ON ref.classid = d.refclassid AND ref.objid = d.refobjid
    WHERE d.deptype <> 'i'
    AND NOT EXISTS (
        SELECT 1 FROM inside dep
        WHERE dep.classid = d.classid AND dep.objid = d.objid
    )
) deps
WHERE dep IS NOT NULL
ORDER BY dep
`

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
