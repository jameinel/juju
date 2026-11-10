// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package leaseexpiry_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/juju/tc"
	_ "github.com/mattn/go-sqlite3"

	coredatabase "github.com/juju/juju/core/database"
	"github.com/juju/juju/core/lease"
	"github.com/juju/juju/internal/database"
	databasetesting "github.com/juju/juju/internal/database/testing"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	"github.com/juju/juju/internal/testhelpers"
	"github.com/juju/juju/internal/worker/leaseexpiry"
)

type storeSuite struct {
	testhelpers.IsolationSuite
}

func TestStoreSuite(t *testing.T) {
	tc.Run(t, &storeSuite{})
}

func (s *storeSuite) TestExpireLeasesUsesStdTxnNoRetry(c *tc.C) {
	db, store := s.newStore(c)
	defer func() { c.Check(db.Close(), tc.ErrorIsNil) }()

	_, err := db.ExecContext(c.Context(), `
INSERT INTO lease (uuid, expiry) VALUES
    ('future', datetime('now', '+2 minutes')),
    ('expired', datetime('now', '-2 minutes')),
    ('pinned', datetime('now', '-2 minutes'));
INSERT INTO lease_pin (uuid, lease_uuid) VALUES ('pin', 'pinned');`)
	c.Assert(err, tc.ErrorIsNil)

	err = store.ExpireLeases(c.Context())
	c.Assert(err, tc.ErrorIsNil)

	c.Check(leaseUUIDs(c, db), tc.DeepEquals, []string{"future", "pinned"})
}

func (s *storeSuite) TestExpireLeasesWithControllerClockAhead(c *tc.C) {
	s.checkExpiryWithClockOffset(c, -24*time.Hour)
}

func (s *storeSuite) TestExpireLeasesWithControllerClockBehind(c *tc.C) {
	s.checkExpiryWithClockOffset(c, 24*time.Hour)
}

func (s *storeSuite) checkExpiryWithClockOffset(c *tc.C, offset time.Duration) {
	now := time.Now().UTC().Truncate(time.Second).Add(offset)
	db, _ := databasetesting.OpenSQLiteWithCurrentTimestamp(func() time.Time {
		return now
	})
	defer func() { c.Check(db.Close(), tc.ErrorIsNil) }()
	store := s.storeWithDB(c, db)

	for _, test := range []struct {
		uuid   string
		expiry time.Time
	}{
		{uuid: "expired", expiry: now.Add(-time.Second)},
		{uuid: "equal", expiry: now},
		{uuid: "future", expiry: now.Add(time.Second)},
		{uuid: "pinned", expiry: now.Add(-time.Second)},
	} {
		_, err := db.ExecContext(c.Context(), `
INSERT INTO lease (uuid, expiry) VALUES (?, ?)`,
			test.uuid, test.expiry.Format("2006-01-02 15:04:05"))
		c.Assert(err, tc.ErrorIsNil)
	}
	_, err := db.ExecContext(c.Context(), `
INSERT INTO lease_pin (uuid, lease_uuid) VALUES ('pin', 'pinned')`)
	c.Assert(err, tc.ErrorIsNil)

	err = store.ExpireLeases(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(leaseUUIDs(c, db), tc.DeepEquals, []string{"equal", "future", "pinned"})
}

func (s *storeSuite) TestExpireLeasesSkipsDeleteWhenOnlyExpiredLeaseIsPinned(c *tc.C) {
	db, store := s.newStore(c)
	defer func() { c.Check(db.Close(), tc.ErrorIsNil) }()

	_, err := db.ExecContext(c.Context(), `
INSERT INTO lease (uuid, expiry)
VALUES ('pinned', datetime('now', '-2 minutes'));
INSERT INTO lease_pin (uuid, lease_uuid)
VALUES ('pin', 'pinned');
PRAGMA query_only = ON;`)
	c.Assert(err, tc.ErrorIsNil)

	// The connection is read-only after setup. Success proves that expiry
	// returned after the count query without attempting the delete.
	err = store.ExpireLeases(c.Context())
	c.Assert(err, tc.ErrorIsNil)
}

func (s *storeSuite) newStore(c *tc.C) (*sql.DB, lease.ExpiryStore) {
	db, err := sql.Open("sqlite3", ":memory:")
	c.Assert(err, tc.ErrorIsNil)
	return db, s.storeWithDB(c, db)
}

func (s *storeSuite) storeWithDB(c *tc.C, db *sql.DB) lease.ExpiryStore {
	db.SetMaxOpenConns(1)

	_, err := db.ExecContext(c.Context(), `
CREATE TABLE lease (
    uuid TEXT PRIMARY KEY,
    expiry DATETIME NOT NULL
);
CREATE TABLE lease_pin (
    uuid TEXT PRIMARY KEY,
    lease_uuid TEXT NOT NULL
);`)
	c.Assert(err, tc.ErrorIsNil)

	runner := noRetryTxnRunner{
		TxnRunner: noopTxnRunner{},
		db:        db,
	}
	store, err := leaseexpiry.NewStore(
		c.Context(),
		stubDBGetter{runner: runner},
		loggertesting.WrapCheckLog(c),
	)
	c.Assert(err, tc.ErrorIsNil)

	return store
}

func leaseUUIDs(c *tc.C, db *sql.DB) []string {
	rows, err := db.QueryContext(c.Context(), "SELECT uuid FROM lease ORDER BY uuid")
	c.Assert(err, tc.ErrorIsNil)
	defer func() { c.Check(rows.Close(), tc.ErrorIsNil) }()

	var got []string
	for rows.Next() {
		var leaseUUID string
		c.Assert(rows.Scan(&leaseUUID), tc.ErrorIsNil)
		got = append(got, leaseUUID)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	return got
}

type noRetryTxnRunner struct {
	coredatabase.TxnRunner
	db *sql.DB
}

func (r noRetryTxnRunner) StdTxnNoRetry(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	return database.StdTxn(ctx, r.db, fn)
}
