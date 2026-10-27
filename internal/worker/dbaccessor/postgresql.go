// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"
	"database/sql"
	"time"

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

// deleteLockTimeout bounds each lock wait while deleting a schema. A model's
// schema is busy only while something still uses the model, so a delete
// that times out is retried later by its caller. The value is not tuned.
const deleteLockTimeout = 15 * time.Second

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
// that the database can be reached. A missing model schema returns an error
// satisfying errors.Is(err, coredatabase.ErrDBDead). A missing controller
// schema returns a plain error instead: Juju never deletes it, so its loss
// must fail the dbaccessor worker rather than quietly retire one tracker.
func (n *PostgresNamespaces) Ping(ctx context.Context, namespace string, db *sql.DB) error {
	schemaName := n.placement.SchemaName(namespace)
	exists, err := postgresql.SchemaExists(ctx, db, schemaName)
	if err != nil {
		return errors.Trace(err)
	} else if exists {
		return nil
	}
	if namespace == coredatabase.ControllerNS {
		return errors.Errorf("controller schema %q no longer exists", schemaName)
	}
	return errors.Annotatef(coredatabase.ErrDBDead, "schema %q no longer exists", schemaName)
}

// CheckControllerInitialised returns an error unless the controller schema
// has had its DDL applied. Only bootstrap creates and initialises it.
func (n *PostgresNamespaces) CheckControllerInitialised(ctx context.Context, runner coredatabase.TxnRunner) error {
	schemaName := n.placement.SchemaName(coredatabase.ControllerNS)
	var initialised bool
	if err := runner.StdTxn(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		initialised, err = postgresql.SchemaInitialised(ctx, tx, schemaName)
		return err
	}); err != nil {
		return errors.Trace(err)
	} else if !initialised {
		return errors.Errorf("controller schema %q is not initialised", schemaName)
	}
	return nil
}

// Delete drops the namespace's schema without CASCADE (D47), on a
// short-lived admin pool. Each lock wait is bounded by deleteLockTimeout. An
// object that Juju did not create, in the schema or depending on it from
// outside, fails the delete and nothing is dropped. Deleting a schema that
// does not exist succeeds. The controller namespace cannot be deleted.
func (n *PostgresNamespaces) Delete(ctx context.Context, namespace string) error {
	if namespace == coredatabase.ControllerNS {
		return errors.Forbiddenf("cannot delete controller schema")
	}

	db, err := n.placement.OpenAdmin()
	if err != nil {
		return errors.Annotatef(err, "opening admin pool to delete namespace %q", namespace)
	}
	defer func() { _ = db.Close() }()

	return errors.Trace(postgresql.DeleteSchema(ctx, db, n.placement.SchemaName(namespace), deleteLockTimeout))
}
