// Copyright 2023 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"context"
	"database/sql"
	"testing"

	"github.com/canonical/gomock/gomock"
	"github.com/juju/tc"

	"github.com/juju/juju/core/database"
	"github.com/juju/juju/core/database/dialect"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	databasetesting "github.com/juju/juju/internal/database/testing"
	"github.com/juju/juju/internal/testhelpers"
)

type patchSuite struct {
	testhelpers.IsolationSuite

	tx *MockTx
}

func TestPatchSuite(t *testing.T) {
	tc.Run(t, &patchSuite{})
}

func (s *patchSuite) TestPatchHash(c *tc.C) {
	defer s.setupMocks(c).Finish()

	patch := MakePatch("SELECT 1")
	c.Assert(patch, tc.NotNil)
	c.Assert(patch.hash, tc.Equals, "4ATr1bVTKkuFmEpi+K1IqBqjRgwcoHcB84YTXXLN7PU=")
}

func (s *patchSuite) TestPatchHashWithSpaces(c *tc.C) {
	defer s.setupMocks(c).Finish()

	patch := MakePatch(`
                SELECT 1
`)
	c.Assert(patch, tc.NotNil)
	c.Assert(patch.hash, tc.Equals, "4ATr1bVTKkuFmEpi+K1IqBqjRgwcoHcB84YTXXLN7PU=")
}

func (s *patchSuite) TestPatchRun(c *tc.C) {
	defer s.setupMocks(c).Finish()

	patch := MakePatch("SELECT * FROM schema_master", 1, 2, "a")

	s.tx.EXPECT().ExecContext(gomock.Any(), "SELECT * FROM schema_master", 1, 2, "a").Return(nil, nil)

	err := patch.run(c.Context(), s.tx)
	c.Assert(err, tc.ErrorIsNil)
}

func (s *patchSuite) setupMocks(c *tc.C) *gomock.Controller {
	ctrl := gomock.NewController(c)

	s.tx = NewMockTx(ctrl)

	return ctrl
}

type schemaSuite struct {
	databasetesting.DqliteSuite
}

func TestSchemaSuite(t *testing.T) {
	tc.Run(t, &schemaSuite{})
}

func (s *schemaSuite) TestSchemaAdd(c *tc.C) {
	schema := New(
		MakePatch("SELECT 1"),
		MakePatch("SELECT 2"),
	)
	c.Check(schema.Len(), tc.Equals, 2)

	schema.Add(MakePatch("SELECT 3"))
	c.Check(schema.Len(), tc.Equals, 3)
	schema.Add(MakePatch("SELECT 4"))
	c.Check(schema.Len(), tc.Equals, 4)
}

func (s *schemaSuite) TestNewDefaultsToSQLite(c *tc.C) {
	c.Check(New().Dialect(), tc.Equals, dialect.SQLite)
	c.Check(NewForDialect(dialect.PostgreSQL).Dialect(), tc.Equals, dialect.PostgreSQL)
}

func (s *schemaSuite) TestEnsureWithNoPatches(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect())
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 0})
}

func (s *schemaSuite) TestSchemaRunMultipleTimes(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})

	schema = NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err = schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 2, Post: 2})
}

func (s *schemaSuite) TestSchemaRunMultipleTimesWithAdditions(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})

	schema = NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE baz (id INTEGER PRIMARY KEY);"),
	)
	current, err = schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 2, Post: 3})
}

func (s *schemaSuite) TestEnsure(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})
}

func (s *schemaSuite) TestEnsureIdempotent(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})

	current, err = schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 2, Post: 2})
}

func (s *schemaSuite) TestEnsureTwiceWithAdditionalChanges(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})

	schema.Add(MakePatch("CREATE TEMP TABLE baz (id INTEGER PRIMARY KEY);"))

	current, err = schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 2, Post: 3})

	schema.Add(MakePatch("CREATE TEMP TABLE alice (id INTEGER PRIMARY KEY);"))

	current, err = schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 3, Post: 4})
}

func (s *schemaSuite) TestEnsureHashBreaks(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TEMP TABLE foo (id INTEGER PRIMARY KEY);"),
		MakePatch("CREATE TEMP TABLE bar (id INTEGER PRIMARY KEY);"),
	)
	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})

	err = s.TxnRunner().StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE schema SET hash = 'blah' WHERE version=2;")
		return err
	})
	c.Assert(err, tc.ErrorIsNil)

	schema.Add(MakePatch("CREATE TEMP TABLE baz (id INTEGER PRIMARY KEY);"))

	_, err = schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.ErrorMatches, `querying current schema version: hash mismatch for version 2`)
}

func (s *schemaSuite) TestHookIsApplied(c *tc.C) {
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("this would fail if the hook is not applied"),
	)
	schema.Hook(func(int, string) (string, error) {
		return "CREATE TABLE bar (id INTEGER PRIMARY KEY);", nil
	})
	c.Log(&schema.hook)

	current, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.IsNil)
	c.Assert(current, tc.DeepEquals, ChangeSet{Current: 0, Post: 1})

	err = s.TxnRunner().StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, "SELECT * FROM bar")
		return row.Err()
	})
	c.Assert(err, tc.ErrorIsNil)
}

func (s *schemaSuite) TestEnsureWaitsForSchemaChangeLock(c *tc.C) {
	if s.SchemaDialect() != dialect.PostgreSQL {
		c.Skip("only PostgreSQL serialises schema changes with a lock")
	}

	// Another transaction holds the lock, as a concurrent schema change would.
	holder, holderPID := s.beginSchemaChange(c)
	defer func() { _ = holder.Rollback() }()

	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TABLE foo (id INTEGER PRIMARY KEY);"),
	)
	done := ensureAsync(c, schema, s.TxnRunner())
	pgtesting.WaitForBlocked(c, s.DB(), holderPID)

	c.Assert(holder.Rollback(), tc.ErrorIsNil)
	res := <-done
	c.Assert(res.err, tc.ErrorIsNil)
	c.Check(res.changes, tc.DeepEquals, ChangeSet{Current: 0, Post: 1})
}

func (s *schemaSuite) TestEnsureSeesSchemaChangeItWaitedFor(c *tc.C) {
	if s.SchemaDialect() != dialect.PostgreSQL {
		c.Skip("only PostgreSQL serialises schema changes with a lock")
	}

	schema := NewForDialect(s.SchemaDialect(),
		MakePatch("CREATE TABLE foo (id INTEGER PRIMARY KEY);"),
	)

	// Another transaction applies the same patch while holding the lock, as
	// an overlapping initialiser of the database would.
	holder, holderPID := s.beginSchemaChange(c)
	defer func() { _ = holder.Rollback() }()
	err := createSchemaTable(c.Context(), holder, s.SchemaDialect())
	c.Assert(err, tc.ErrorIsNil)
	err = ensurePatchesAreApplied(c.Context(), holder, s.SchemaDialect(), 0,
		schema.patches, computeHashes(schema.patches))
	c.Assert(err, tc.ErrorIsNil)

	done := ensureAsync(c, schema, s.TxnRunner())
	pgtesting.WaitForBlocked(c, s.DB(), holderPID)

	// Once the holder commits, Ensure finds the patch already applied
	// rather than applying it again.
	c.Assert(holder.Commit(), tc.ErrorIsNil)
	res := <-done
	c.Assert(res.err, tc.ErrorIsNil)
	c.Check(res.changes, tc.DeepEquals, ChangeSet{Current: 1, Post: 1})
}

func (s *schemaSuite) TestEnsureRunsAtReadCommitted(c *tc.C) {
	if s.SchemaDialect() != dialect.PostgreSQL {
		c.Skip("only PostgreSQL changes the isolation of schema changes")
	}

	// The patch fails unless the transaction applying it runs at READ
	// COMMITTED rather than the connection's default of SERIALIZABLE.
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch(`
DO $$
BEGIN
    IF current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'schema change runs at %',
            current_setting('transaction_isolation');
    END IF;
END
$$;`),
	)
	changes, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(changes, tc.DeepEquals, ChangeSet{Current: 0, Post: 1})
}

func (s *schemaSuite) TestEnsureHoldsSchemaChangeLock(c *tc.C) {
	if s.SchemaDialect() != dialect.PostgreSQL {
		c.Skip("only PostgreSQL serialises schema changes with a lock")
	}

	// The patch fails unless the transaction applying it holds the lock.
	schema := NewForDialect(s.SchemaDialect(),
		MakePatch(`
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_locks
        WHERE locktype = 'advisory' AND pid = pg_backend_pid() AND granted
    ) THEN
        RAISE EXCEPTION 'schema change lock not held';
    END IF;
END
$$;`),
	)
	changes, err := schema.Ensure(c.Context(), s.TxnRunner())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(changes, tc.DeepEquals, ChangeSet{Current: 0, Post: 1})
}

func (s *schemaSuite) TestBeginSchemaChangeSQLite(c *tc.C) {
	// SQLite needs no statements, so nothing runs on the nil transaction.
	err := beginSchemaChange(c.Context(), nil, dialect.SQLite)
	c.Check(err, tc.ErrorIsNil)
}

// beginSchemaChange starts a transaction that has begun a schema change, as
// a concurrent one would, and returns it with its backend PID. The caller
// must end the transaction.
func (s *schemaSuite) beginSchemaChange(c *tc.C) (*sql.Tx, int) {
	tx, err := s.DB().BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	_, err = tx.ExecContext(c.Context(), dialect.PostgreSQL.BeginSchemaChangeSQL())
	c.Assert(err, tc.ErrorIsNil)
	var pid int
	err = tx.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&pid)
	c.Assert(err, tc.ErrorIsNil)
	return tx, pid
}

type ensureResult struct {
	changes ChangeSet
	err     error
}

// ensureAsync runs schema.Ensure in a goroutine and returns a channel that
// receives its result.
func ensureAsync(c *tc.C, schema *Schema, runner database.TxnRunner) <-chan ensureResult {
	done := make(chan ensureResult, 1)
	go func() {
		changes, err := schema.Ensure(c.Context(), runner)
		done <- ensureResult{changes: changes, err: err}
	}()
	return done
}
