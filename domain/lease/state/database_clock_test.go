// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state_test

import (
	"context"
	"database/sql"
	stderrors "errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/canonical/sqlair"
	"github.com/juju/tc"

	coredatabase "github.com/juju/juju/core/database"
	corelease "github.com/juju/juju/core/lease"
	"github.com/juju/juju/domain/lease/state"
	databasetesting "github.com/juju/juju/internal/database/testing"
	coretesting "github.com/juju/juju/internal/testing"
	"github.com/juju/juju/internal/uuid"
)

type databaseClockSuite struct {
	coretesting.BaseSuite
}

func TestDatabaseClockSuite(t *testing.T) {
	tc.Run(t, &databaseClockSuite{})
}

func (s *databaseClockSuite) TestClaimLeaseUsesDatabaseClock(c *tc.C) {
	db, runner, clock := newClockDatabase(c)
	store := stateWithRunner(runner)
	now := time.Unix(0, clock.Load()).UTC()

	for _, test := range []struct {
		name     string
		duration time.Duration
		expiry   time.Duration
	}{
		{name: "zero"},
		{name: "whole-second", duration: 3 * time.Second, expiry: 3 * time.Second},
		{name: "fractional", duration: 3*time.Second + time.Nanosecond, expiry: 4 * time.Second},
	} {
		key := corelease.Key{
			Namespace: "application-leadership",
			ModelUUID: "model",
			Lease:     test.name,
		}
		err := store.ClaimLease(c.Context(), uuid.MustNewUUID(), key, corelease.Request{
			Holder:   "unit/0",
			Duration: test.duration,
		})
		c.Assert(err, tc.ErrorIsNil)

		start, expiry := readClockLease(c, db, test.name)
		c.Check(start, tc.Equals, now.Format("2006-01-02 15:04:05"))
		c.Check(expiry, tc.Equals, now.Add(test.expiry).Format("2006-01-02 15:04:05"))
	}
}

func (s *databaseClockSuite) TestClaimLeaseReadsTimeAgainOnRetry(c *tc.C) {
	db, runner, clock := newClockDatabase(c)
	initial := time.Unix(0, clock.Load()).UTC()
	store := stateWithRunner(advancingTxnRunner{TxnRunner: runner, clock: clock})
	key := corelease.Key{
		Namespace: "application-leadership",
		ModelUUID: "model",
		Lease:     "claim",
	}

	err := store.ClaimLease(c.Context(), uuid.MustNewUUID(), key, corelease.Request{
		Holder:   "unit/0",
		Duration: 2 * time.Minute,
	})
	c.Assert(err, tc.ErrorIsNil)

	start, expiry := readClockLease(c, db, key.Lease)
	c.Check(start, tc.Equals, initial.Add(time.Minute).Format("2006-01-02 15:04:05"))
	c.Check(expiry, tc.Equals, initial.Add(3*time.Minute).Format("2006-01-02 15:04:05"))
}

func (s *databaseClockSuite) TestExtendLeaseReadsTimeAgainOnRetry(c *tc.C) {
	db, runner, clock := newClockDatabase(c)
	initial := time.Unix(0, clock.Load()).UTC()
	key := corelease.Key{
		Namespace: "application-leadership",
		ModelUUID: "model",
		Lease:     "extend",
	}
	req := corelease.Request{Holder: "unit/0", Duration: time.Second}
	err := stateWithRunner(runner).ClaimLease(c.Context(), uuid.MustNewUUID(), key, req)
	c.Assert(err, tc.ErrorIsNil)

	req.Duration = 2*time.Minute + time.Nanosecond
	store := stateWithRunner(advancingTxnRunner{TxnRunner: runner, clock: clock})
	err = store.ExtendLease(c.Context(), key, req)
	c.Assert(err, tc.ErrorIsNil)

	start, expiry := readClockLease(c, db, key.Lease)
	c.Check(start, tc.Equals, initial.Format("2006-01-02 15:04:05"))
	c.Check(expiry, tc.Equals, initial.Add(3*time.Minute+time.Second).Format("2006-01-02 15:04:05"))
}

func newClockDatabase(c *tc.C) (*sql.DB, coredatabase.TxnRunner, *atomic.Int64) {
	var clock atomic.Int64
	clock.Store(time.Now().UTC().Truncate(time.Second).Add(24 * time.Hour).UnixNano())
	db, runner := databasetesting.OpenSQLiteWithCurrentTimestamp(func() time.Time {
		return time.Unix(0, clock.Load())
	})
	c.Cleanup(func() { c.Check(db.Close(), tc.ErrorIsNil) })

	_, err := db.ExecContext(c.Context(), `
CREATE TABLE lease_type (id INTEGER PRIMARY KEY, type TEXT);
INSERT INTO lease_type VALUES (1, 'application-leadership');
CREATE TABLE lease (
    uuid TEXT PRIMARY KEY,
    lease_type_id INTEGER NOT NULL,
    model_uuid TEXT,
    name TEXT,
    holder TEXT,
    start TIMESTAMP,
    expiry TIMESTAMP
);
CREATE UNIQUE INDEX idx_lease ON lease (model_uuid, lease_type_id, name);`)
	c.Assert(err, tc.ErrorIsNil)

	var got string
	err = db.QueryRowContext(c.Context(), `SELECT CURRENT_TIMESTAMP`).Scan(&got)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.Equals, time.Unix(0, clock.Load()).UTC().Format("2006-01-02 15:04:05"))
	return db, runner, &clock
}

func stateWithRunner(runner coredatabase.TxnRunner) *state.State {
	return state.NewState(func(context.Context) (coredatabase.TxnRunner, error) {
		return runner, nil
	})
}

func readClockLease(c *tc.C, db *sql.DB, name string) (string, string) {
	var start, expiry string
	err := db.QueryRowContext(c.Context(), `
SELECT CAST(start AS TEXT), CAST(expiry AS TEXT)
FROM lease
WHERE name = ?`, name).Scan(&start, &expiry)
	c.Assert(err, tc.ErrorIsNil)
	return start, expiry
}

type advancingTxnRunner struct {
	coredatabase.TxnRunner
	clock *atomic.Int64
}

func (r advancingTxnRunner) Txn(ctx context.Context, fn func(context.Context, *sqlair.TX) error) error {
	retryErr := stderrors.New("retry test transaction")
	err := r.TxnRunner.Txn(ctx, func(ctx context.Context, tx *sqlair.TX) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return retryErr
	})
	if !stderrors.Is(err, retryErr) {
		if err == nil {
			return stderrors.New("first test transaction did not roll back")
		}
		return err
	}
	r.clock.Add(int64(time.Minute))
	return r.TxnRunner.Txn(ctx, fn)
}
