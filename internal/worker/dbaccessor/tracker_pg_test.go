// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"
	"database/sql"
	"sync/atomic"
	stdtesting "testing"
	"time"

	"github.com/juju/clock/testclock"
	"github.com/juju/tc"
	"github.com/juju/worker/v5/workertest"

	coredatabase "github.com/juju/juju/core/database"
	coreschema "github.com/juju/juju/core/database/schema"
	"github.com/juju/juju/domain/schema"
	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	"github.com/juju/juju/internal/testhelpers"
	coretesting "github.com/juju/juju/internal/testing"
)

// trackedDBPostgresSuite runs the tracked database against PostgreSQL. It
// is skipped unless pgtesting.URLEnvVar is set.
type trackedDBPostgresSuite struct {
	coretesting.BaseSuite

	admin      *sql.DB
	placement  postgresql.Placement
	namespaces *PostgresNamespaces
}

func TestTrackedDBPostgresSuite(t *stdtesting.T) {
	testhelpers.PrintGoroutineLeaks(t, func(t *stdtesting.T) {
		tc.Run(t, &trackedDBPostgresSuite{})
	})
}

func (s *trackedDBPostgresSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)

	s.admin = pgtesting.OpenAdmin(c)
	s.placement = postgresql.Placement{
		URL:          pgtesting.URL(),
		SchemaPrefix: pgtesting.NewSchemaPrefix(c),
	}
	pgtesting.DropSchemasOnCleanup(c, s.admin, s.placement.SchemaPrefix)
	s.namespaces = NewPostgresNamespaces(s.placement)
}

func (s *trackedDBPostgresSuite) TestFreshNamespace(c *tc.C) {
	w, err := s.newTracker(c, c.Context(), "fresh")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	s.checkInitialised(c, "fresh", true)
	c.Check(s.schemaVersions(c, w), tc.Equals, modelDDL(c).Len())
	c.Check(s.currentSchema(c, w), tc.Equals, s.placement.SchemaName("fresh"))

	workertest.CleanKill(c, w)
}

func (s *trackedDBPostgresSuite) TestExistingNamespace(c *tc.C) {
	w, err := s.newTracker(c, c.Context(), "existing")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	s.exec(c, w,
		"CREATE TABLE sentinel (v TEXT)",
		"INSERT INTO sentinel VALUES ('kept')",
		// Applying the model DDL again would fail on this hash, so the
		// tracker below must rely on the marker alone.
		"UPDATE schema SET hash = 'tampered'",
	)
	err = database.NewDBMigration(w, loggertesting.WrapCheckLog(c), modelDDL(c)).Apply(c.Context())
	c.Assert(err, tc.ErrorMatches, ".*hash mismatch.*")
	workertest.CleanKill(c, w)

	w, err = s.newTracker(c, c.Context(), "existing")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	var v string
	s.queryRow(c, w, "SELECT v FROM sentinel", &v)
	c.Check(v, tc.Equals, "kept")
	c.Check(s.schemaVersions(c, w), tc.Equals, modelDDL(c).Len())

	workertest.CleanKill(c, w)
}

func (s *trackedDBPostgresSuite) TestEmptySchemaWithoutMarker(c *tc.C) {
	err := postgresql.CreateSchema(c.Context(), s.admin, s.placement.SchemaName("empty"))
	c.Assert(err, tc.ErrorIsNil)
	s.checkInitialised(c, "empty", false)

	w, err := s.newTracker(c, c.Context(), "empty")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	s.checkInitialised(c, "empty", true)
	c.Check(s.schemaVersions(c, w), tc.Equals, modelDDL(c).Len())

	workertest.CleanKill(c, w)
}

func (s *trackedDBPostgresSuite) TestDDLFailureLeavesNoMarker(c *tc.C) {
	name := s.placement.SchemaName("conflict")
	quoted := postgresql.QuoteIdentifier(name)
	s.adminExec(c,
		"CREATE SCHEMA "+quoted,
		"CREATE TABLE "+quoted+".change_log (v TEXT)",
	)

	_, err := s.newTracker(c, c.Context(), "conflict")
	c.Assert(err, tc.ErrorMatches, `.*relation "change_log" already exists.*`)

	// The DDL ran in one transaction, so not even the bookkeeping table
	// remains.
	s.checkInitialised(c, "conflict", false)
	c.Check(s.adminRelationExists(c, name, "schema"), tc.IsFalse)

	// Once the conflict is gone, a retry initialises the namespace.
	s.adminExec(c, "DROP TABLE "+quoted+".change_log")
	w, err := s.newTracker(c, c.Context(), "conflict")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	s.checkInitialised(c, "conflict", true)
	c.Check(s.schemaVersions(c, w), tc.Equals, modelDDL(c).Len())

	workertest.CleanKill(c, w)
}

func (s *trackedDBPostgresSuite) TestOverlappingInitialisers(c *tc.C) {
	const n = 4

	type result struct {
		w   TrackedDB
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	for range n {
		go func() {
			<-start
			w, err := s.newTracker(c, c.Context(), "overlap")
			results <- result{w: w, err: err}
		}()
	}
	close(start)

	var failures int
	for range n {
		var r result
		select {
		case r = <-results:
		case <-c.Context().Done():
			c.Fatal("timed out waiting for overlapping initialisers")
		}
		if r.err != nil {
			c.Logf("overlapping initialiser failed: %v", r.err)
			failures++
			continue
		}
		workertest.CleanKill(c, r.w)
	}
	c.Logf("%d of %d overlapping initialisers failed", failures, n)

	// A failed initialiser leaves nothing behind, so each retry succeeds.
	for range failures {
		w, err := s.newTracker(c, c.Context(), "overlap")
		c.Assert(err, tc.ErrorIsNil)
		workertest.CleanKill(c, w)
	}

	s.checkInitialised(c, "overlap", true)
	w, err := s.newTracker(c, c.Context(), "overlap")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)
	c.Check(s.schemaVersions(c, w), tc.Equals, modelDDL(c).Len())
	workertest.CleanKill(c, w)
}

func (s *trackedDBPostgresSuite) TestCancelledInitialisationLeavesNoMarker(c *tc.C) {
	name := s.placement.SchemaName("cancel")
	err := postgresql.CreateSchema(c.Context(), s.admin, name)
	c.Assert(err, tc.ErrorIsNil)

	// An uncommitted bookkeeping table blocks the tracker's DDL until the
	// test cancels it.
	tx, err := s.admin.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(c.Context(),
		"CREATE TABLE "+postgresql.QuoteIdentifier(name)+`."schema" (version BIGINT)`)
	c.Assert(err, tc.ErrorIsNil)
	var blocker int
	err = tx.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&blocker)
	c.Assert(err, tc.ErrorIsNil)

	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		w, err := s.newTracker(c, ctx, "cancel")
		if err == nil {
			w.Kill()
			_ = w.Wait()
		}
		errs <- err
	}()

	waitForBlocked(c, s.admin, blocker)
	cancel()

	select {
	case err = <-errs:
	case <-c.Context().Done():
		c.Fatal("timed out waiting for the cancelled initialiser")
	}
	c.Assert(err, tc.ErrorMatches, ".*context canceled.*")

	err = tx.Rollback()
	c.Assert(err, tc.ErrorIsNil)
	s.checkInitialised(c, "cancel", false)

	w, err := s.newTracker(c, c.Context(), "cancel")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)
	s.checkInitialised(c, "cancel", true)
	workertest.CleanKill(c, w)
}

func (s *trackedDBPostgresSuite) TestNamespaceIsolation(c *tc.C) {
	a, err := s.newTracker(c, c.Context(), "a")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, a)
	b, err := s.newTracker(c, c.Context(), "b")
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, b)

	c.Check(s.currentSchema(c, a), tc.Equals, s.placement.SchemaName("a"))
	c.Check(s.currentSchema(c, b), tc.Equals, s.placement.SchemaName("b"))

	s.exec(c, a, "CREATE TABLE isolation (v TEXT)", "INSERT INTO isolation VALUES ('a')")
	var exists bool
	s.queryRow(c, b, "SELECT to_regclass('isolation') IS NOT NULL", &exists)
	c.Check(exists, tc.IsFalse)

	s.exec(c, b, "CREATE TABLE isolation (v TEXT)", "INSERT INTO isolation VALUES ('b')")
	var v string
	s.queryRow(c, a, "SELECT string_agg(v, ',') FROM isolation", &v)
	c.Check(v, tc.Equals, "a")
	s.queryRow(c, b, "SELECT string_agg(v, ',') FROM isolation", &v)
	c.Check(v, tc.Equals, "b")

	workertest.CleanKill(c, a)
	workertest.CleanKill(c, b)
}

func (s *trackedDBPostgresSuite) TestDroppedSchemaEndsTrackerWithErrDBDead(c *tc.C) {
	clk := testclock.NewDilatedWallClock(time.Millisecond)
	opener := &countingOpener{DBOpener: s.namespaces}

	dropped, err := NewTrackedDBWorker(c.Context(), opener, "dropped",
		s.trackerOptions(c, WithClock(clk))...)
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, dropped)
	survivor, err := s.newTracker(c, c.Context(), "survivor", WithClock(clk))
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, survivor)

	err = postgresql.DropSchema(c.Context(), s.admin, s.placement.SchemaName("dropped"))
	c.Assert(err, tc.ErrorIsNil)

	c.Check(dropped.Wait(), tc.ErrorIs, coredatabase.ErrDBDead)
	err = dropped.StdTxn(c.Context(), func(context.Context, *sql.Tx) error {
		c.Error("transaction ran on a dead database")
		return nil
	})
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBDead)

	// The tracker did not reopen the namespace, so nothing recreated it.
	c.Check(opener.opens.Load(), tc.Equals, int64(1))
	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName("dropped"))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)

	workertest.CheckAlive(c, survivor)
	c.Check(s.currentSchema(c, survivor), tc.Equals, s.placement.SchemaName("survivor"))
	workertest.CleanKill(c, survivor)
}

func (s *trackedDBPostgresSuite) TestControllerNamespaceIsNotCreated(c *tc.C) {
	clk := testclock.NewDilatedWallClock(time.Millisecond)
	w, err := s.newTracker(c, c.Context(), coredatabase.ControllerNS, WithClock(clk))
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	// The tracker never creates the controller schema, so its first ping
	// finds the schema missing. Juju never deletes the controller schema,
	// so its loss is not reported as a dead database.
	err = w.Wait()
	c.Check(err, tc.ErrorMatches, `.*controller schema ".*" no longer exists`)
	c.Check(err, tc.Not(tc.ErrorIs), coredatabase.ErrDBDead)
	exists, err := postgresql.SchemaExists(c.Context(), s.admin,
		s.placement.SchemaName(coredatabase.ControllerNS))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)
}

func (s *trackedDBPostgresSuite) newTracker(
	c *tc.C, ctx context.Context, namespace string, opts ...TrackedDBWorkerOption,
) (TrackedDB, error) {
	return NewTrackedDBWorker(ctx, s.namespaces, namespace, s.trackerOptions(c, opts...)...)
}

func (s *trackedDBPostgresSuite) trackerOptions(c *tc.C, opts ...TrackedDBWorkerOption) []TrackedDBWorkerOption {
	return append([]TrackedDBWorkerOption{
		WithBackend(s.namespaces),
		WithLogger(loggertesting.WrapCheckLog(c)),
		WithMetricsCollector(NewMetricsCollector()),
	}, opts...)
}

func (s *trackedDBPostgresSuite) checkInitialised(c *tc.C, namespace string, expected bool) {
	initialised, err := postgresql.SchemaInitialised(c.Context(), s.admin, s.placement.SchemaName(namespace))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(initialised, tc.Equals, expected, tc.Commentf("namespace %q", namespace))
}

func (s *trackedDBPostgresSuite) schemaVersions(c *tc.C, runner coredatabase.TxnRunner) int {
	var n int
	s.queryRow(c, runner, "SELECT count(*) FROM schema", &n)
	return n
}

func (s *trackedDBPostgresSuite) currentSchema(c *tc.C, runner coredatabase.TxnRunner) string {
	var name string
	s.queryRow(c, runner, "SELECT current_schema()", &name)
	return name
}

func (s *trackedDBPostgresSuite) queryRow(c *tc.C, runner coredatabase.TxnRunner, query string, dest any) {
	err := runner.StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, query).Scan(dest)
	})
	c.Assert(err, tc.ErrorIsNil)
}

func (s *trackedDBPostgresSuite) exec(c *tc.C, runner coredatabase.TxnRunner, stmts ...string) {
	err := runner.StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
	c.Assert(err, tc.ErrorIsNil)
}

func (s *trackedDBPostgresSuite) adminExec(c *tc.C, stmts ...string) {
	for _, stmt := range stmts {
		_, err := s.admin.ExecContext(c.Context(), stmt)
		c.Assert(err, tc.ErrorIsNil)
	}
}

func (s *trackedDBPostgresSuite) adminRelationExists(c *tc.C, schemaName, relation string) bool {
	var exists bool
	err := s.admin.QueryRowContext(c.Context(),
		"SELECT to_regclass($1) IS NOT NULL",
		postgresql.QuoteIdentifier(schemaName)+"."+postgresql.QuoteIdentifier(relation),
	).Scan(&exists)
	c.Assert(err, tc.ErrorIsNil)
	return exists
}

// waitForBlocked waits until another session is waiting on a lock held by
// the blocker backend.
func waitForBlocked(c *tc.C, admin *sql.DB, blocker int) {
	for {
		var waiting bool
		err := admin.QueryRowContext(c.Context(),
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))",
			blocker,
		).Scan(&waiting)
		c.Assert(err, tc.ErrorIsNil)
		if waiting {
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-c.Context().Done():
			c.Fatal("timed out waiting for a session to block")
		}
	}
}

func modelDDL(c *tc.C) *coreschema.Schema {
	ddl, err := schema.ModelDDLForBackend(schema.BackendPostgreSQL)
	c.Assert(err, tc.ErrorIsNil)
	return ddl
}

// countingOpener counts the databases it opens.
type countingOpener struct {
	DBOpener
	opens atomic.Int64
}

func (o *countingOpener) Open(ctx context.Context, namespace string) (*sql.DB, error) {
	o.opens.Add(1)
	return o.DBOpener.Open(ctx, namespace)
}
