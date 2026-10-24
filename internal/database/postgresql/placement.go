// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
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

// DropSchema drops the schema and everything in it. Dropping a schema that
// does not exist is not an error.
func DropSchema(ctx context.Context, db Execer, name string) error {
	_, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+QuoteIdentifier(name)+" CASCADE")
	if err != nil {
		return fmt.Errorf("dropping schema %q: %w", name, err)
	}
	return nil
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

func isPGCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
