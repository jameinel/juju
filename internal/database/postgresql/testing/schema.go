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

// URL returns the PostgreSQL URL for tests, or "" when tests should not use
// PostgreSQL.
func URL() string {
	return os.Getenv(URLEnvVar)
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
// test in its comment, and drops the schema when the test finishes.
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
// when the test finishes. It is for schemas created by code under test.
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

func dropSchema(c *tc.C, admin *sql.DB, name string) {
	_, err := admin.ExecContext(context.Background(),
		"DROP SCHEMA IF EXISTS "+pgx.Identifier{name}.Sanitize()+" CASCADE")
	c.Check(err, tc.ErrorIsNil, tc.Commentf("dropping schema %q", name))
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
