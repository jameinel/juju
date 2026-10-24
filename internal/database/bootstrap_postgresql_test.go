// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/juju/tc"

	coredatabase "github.com/juju/juju/core/database"
	coremodel "github.com/juju/juju/core/model"
	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type bootstrapPostgresSuite struct {
	coretesting.BaseSuite

	placement postgresql.Placement
	admin     *sql.DB
	modelUUID coremodel.UUID
}

func TestBootstrapPostgresSuite(t *testing.T) {
	tc.Run(t, &bootstrapPostgresSuite{})
}

func (s *bootstrapPostgresSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)

	s.admin = pgtesting.OpenAdmin(c)
	// A test prefix keeps these schemas apart from a real controller's
	// juju_controller schema in the same database.
	s.placement = postgresql.Placement{
		URL:          pgtesting.URL(),
		SchemaPrefix: pgtesting.NewSchemaPrefix(c),
	}
	pgtesting.DropSchemasOnCleanup(c, s.admin, s.placement.SchemaPrefix)
	s.modelUUID = tc.Must0(c, coremodel.NewUUID)
}

func (s *bootstrapPostgresSuite) TestBootstrapPostgres(c *tc.C) {
	var called bool
	check := func(ctx context.Context, controller, model coredatabase.TxnRunner) error {
		called = true
		err := controller.StdTxn(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var count int
			err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM lease_type").Scan(&count)
			c.Assert(err, tc.ErrorIsNil)
			c.Check(count, tc.Equals, 3)

			var (
				controllerID string
				nodeID       sql.NullString
				bindAddress  sql.NullString
			)
			err = tx.QueryRowContext(ctx,
				"SELECT controller_id, dqlite_node_id, dqlite_bind_address FROM controller_node",
			).Scan(&controllerID, &nodeID, &bindAddress)
			c.Assert(err, tc.ErrorIsNil)
			c.Check(controllerID, tc.Equals, "0")
			c.Check(nodeID.Valid, tc.IsFalse)
			c.Check(bindAddress.Valid, tc.IsFalse)
			return nil
		})
		if err != nil {
			return err
		}
		return model.StdTxn(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var schemaName string
			err := tx.QueryRowContext(ctx,
				"SELECT table_schema FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'change_log'",
			).Scan(&schemaName)
			c.Assert(err, tc.ErrorIsNil)
			c.Check(schemaName, tc.Equals, s.placement.SchemaName(s.modelUUID.String()))
			return nil
		})
	}

	initialiser := NewPostgresInitialiser(s.placement, loggertesting.WrapCheckLog(c))
	err := initialiser.Initialise(c.Context(), s.modelUUID, check)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(called, tc.IsTrue)

	for _, ns := range []string{coredatabase.ControllerNS, s.modelUUID.String()} {
		exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName(ns))
		c.Assert(err, tc.ErrorIsNil)
		c.Check(exists, tc.IsTrue)
	}
}

func (s *bootstrapPostgresSuite) TestBootstrapPostgresFailsIfControllerExists(c *tc.C) {
	err := postgresql.CreateSchema(c.Context(), s.admin, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Assert(err, tc.ErrorIsNil)

	err = BootstrapPostgres(c.Context(), s.placement, s.modelUUID, loggertesting.WrapCheckLog(c))
	c.Check(err, tc.ErrorIs, postgresql.ErrSchemaExists)

	// The model schema creation is rolled back with the controller's.
	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName(s.modelUUID.String()))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)
}

func (s *bootstrapPostgresSuite) TestBootstrapPostgresFailsIfModelExists(c *tc.C) {
	err := postgresql.CreateSchema(c.Context(), s.admin, s.placement.SchemaName(s.modelUUID.String()))
	c.Assert(err, tc.ErrorIsNil)

	err = BootstrapPostgres(c.Context(), s.placement, s.modelUUID, loggertesting.WrapCheckLog(c))
	c.Check(err, tc.ErrorIs, postgresql.ErrSchemaExists)

	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)
}

func (s *bootstrapPostgresSuite) TestBootstrapPostgresTwiceFails(c *tc.C) {
	err := BootstrapPostgres(c.Context(), s.placement, s.modelUUID, loggertesting.WrapCheckLog(c))
	c.Assert(err, tc.ErrorIsNil)

	err = BootstrapPostgres(c.Context(), s.placement, s.modelUUID, loggertesting.WrapCheckLog(c))
	c.Check(err, tc.ErrorIs, postgresql.ErrSchemaExists)
	c.Check(err, tc.ErrorMatches, `.*"`+s.placement.SchemaName(coredatabase.ControllerNS)+`".*no Juju state`)
}
