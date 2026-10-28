// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/juju/juju/core/database/dialect"
)

// DefaultSchemaPrefix is prepended to every Juju database namespace to name
// its PostgreSQL schema, for example "juju_controller" and
// "juju_<model-uuid>". It keeps Juju's schemas apart from others in the
// database, such as "public" and the charm's "login_hook".
const DefaultSchemaPrefix = "juju_"

// ErrSchemaExists is returned by CreateSchema when the schema already exists.
var ErrSchemaExists = errors.New("schema already exists")

// SQLSTATE codes for schema creation races and conflicts.
const (
	duplicateSchemaCode = "42P06"
	uniqueViolationCode = "23505"
)

// Placement locates Juju database namespaces in one PostgreSQL database. Each
// namespace is a schema named by SchemaName. It is the single place that maps
// a namespace to a database and schema, so namespaces can later be sharded
// across databases or clusters.
type Placement struct {
	// URL is the connection URL of the database.
	URL string

	// CACert is the PEM CA certificate used to verify the server. It is
	// optional; see WithRootCAs.
	CACert string

	// SchemaPrefix is prepended to each namespace to form its schema name.
	// Empty means DefaultSchemaPrefix. Only tests set another prefix, so that
	// they cannot collide with a controller using the same database.
	SchemaPrefix string
}

// SchemaName returns the PostgreSQL schema holding the namespace.
func (p Placement) SchemaName(namespace string) string {
	prefix := p.SchemaPrefix
	if prefix == "" {
		prefix = DefaultSchemaPrefix
	}
	return prefix + namespace
}

// OpenAdmin returns a pool for schema management. It has no search_path, so
// statements must qualify object names.
func (p Placement) OpenAdmin() (*sql.DB, error) {
	return Open(p.URL, WithRootCAs(p.CACert), WithTimeZoneUTC())
}

// OpenNamespace returns a pool whose search_path is the namespace's schema.
// The schema is not created.
func (p Placement) OpenNamespace(namespace string) (*sql.DB, error) {
	return Open(p.URL,
		WithRootCAs(p.CACert),
		WithSearchPath(p.SchemaName(namespace)),
		WithTimeZoneUTC(),
	)
}

// Execer runs statements; both *sql.DB and *sql.Tx implement it.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Querier runs queries; both *sql.DB and *sql.Tx implement it.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CreateSchema creates the schema. It returns an error satisfying
// errors.Is(err, ErrSchemaExists) if the schema already exists.
func CreateSchema(ctx context.Context, db Execer, name string) error {
	_, err := db.ExecContext(ctx, "CREATE SCHEMA "+QuoteIdentifier(name))
	if isPGCode(err, duplicateSchemaCode) {
		return fmt.Errorf("creating schema %q: %w", name, ErrSchemaExists)
	} else if err != nil {
		return fmt.Errorf("creating schema %q: %w", name, err)
	}
	return nil
}

// EnsureSchema creates the schema if it does not exist. It tolerates another
// session creating the same schema concurrently.
func EnsureSchema(ctx context.Context, db Execer, name string) error {
	_, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+QuoteIdentifier(name))
	// IF NOT EXISTS checks the catalogue before inserting, so a concurrent
	// creation can still fail on the catalogue's unique index.
	if err != nil && !isPGCode(err, uniqueViolationCode) && !isPGCode(err, duplicateSchemaCode) {
		return fmt.Errorf("ensuring schema %q: %w", name, err)
	}
	return nil
}

// DropSchema drops the schema and everything in it, with CASCADE. Dropping a
// schema that does not exist is not an error. It is for tests only: Juju
// deletes a namespace's schema with DeleteSchema, which never cascades.
func DropSchema(ctx context.Context, db Execer, name string) error {
	_, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+QuoteIdentifier(name)+" CASCADE")
	if err != nil {
		return fmt.Errorf("dropping schema %q: %w", name, err)
	}
	return nil
}

// DeleteSchema drops a Juju namespace's schema without CASCADE, so it removes
// only the kinds of object that Juju creates there. In one transaction it
// lists the schema's objects from pg_catalog and drops its views, then its
// tables (with their indexes, triggers and owned sequences), then its
// functions, then any remaining sequences, and finally the schema, each with
// RESTRICT. Any other object in the schema, or an object outside it that
// depends on one inside, fails the delete and nothing is dropped. The error
// then carries PostgreSQL's detail, which names the dependent objects.
//
// Each lock wait is bounded by lockTimeout, which must be at least a
// millisecond. The first is for the database's schema change lock
// (dialect.Dialect.BeginSchemaChangeSQL), which the delete holds until it
// ends, so it never runs alongside another Juju schema change on the
// database. Deleting a schema that does not exist succeeds, as does
// deleting one that another session deletes concurrently.
func DeleteSchema(ctx context.Context, db *sql.DB, name string, lockTimeout time.Duration) error {
	if lockTimeout < time.Millisecond {
		return fmt.Errorf("deleting schema %q: lock timeout %v is less than a millisecond", name, lockTimeout)
	}

	// The transaction reads pg_catalog after waiting for locks, so each
	// statement needs a fresh snapshot: a SERIALIZABLE snapshot would still
	// list objects that a concurrent delete has since dropped, and regclass
	// renders a dropped relation as its bare OID. SERIALIZABLE (D7) adds
	// nothing here, as PostgreSQL does not track reads of system catalogs.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("deleting schema %q: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := deleteSchema(ctx, tx, name, lockTimeout); err != nil {
		return fmt.Errorf("deleting schema %q: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("deleting schema %q: committing: %w", name, withDetail(err))
	}
	return nil
}

// deleteSteps are the kinds of object that DeleteSchema drops, in order. Each
// list query takes the schema name as its only parameter and returns names
// that PostgreSQL has quoted and qualified for the session's search_path.
var deleteSteps = []struct {
	kind string
	drop string
	list string
}{{
	kind: "views",
	drop: "VIEW",
	list: listRelations("v"),
}, {
	kind: "tables",
	drop: "TABLE",
	list: listRelations("r"),
}, {
	kind: "functions",
	drop: "FUNCTION",
	list: `
SELECT p.oid::pg_catalog.regprocedure::text
FROM pg_catalog.pg_proc AS p
JOIN pg_catalog.pg_namespace AS n ON n.oid = p.pronamespace
WHERE n.nspname = $1 AND p.prokind = 'f'
ORDER BY p.oid`,
}, {
	kind: "sequences",
	drop: "SEQUENCE",
	list: listRelations("S"),
}}

// listRelations returns the query listing a schema's relations of one
// pg_class relkind: "v" for views, "r" for tables and "S" for sequences.
func listRelations(relkind string) string {
	return `
SELECT c.oid::pg_catalog.regclass::text
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relkind = '` + relkind + `'
ORDER BY c.oid`
}

func deleteSchema(ctx context.Context, tx *sql.Tx, name string, lockTimeout time.Duration) error {
	// SET takes no parameters, so the value is formatted into the statement.
	_, err := tx.ExecContext(ctx,
		"SET LOCAL lock_timeout = "+strconv.FormatInt(lockTimeout.Milliseconds(), 10))
	if err != nil {
		return fmt.Errorf("setting lock timeout: %w", err)
	}

	// Wait for any other schema change on the database to finish, so that
	// this delete's locks are not added to theirs in the shared lock table.
	// This follows SET LOCAL, so that lockTimeout bounds the wait.
	if _, err := tx.ExecContext(ctx, dialect.PostgreSQL.BeginSchemaChangeSQL()); err != nil {
		return fmt.Errorf("beginning schema change: %w", err)
	}

	if exists, err := SchemaExists(ctx, tx, name); err != nil {
		return err
	} else if !exists {
		return nil
	}

	// Each list is read after the previous drop, so it holds only what
	// remains, such as sequences that no table owned. IF EXISTS skips an
	// object that a concurrent delete dropped after it was listed.
	for _, step := range deleteSteps {
		names, err := listNames(ctx, tx, step.list, name)
		if err != nil {
			return fmt.Errorf("listing %s: %w", step.kind, err)
		} else if len(names) == 0 {
			continue
		}
		stmt := "DROP " + step.drop + " IF EXISTS " + strings.Join(names, ", ") + " RESTRICT"
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("dropping %s: %w", step.kind, withDetail(err))
		}
	}

	_, err = tx.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+QuoteIdentifier(name)+" RESTRICT")
	if err != nil {
		return fmt.Errorf("dropping schema: %w", withDetail(err))
	}
	return nil
}

func listNames(ctx context.Context, tx *sql.Tx, query, schemaName string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, schemaName)
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

// withDetail appends a PostgreSQL error's detail, which pgconn leaves out of
// the error's message. For a failed drop it names the dependent objects.
func withDetail(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Detail != "" {
		return fmt.Errorf("%w: %s", err, pgErr.Detail)
	}
	return err
}

// SchemaExists reports whether the schema exists.
func SchemaExists(ctx context.Context, db Querier, name string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = $1)", name,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking schema %q exists: %w", name, err)
	}
	return exists, nil
}

// SchemaInitialised reports whether Juju's DDL has been applied to the
// schema: the schema has a "schema" bookkeeping table with at least one
// version row. A failed first DDL run rolls back without leaving a row, so
// an empty schema, or one holding other objects, is not initialised.
func SchemaInitialised(ctx context.Context, db Querier, name string) (bool, error) {
	var hasTable bool
	err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_class AS c
    JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
    WHERE n.nspname = $1 AND c.relname = 'schema' AND c.relkind = 'r'
)`, name).Scan(&hasTable)
	if err != nil {
		return false, fmt.Errorf("checking schema %q is initialised: %w", name, err)
	} else if !hasTable {
		return false, nil
	}

	var hasVersion bool
	err = db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+QuoteIdentifier(name)+`."schema")`,
	).Scan(&hasVersion)
	if err != nil {
		return false, fmt.Errorf("checking schema %q is initialised: %w", name, err)
	}
	return hasVersion, nil
}

func isPGCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
