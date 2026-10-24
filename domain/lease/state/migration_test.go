// Copyright 2024 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/juju/tc"

	coremodel "github.com/juju/juju/core/model"
	schematesting "github.com/juju/juju/domain/schema/testing"
)

type migrationSuite struct {
	schematesting.ControllerSuite
}

func TestMigrationSuite(t *testing.T) {
	tc.Run(t, &migrationSuite{})
}

func (s *migrationSuite) TestGetApplicationLeadershipForModelNoLeaders(c *tc.C) {
	modelUUID := tc.Must0(c, coremodel.NewUUID)

	state := NewMigrationState(s.TxnRunnerFactory())
	leases, err := state.GetApplicationLeadershipForModel(c.Context(), modelUUID)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leases, tc.HasLen, 0)
}

func (s *migrationSuite) TestGetApplicationLeadershipForModel(c *tc.C) {
	modelUUID := tc.Must0(c, coremodel.NewUUID)

	state := NewMigrationState(s.TxnRunnerFactory())
	start, expiry := leaseWindow()

	err := s.TxnRunner().StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO lease (uuid, lease_type_id, model_uuid, name, holder, start, expiry)
VALUES ('1', 1, ?, 'foo', 'unit', ?, ?)
`, modelUUID, start, expiry)
		return err
	})
	c.Assert(err, tc.ErrorIsNil)

	leases, err := state.GetApplicationLeadershipForModel(c.Context(), modelUUID)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leases, tc.DeepEquals, map[string]string{
		"foo": "unit",
	})
}

func (s *migrationSuite) TestGetApplicationLeadershipForModelSingularControllerType(c *tc.C) {
	modelUUID := tc.Must0(c, coremodel.NewUUID)

	state := NewMigrationState(s.TxnRunnerFactory())
	start, expiry := leaseWindow()

	err := s.TxnRunner().StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO lease (uuid, lease_type_id, model_uuid, name, holder, start, expiry)
VALUES ('1', 1, ?, 'foo', 'unit', ?, ?)
`, modelUUID, start, expiry)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`
INSERT INTO lease (uuid, lease_type_id, model_uuid, name, holder, start, expiry)
VALUES ('2', 0, ?, 'controller', 'abc', ?, ?)
`, modelUUID, start, expiry)
		return err
	})
	c.Assert(err, tc.ErrorIsNil)

	leases, err := state.GetApplicationLeadershipForModel(c.Context(), modelUUID)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leases, tc.DeepEquals, map[string]string{
		"foo": "unit",
	})
}

func (s *migrationSuite) TestGetApplicationLeadershipForModelExpired(c *tc.C) {
	modelUUID := tc.Must0(c, coremodel.NewUUID)

	state := NewMigrationState(s.TxnRunnerFactory())
	now := time.Now()

	err := s.TxnRunner().StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`
INSERT INTO lease (uuid, lease_type_id, model_uuid, name, holder, start, expiry)
VALUES ('1', 1, ?, 'foo', 'unit', ?, ?)
`, modelUUID,
			formatTimestamp(now.Add(-48*time.Hour)),
			formatTimestamp(now.Add(-24*time.Hour)),
		)
		return err
	})
	c.Assert(err, tc.ErrorIsNil)

	leases, err := state.GetApplicationLeadershipForModel(c.Context(), modelUUID)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leases, tc.HasLen, 0)
}

// leaseWindow returns start and expiry timestamps for a lease that is held
// from now for one day.
func leaseWindow() (string, string) {
	now := time.Now()
	return formatTimestamp(now), formatTimestamp(now.Add(24 * time.Hour))
}
