// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package database

import (
	"context"
	"database/sql"

	coredatabase "github.com/juju/juju/core/database"
	"github.com/juju/juju/core/logger"
	"github.com/juju/juju/core/model"
	"github.com/juju/juju/domain/schema"
	"github.com/juju/juju/internal/database/postgresql"
	"github.com/juju/juju/internal/errors"
)

// PostgresInitialiser bootstraps a controller's databases in an external
// PostgreSQL database.
type PostgresInitialiser struct {
	placement postgresql.Placement
	logger    logger.Logger
}

// NewPostgresInitialiser returns a PostgresInitialiser that creates the
// controller's schemas where placement locates them.
func NewPostgresInitialiser(placement postgresql.Placement, logger logger.Logger) *PostgresInitialiser {
	return &PostgresInitialiser{
		placement: placement,
		logger:    logger,
	}
}

// Initialise creates the controller and controller model schemas, then runs
// the bootstrap operations against them.
func (i *PostgresInitialiser) Initialise(
	ctx context.Context, controllerModelUUID model.UUID, opts ...BootstrapOpt,
) error {
	return BootstrapPostgres(ctx, i.placement, controllerModelUUID, i.logger, opts...)
}

// BootstrapPostgres creates the schemas for the controller and the controller
// model, and runs the DDL to create their tables. It fails if either schema
// exists: a new controller never adopts existing state. Each controller needs
// its own PostgreSQL database.
//
// Like BootstrapDqlite, a failure part way through leaves partial state that
// must be removed before bootstrapping again.
//
// It accepts an optional list of functions to perform operations on the
// controller and model databases.
func BootstrapPostgres(
	ctx context.Context,
	placement postgresql.Placement,
	uuid model.UUID,
	logger logger.Logger,
	opts ...BootstrapOpt,
) error {
	controllerDDL, err := schema.ControllerDDLForBackend(schema.BackendPostgreSQL)
	if err != nil {
		return errors.Capture(err)
	}
	modelDDL, err := schema.ModelDDLForBackend(schema.BackendPostgreSQL)
	if err != nil {
		return errors.Capture(err)
	}

	if err := createBootstrapSchemas(ctx, placement,
		coredatabase.ControllerNS, uuid.String(),
	); err != nil {
		return errors.Capture(err)
	}

	controllerDB, err := placement.OpenNamespace(coredatabase.ControllerNS)
	if err != nil {
		return errors.Errorf("opening controller database: %w", err)
	}
	defer closeDB(ctx, controllerDB, logger)
	controller := &txnRunner{db: controllerDB}
	if err := NewDBMigration(controller, logger, controllerDDL).Apply(ctx); err != nil {
		return errors.Errorf("running controller migration: %w", err)
	}

	// The controller node must exist before the seed operations run, as it
	// is required for referential integrity. PostgreSQL controllers are not
	// Dqlite nodes, so they have no node ID or bind address.
	if err := insertPostgresControllerNode(ctx, controller); err != nil {
		return errors.Errorf("inserting controller node: %w", err)
	}

	modelDB, err := placement.OpenNamespace(uuid.String())
	if err != nil {
		return errors.Errorf("opening model database: %w", err)
	}
	defer closeDB(ctx, modelDB, logger)
	modelRunner := &txnRunner{db: modelDB}
	if err := NewDBMigration(modelRunner, logger, modelDDL).Apply(ctx); err != nil {
		return errors.Errorf("running model migration: %w", err)
	}

	for i, op := range opts {
		if err := op(ctx, controller, modelRunner); err != nil {
			return errors.Errorf("running bootstrap operation at index %d: %w", i, err)
		}
	}
	return nil
}

// createBootstrapSchemas creates the schemas for the namespaces in one
// transaction, so that both or neither exist if creation fails.
func createBootstrapSchemas(ctx context.Context, placement postgresql.Placement, namespaces ...string) error {
	admin, err := placement.OpenAdmin()
	if err != nil {
		return errors.Errorf("opening database: %w", err)
	}
	defer func() { _ = admin.Close() }()

	tx, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return errors.Errorf("creating schemas: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, ns := range namespaces {
		if err := postgresql.CreateSchema(ctx, tx, placement.SchemaName(ns)); errors.Is(err, postgresql.ErrSchemaExists) {
			return errors.Errorf(
				"%w: bootstrap requires a database with no Juju state", err,
			)
		} else if err != nil {
			return errors.Capture(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.Errorf("creating schemas: %w", err)
	}
	return nil
}

func insertPostgresControllerNode(ctx context.Context, runner coredatabase.TxnRunner) error {
	return runner.StdTxn(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO controller_node (controller_id) VALUES ('0')`)
		return errors.Capture(err)
	})
}

func closeDB(ctx context.Context, db *sql.DB, logger logger.Logger) {
	if err := db.Close(); err != nil {
		logger.Errorf(ctx, "closing bootstrap database: %v", err)
	}
}
