// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/canonical/sqlair"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juju/clock"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/drivererrors"
	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	"github.com/juju/juju/internal/database/txn"
	"github.com/juju/juju/internal/logger"
	coretesting "github.com/juju/juju/internal/testing"
)

type driverSuite struct {
	coretesting.BaseSuite

	url    string
	schema string
	db     *sql.DB
}

func TestDriverSuite(t *testing.T) {
	tc.Run(t, &driverSuite{})
}

func (s *driverSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)

	admin := pgtesting.OpenAdmin(c)
	s.url = pgtesting.URL()

	// Test schema names contain hyphens, which exercise search_path quoting.
	s.schema = pgtesting.NewSchemaName(c, "")
	pgtesting.CreateSchema(c, admin, s.schema)

	var err error
	s.db, err = postgresql.Open(
		s.url,
		postgresql.WithSearchPath(s.schema),
		postgresql.WithTimeZoneUTC(),
	)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = s.db.Close() })

	_, err = s.db.ExecContext(c.Context(), `
CREATE TABLE parent (id INT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE child (
    id INT PRIMARY KEY,
    parent_id INT NOT NULL REFERENCES parent(id),
    n INT CHECK (n > 0)
);`)
	c.Assert(err, tc.ErrorIsNil)
}

func (s *driverSuite) TestSearchPathAndSettings(c *tc.C) {
	var schema, iso, timeZone string
	err := s.db.QueryRowContext(c.Context(), `SELECT current_schema()`).Scan(&schema)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(schema, tc.Equals, s.schema)

	err = s.db.QueryRowContext(c.Context(), `SHOW default_transaction_isolation`).Scan(&iso)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(iso, tc.Equals, "serializable")

	err = s.db.QueryRowContext(c.Context(), `SHOW timezone`).Scan(&timeZone)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(timeZone, tc.Equals, "UTC")
}

func (s *driverSuite) TestSessionRoleSurvivesPoolReuse(c *tc.C) {
	// The role must not change between connections taken from the pool.
	var first string
	err := s.db.QueryRowContext(c.Context(), `SELECT current_user`).Scan(&first)
	c.Assert(err, tc.ErrorIsNil)

	for i := 0; i < 5; i++ {
		var got string
		err := s.db.QueryRowContext(c.Context(), `SELECT current_user`).Scan(&got)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(got, tc.Equals, first)
	}
}

func (s *driverSuite) TestPositionalPlaceholders(c *tc.C) {
	_, err := s.db.ExecContext(c.Context(), `INSERT INTO parent (id, name) VALUES (?, ?)`, 1, "a")
	c.Assert(err, tc.ErrorIsNil)

	var name string
	err = s.db.QueryRowContext(c.Context(), `SELECT name FROM parent WHERE id = ? AND name = ?`, 1, "a").Scan(&name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(name, tc.Equals, "a")
}

func (s *driverSuite) TestNamedPlaceholdersRepeated(c *tc.C) {
	_, err := s.db.ExecContext(c.Context(), `INSERT INTO parent (id, name) VALUES (@sqlair_0, @sqlair_1)`,
		sql.Named("sqlair_1", "b"), sql.Named("sqlair_0", 2))
	c.Assert(err, tc.ErrorIsNil)

	var n int
	err = s.db.QueryRowContext(c.Context(),
		`SELECT count(*) FROM parent WHERE id = @sqlair_0 OR id + 1 = @sqlair_0 OR name = @sqlair_1`,
		sql.Named("sqlair_0", 2), sql.Named("sqlair_1", "b")).Scan(&n)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(n, tc.Equals, 1)
}

func (s *driverSuite) TestPreparedStatement(c *tc.C) {
	stmt, err := s.db.PrepareContext(c.Context(), `INSERT INTO parent (id, name) VALUES (@sqlair_0, @sqlair_1)`)
	c.Assert(err, tc.ErrorIsNil)
	defer stmt.Close()

	for i := 0; i < 3; i++ {
		_, err = stmt.ExecContext(c.Context(),
			sql.Named("sqlair_1", fmt.Sprintf("n%d", i)), sql.Named("sqlair_0", i))
		c.Assert(err, tc.ErrorIsNil)
	}

	rows, err := s.db.QueryContext(c.Context(), `SELECT id FROM parent WHERE name = ?`, "n2")
	c.Assert(err, tc.ErrorIsNil)
	defer rows.Close()
	c.Assert(rows.Next(), tc.IsTrue)
	var id int
	c.Assert(rows.Scan(&id), tc.ErrorIsNil)
	c.Check(id, tc.Equals, 2)
}

func (s *driverSuite) TestWrongArgumentCount(c *tc.C) {
	_, err := s.db.ExecContext(c.Context(), `INSERT INTO parent (id, name) VALUES (?, ?)`, 1)
	c.Check(err, tc.NotNil)
}

func (s *driverSuite) TestTimestamptzScannedInUTC(c *tc.C) {
	conn, err := s.db.Conn(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	defer conn.Close()
	_, err = conn.ExecContext(c.Context(), `SET TIME ZONE 'America/New_York'`)
	c.Assert(err, tc.ErrorIsNil)

	in := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3*60*60))
	var out time.Time
	err = conn.QueryRowContext(c.Context(), `SELECT ?::timestamptz`, in).Scan(&out)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(out.Location(), tc.Equals, time.UTC)
	c.Check(out.Equal(in), tc.IsTrue)
}

func (s *driverSuite) TestMultiStatementWithoutArgs(c *tc.C) {
	_, err := s.db.ExecContext(c.Context(), `
INSERT INTO parent (id, name) VALUES (10, 'x');
INSERT INTO parent (id, name) VALUES (11, '?');`)
	c.Assert(err, tc.ErrorIsNil)

	var q string
	err = s.db.QueryRowContext(c.Context(), `SELECT name FROM parent WHERE id = 11`).Scan(&q)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(q, tc.Equals, "?")
}

func (s *driverSuite) TestSqlair(c *tc.C) {
	type Parent struct {
		ID   int    `db:"id"`
		Name string `db:"name"`
	}

	sdb := sqlair.NewDB(s.db)
	insert, err := sqlair.Prepare(`INSERT INTO parent (id, name) VALUES ($Parent.id, $Parent.name)`, Parent{})
	c.Assert(err, tc.ErrorIsNil)
	selectStmt, err := sqlair.Prepare(`
SELECT &Parent.* FROM parent
WHERE id = $Parent.id OR name = $Parent.name`, Parent{})
	c.Assert(err, tc.ErrorIsNil)

	err = sdb.Query(c.Context(), insert, Parent{ID: 5, Name: "five"}).Run()
	c.Assert(err, tc.ErrorIsNil)

	var got []Parent
	err = sdb.Query(c.Context(), selectStmt, Parent{ID: 5, Name: "five"}).GetAll(&got)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.DeepEquals, []Parent{{ID: 5, Name: "five"}})

	err = sdb.Query(c.Context(), selectStmt, Parent{ID: 6, Name: "six"}).GetAll(&got)
	c.Check(err, tc.ErrorIs, sqlair.ErrNoRows)
}

func (s *driverSuite) TestSqlairEmptySlice(c *tc.C) {
	type Count struct {
		N int `db:"n"`
	}
	type IDs []int
	type Names []string

	_, err := s.db.ExecContext(c.Context(), `
INSERT INTO parent (id, name) VALUES (1, 'a');
INSERT INTO child (id, parent_id, n) VALUES (1, 1, NULL), (2, 1, 5);`)
	c.Assert(err, tc.ErrorIsNil)

	sdb := sqlair.NewDB(s.db)
	count := func(query string, args ...any) int {
		stmt, err := sqlair.Prepare(query, append([]any{Count{}}, args...)...)
		c.Assert(err, tc.ErrorIsNil)
		var got Count
		err = sdb.Query(c.Context(), stmt, args...).Get(&got)
		c.Assert(err, tc.ErrorIsNil)
		return got.N
	}

	// An empty list matches nothing with IN and everything with NOT IN,
	// including a NULL operand, as on SQLite.
	c.Check(count(`SELECT count(*) AS &Count.n FROM child WHERE n IN ($IDs[:])`, IDs{}), tc.Equals, 0)
	c.Check(count(`SELECT count(*) AS &Count.n FROM child WHERE n NOT IN ($IDs[:])`, IDs{}), tc.Equals, 2)
	c.Check(count(`SELECT count(*) AS &Count.n FROM parent WHERE name IN ($Names[:])`, Names{}), tc.Equals, 0)
	c.Check(count(`SELECT count(*) AS &Count.n FROM parent WHERE name NOT IN ($Names[:])`, Names{}), tc.Equals, 1)
	c.Check(count(`SELECT count(*) AS &Count.n FROM child WHERE n NOT IN ($IDs[:])`, IDs{5}), tc.Equals, 0)
}

func (s *driverSuite) TestConstraintErrors(c *tc.C) {
	exec := func(q string, args ...any) error {
		_, err := s.db.ExecContext(c.Context(), q, args...)
		return err
	}
	c.Assert(exec(`INSERT INTO parent (id, name) VALUES (1, 'a')`), tc.ErrorIsNil)

	err := exec(`INSERT INTO parent (id, name) VALUES (1, 'b')`)
	c.Check(database.IsErrConstraintPrimaryKey(err), tc.IsTrue)
	c.Check(database.IsErrConstraintUnique(err), tc.IsFalse)

	err = exec(`INSERT INTO parent (id, name) VALUES (2, 'a')`)
	c.Check(database.IsErrConstraintUnique(err), tc.IsTrue)
	c.Check(database.IsErrConstraintPrimaryKey(err), tc.IsFalse)

	err = exec(`INSERT INTO parent (id, name) VALUES (3, NULL)`)
	c.Check(database.IsErrConstraintNotNull(err), tc.IsTrue)

	err = exec(`INSERT INTO child (id, parent_id, n) VALUES (1, 99, 1)`)
	c.Check(database.IsErrConstraintForeignKey(err), tc.IsTrue)

	err = exec(`INSERT INTO child (id, parent_id, n) VALUES (1, 1, 0)`)
	c.Check(database.IsErrConstraintCheck(err), tc.IsTrue)

	err = exec(`DO $$ BEGIN RAISE EXCEPTION 'nope'; END $$`)
	c.Check(database.IsErrConstraintTrigger(err), tc.IsTrue)

	for _, e := range []error{err} {
		c.Check(drivererrors.IsConstraintError(e), tc.IsTrue)
		c.Check(drivererrors.IsError(e), tc.IsTrue)
		c.Check(database.IsErrRetryable(e), tc.IsFalse)
	}

	err = exec(`SELECT * FROM missing`)
	c.Check(drivererrors.IsError(err), tc.IsTrue)
	c.Check(drivererrors.IsConstraintError(err), tc.IsFalse)
	c.Check(database.IsErrRetryable(err), tc.IsFalse)
}

// TestSerializationFailureRetried forces a write skew between two
// transactions. The loser fails with 40001, which the transaction runner must
// retry until it succeeds.
func (s *driverSuite) TestSerializationFailureRetried(c *tc.C) {
	ctx := c.Context()
	_, err := s.db.ExecContext(ctx, `INSERT INTO parent (id, name) VALUES (1, 'a'), (2, 'b')`)
	c.Assert(err, tc.ErrorIsNil)

	// Each transaction reads the whole table and writes a row the other has
	// read. Under SERIALIZABLE exactly one must fail.
	tx1, err := s.db.BeginTx(ctx, nil)
	c.Assert(err, tc.ErrorIsNil)
	tx2, err := s.db.BeginTx(ctx, nil)
	c.Assert(err, tc.ErrorIsNil)

	var n int
	c.Assert(tx1.QueryRowContext(ctx, `SELECT count(*) FROM parent`).Scan(&n), tc.ErrorIsNil)
	c.Assert(tx2.QueryRowContext(ctx, `SELECT count(*) FROM parent`).Scan(&n), tc.ErrorIsNil)
	_, err = tx1.ExecContext(ctx, `UPDATE parent SET name = 'x' WHERE id = 1`)
	c.Assert(err, tc.ErrorIsNil)
	_, err = tx2.ExecContext(ctx, `UPDATE parent SET name = 'y' WHERE id = 2`)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(tx1.Commit(), tc.ErrorIsNil)

	err = tx2.Commit()
	c.Assert(err, tc.NotNil)
	c.Check(drivererrors.IsPGErrorCode(err, "40001"), tc.IsTrue)
	c.Check(database.IsErrRetryable(err), tc.IsTrue)

	// The runner retries a body that fails with a serialisation failure.
	runner := txn.NewRetryingTxnRunner(
		txn.WithRetryStrategy(txn.DefaultRetryStrategy(clock.WallClock, logger.GetLogger("test"))),
	)
	attempts := 0
	err = runner.Retry(ctx, func() error {
		return runner.StdTxn(ctx, s.db, func(ctx context.Context, tx *sql.Tx) error {
			attempts++
			if attempts < 3 {
				return &pgconn.PgError{Code: "40001"}
			}
			return nil
		})
	})
	c.Assert(err, tc.ErrorIsNil)
	c.Check(attempts, tc.Equals, 3)
}
