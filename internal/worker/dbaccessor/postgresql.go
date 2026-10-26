// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"
	"database/sql"

	"github.com/juju/errors"

	coredatabase "github.com/juju/juju/core/database"
	"github.com/juju/juju/core/logger"
	"github.com/juju/juju/domain/schema"
	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/postgresql"
)

// PostgresNamespaces stores each Juju database namespace in its own
// PostgreSQL schema, located by a Placement. It is both the DBOpener and the
// TrackedDBBackend of a tracked PostgreSQL database.
type PostgresNamespaces struct {
	placement postgresql.Placement
}

// NewPostgresNamespaces returns PostgresNamespaces for the placement.
func NewPostgresNamespaces(placement postgresql.Placement) *PostgresNamespaces {
	return &PostgresNamespaces{placement: placement}
}

// Open returns a pool whose search_path is the namespace's schema. It does
// not connect or create the schema.
func (n *PostgresNamespaces) Open(_ context.Context, namespace string) (*sql.DB, error) {
	db, err := n.placement.OpenNamespace(namespace)
	if err != nil {
		return nil, errors.Annotatef(err, "opening namespace %q", namespace)
	}
	return db, nil
}

// PrepareDB does nothing: PostgreSQL always enforces foreign keys.
func (n *PostgresNamespaces) PrepareDB(context.Context, *sql.DB) error {
	return nil
}

// EnsureModelInitialised creates the namespace's schema if it does not exist,
// then applies the model DDL unless the schema is already initialised.
func (n *PostgresNamespaces) EnsureModelInitialised(
	ctx context.Context, namespace string, db *sql.DB,
	runner coredatabase.TxnRunner, logger logger.Logger,
) error {
	schemaName := n.placement.SchemaName(namespace)

	// This runs outside a transaction: a creation race that EnsureSchema
	// tolerates would still abort the transaction it ran in.
	if err := postgresql.EnsureSchema(ctx, db, schemaName); err != nil {
		return errors.Trace(err)
	}

	var initialised bool
	if err := runner.StdTxn(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		initialised, err = postgresql.SchemaInitialised(ctx, tx, schemaName)
		return err
	}); err != nil {
		return errors.Trace(err)
	} else if initialised {
		return nil
	}

	ddl, err := schema.ModelDDLForBackend(schema.BackendPostgreSQL)
	if err != nil {
		return errors.Trace(err)
	}
	return errors.Trace(database.NewDBMigration(runner, logger, ddl).Apply(ctx))
}

// Ping checks that the namespace's schema still exists, which also checks
// that the database can be reached. A missing schema returns an error
// satisfying errors.Is(err, coredatabase.ErrDBDead).
func (n *PostgresNamespaces) Ping(ctx context.Context, namespace string, db *sql.DB) error {
	schemaName := n.placement.SchemaName(namespace)
	exists, err := postgresql.SchemaExists(ctx, db, schemaName)
	if err != nil {
		return errors.Trace(err)
	} else if !exists {
		return errors.Annotatef(coredatabase.ErrDBDead, "schema %q no longer exists", schemaName)
	}
	return nil
}
