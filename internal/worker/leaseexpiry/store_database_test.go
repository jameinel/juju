// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package leaseexpiry_test

import (
	"testing"

	"github.com/juju/tc"

	schematesting "github.com/juju/juju/domain/schema/testing"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	"github.com/juju/juju/internal/worker/leaseexpiry"
)

type databaseStoreSuite struct {
	schematesting.ControllerSuite
}

func TestDatabaseStoreSuite(t *testing.T) {
	tc.Run(t, &databaseStoreSuite{})
}

func (s *databaseStoreSuite) TestExpireLeases(c *tc.C) {
	db := s.DB()
	runner := noRetryTxnRunner{TxnRunner: s.TxnRunner(), db: db}
	store, err := leaseexpiry.NewStore(
		c.Context(), stubDBGetter{runner: runner}, loggertesting.WrapCheckLog(c),
	)
	c.Assert(err, tc.ErrorIsNil)

	for _, entry := range []struct {
		uuid   string
		expiry any
	}{
		{uuid: "future", expiry: "2999-01-01 00:00:00"},
		{uuid: "no-expiry", expiry: nil},
		{uuid: "pinned", expiry: "2000-01-01 00:00:00"},
	} {
		_, err = db.ExecContext(c.Context(), `
INSERT INTO lease (uuid, lease_type_id, model_uuid, name, holder, start, expiry)
VALUES (?, 1, 'model', ?, 'unit/0', '2000-01-01 00:00:00', ?)`,
			entry.uuid, entry.uuid, entry.expiry)
		c.Assert(err, tc.ErrorIsNil)
	}
	_, err = db.ExecContext(c.Context(), `
INSERT INTO lease_pin (uuid, lease_uuid, entity_id) VALUES ('pin', 'pinned', 'unit/0')`)
	c.Assert(err, tc.ErrorIsNil)

	err = store.ExpireLeases(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leaseUUIDs(c, db), tc.DeepEquals, []string{"future", "no-expiry", "pinned"})

	_, err = db.ExecContext(c.Context(), `
INSERT INTO lease (uuid, lease_type_id, model_uuid, name, holder, start, expiry)
VALUES ('expired', 1, 'model', 'expired', 'unit/0', '2000-01-01 00:00:00', '2000-01-01 00:00:00')`)
	c.Assert(err, tc.ErrorIsNil)
	err = store.ExpireLeases(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leaseUUIDs(c, db), tc.DeepEquals, []string{"future", "no-expiry", "pinned"})
}
