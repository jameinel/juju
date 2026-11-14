// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/canonical/sqlair"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/drivererrors"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type driverSuite struct {
	coretesting.BaseSuite
	db *sql.DB
}

func TestDriverSuite(t *testing.T) {
	tc.Run(t, &driverSuite{})
}

func (s *driverSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)
	s.db = pgtesting.Open(c)
	_, err := s.db.ExecContext(c.Context(), `
CREATE TABLE parent (id INT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE child (id INT PRIMARY KEY, parent_id INT REFERENCES parent(id), n INT CHECK (n > 0));
`)
	c.Assert(err, tc.ErrorIsNil)
}

func (s *driverSuite) TestBackendAndSettings(c *tc.C) {
	var version int
	var name, isolation, timezone string
	err := s.db.QueryRowContext(c.Context(), `
SELECT current_setting('server_version_num')::int, current_schema(),
       current_setting('default_transaction_isolation'), current_setting('timezone')
`).Scan(&version, &name, &isolation, &timezone)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(version >= 160000, tc.IsTrue)
	c.Check(name, tc.HasPrefix, "juju-pgtest-")
	c.Check(isolation, tc.Equals, "serializable")
	c.Check(timezone, tc.Equals, "UTC")
}

func (s *driverSuite) TestParametersAndPreparedStatements(c *tc.C) {
	_, err := s.db.ExecContext(c.Context(), "INSERT INTO parent VALUES (?, ?)", 1, "one")
	c.Assert(err, tc.ErrorIsNil)
	insert, err := s.db.PrepareContext(c.Context(), "INSERT INTO parent VALUES (?, ?)")
	c.Assert(err, tc.ErrorIsNil)
	defer func() { c.Check(insert.Close(), tc.ErrorIsNil) }()
	_, err = insert.ExecContext(c.Context(), 2, "two")
	c.Assert(err, tc.ErrorIsNil)
	stmt, err := s.db.PrepareContext(c.Context(), `
SELECT name FROM parent WHERE id = @sqlair_0 OR id + 1 = @sqlair_0 OR name = @sqlair_1
`)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { c.Check(stmt.Close(), tc.ErrorIsNil) }()
	var got string
	err = stmt.QueryRowContext(c.Context(), sql.Named("sqlair_1", "one"), sql.Named("sqlair_0", 1)).Scan(&got)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.Equals, "one")
	err = s.db.QueryRowContext(c.Context(), "SELECT name FROM parent WHERE id = $1", 1).Scan(&got)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.Equals, "one")
	_, err = s.db.ExecContext(c.Context(), "INSERT INTO parent VALUES (?, ?)", 2)
	c.Check(err, tc.NotNil)
	_, err = s.db.ExecContext(c.Context(), "SELECT 1 WHERE 1 IN ()")
	c.Check(err, tc.NotNil)
	c.Check(drivererrors.IsErrRetryable(err), tc.IsFalse)
}

func (s *driverSuite) TestSqlairValues(c *tc.C) {
	type values struct {
		ID   int            `db:"id"`
		Name sql.NullString `db:"name"`
		Flag sql.NullBool   `db:"flag"`
	}
	_, err := s.db.ExecContext(c.Context(), "CREATE TABLE value_test (id INT PRIMARY KEY, name TEXT, flag BOOLEAN)")
	c.Assert(err, tc.ErrorIsNil)
	insert, err := sqlair.Prepare(
		"INSERT INTO value_test (id, name, flag) VALUES ($values.id, $values.name, $values.flag)", values{})
	c.Assert(err, tc.ErrorIsNil)
	query, err := sqlair.Prepare("SELECT &values.* FROM value_test ORDER BY id", values{})
	c.Assert(err, tc.ErrorIsNil)
	db := sqlair.NewDB(s.db)
	want := []values{
		{ID: 1},
		{ID: 2, Name: sql.NullString{String: "two", Valid: true}, Flag: sql.NullBool{Bool: true, Valid: true}},
		{ID: 3, Name: sql.NullString{Valid: true}, Flag: sql.NullBool{Valid: true}},
	}
	for _, value := range want {
		c.Assert(db.Query(c.Context(), insert, value).Run(), tc.ErrorIsNil)
	}
	var got []values
	c.Assert(db.Query(c.Context(), query).GetAll(&got), tc.ErrorIsNil)
	c.Check(got, tc.DeepEquals, want)
}

func (s *driverSuite) TestTimestamps(c *tc.C) {
	input := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("test", 3*60*60))
	var output time.Time
	err := s.db.QueryRowContext(c.Context(), "SELECT ?::timestamptz", input).Scan(&output)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(output.Location(), tc.Equals, time.UTC)
	c.Check(output.Equal(input), tc.IsTrue)
}

func (s *driverSuite) TestCommitSerializationFailure(c *tc.C) {
	ctx := c.Context()
	_, err := s.db.ExecContext(ctx, "INSERT INTO parent VALUES (1, 'one'), (2, 'two')")
	c.Assert(err, tc.ErrorIsNil)
	first, err := s.db.BeginTx(ctx, nil)
	c.Assert(err, tc.ErrorIsNil)
	second, err := s.db.BeginTx(ctx, nil)
	c.Assert(err, tc.ErrorIsNil)
	var count int
	c.Assert(first.QueryRowContext(ctx, "SELECT count(*) FROM parent").Scan(&count), tc.ErrorIsNil)
	c.Assert(second.QueryRowContext(ctx, "SELECT count(*) FROM parent").Scan(&count), tc.ErrorIsNil)
	_, err = first.ExecContext(ctx, "UPDATE parent SET name = 'first' WHERE id = 1")
	c.Assert(err, tc.ErrorIsNil)
	_, err = second.ExecContext(ctx, "UPDATE parent SET name = 'second' WHERE id = 2")
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(first.Commit(), tc.ErrorIsNil)
	err = second.Commit()
	c.Assert(err, tc.NotNil)
	c.Check(database.IsErrRetryable(err), tc.IsTrue)
	c.Check(drivererrors.IsConstraintError(err), tc.IsFalse)
}

func (s *driverSuite) TestConstraintErrors(c *tc.C) {
	_, err := s.db.ExecContext(c.Context(), "INSERT INTO parent VALUES (1, 'one')")
	c.Assert(err, tc.ErrorIsNil)
	tests := []struct {
		query string
		check func(error) bool
	}{
		{"INSERT INTO parent VALUES (1, 'two')", database.IsErrConstraintPrimaryKey},
		{"INSERT INTO parent VALUES (2, 'one')", database.IsErrConstraintUnique},
		{"INSERT INTO parent VALUES (2, NULL)", database.IsErrConstraintNotNull},
		{"INSERT INTO child VALUES (1, 99, 1)", database.IsErrConstraintForeignKey},
		{"INSERT INTO child VALUES (1, 1, 0)", database.IsErrConstraintCheck},
		{"DO $$ BEGIN RAISE EXCEPTION 'immutable'; END $$", database.IsErrConstraintTrigger},
	}
	for _, test := range tests {
		_, err := s.db.ExecContext(c.Context(), test.query)
		c.Assert(err, tc.NotNil)
		c.Check(test.check(err), tc.IsTrue)
		c.Check(drivererrors.IsConstraintError(err), tc.IsTrue)
		c.Check(drivererrors.IsError(err), tc.IsTrue)
		c.Check(database.IsErrRetryable(err), tc.IsFalse)
	}
}

func (s *driverSuite) TestCommitRollbackAndRetry(c *tc.C) {
	ctx := c.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	c.Assert(err, tc.ErrorIsNil)
	_, err = tx.ExecContext(ctx, "INSERT INTO parent VALUES (1, 'one')")
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(tx.Rollback(), tc.ErrorIsNil)
	var count int
	c.Assert(s.db.QueryRowContext(ctx, "SELECT count(*) FROM parent").Scan(&count), tc.ErrorIsNil)
	c.Check(count, tc.Equals, 0)
	_, err = s.db.ExecContext(ctx, "INSERT INTO parent VALUES (1, 'one')")
	c.Assert(err, tc.ErrorIsNil)

	var conflict sync.Once
	attempts := 0
	err = database.Retry(ctx, func() error {
		attempts++
		return database.StdTxn(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
			var name string
			if err := tx.QueryRowContext(ctx, "SELECT name FROM parent WHERE id = 1").Scan(&name); err != nil {
				return err
			}
			// Commit a conflicting write once after establishing the snapshot.
			var externalErr error
			conflict.Do(func() {
				_, externalErr = s.db.ExecContext(ctx, "UPDATE parent SET name = 'external' WHERE id = 1")
			})
			if externalErr != nil {
				return externalErr
			}
			_, err := tx.ExecContext(ctx, "UPDATE parent SET name = 'committed' WHERE id = 1")
			return err
		})
	})
	c.Assert(err, tc.ErrorIsNil)
	c.Check(attempts, tc.Equals, 2)
	var name string
	c.Assert(s.db.QueryRowContext(ctx, "SELECT name FROM parent WHERE id = 1").Scan(&name), tc.ErrorIsNil)
	c.Check(name, tc.Equals, "committed")
}

func (s *driverSuite) TestCancellation(c *tc.C) {
	conn, err := s.db.Conn(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	defer func() { c.Check(conn.Close(), tc.ErrorIsNil) }()
	var pid int
	c.Assert(conn.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&pid), tc.ErrorIsNil)
	blocker, err := s.db.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { c.Check(blocker.Rollback(), tc.ErrorIsNil) }()
	_, err = blocker.ExecContext(c.Context(), "SELECT pg_advisory_xact_lock(1869968498, ?)", pid)
	c.Assert(err, tc.ErrorIsNil)
	ctx, cancel := context.WithCancel(c.Context())
	done := make(chan error, 1)
	defer func() {
		cancel()
		for range done {
		}
	}()
	go func() {
		defer close(done)
		_, err := conn.ExecContext(ctx, "SELECT pg_advisory_xact_lock(1869968498, ?)", pid)
		done <- err
	}()
	for {
		select {
		case err := <-done:
			c.Fatalf("blocking query ended before cancellation: %v", err)
		default:
		}
		var waiting bool
		// pg_locks is visible even when the login role switches to an owner
		// role that cannot see its sessions' pg_stat_activity details.
		err := s.db.QueryRowContext(c.Context(), `
SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid = ? AND locktype = 'advisory' AND NOT granted)
`, pid).Scan(&waiting)
		c.Assert(err, tc.ErrorIsNil)
		if waiting {
			break
		}
	}
	cancel()
	err = <-done
	c.Check(err, tc.NotNil)
	c.Check(database.IsErrRetryable(err), tc.IsFalse)
	c.Assert(s.db.PingContext(c.Context()), tc.ErrorIsNil)
}
