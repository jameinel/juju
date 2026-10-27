// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"
	"database/sql"
	"net/url"
	stdtesting "testing"
	"time"

	"github.com/juju/clock"
	"github.com/juju/clock/testclock"
	"github.com/juju/errors"
	"github.com/juju/tc"
	"github.com/juju/worker/v5/workertest"

	coredatabase "github.com/juju/juju/core/database"
	coremodel "github.com/juju/juju/core/model"
	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	"github.com/juju/juju/internal/testhelpers"
	coretesting "github.com/juju/juju/internal/testing"
)

// pgWorkerSuite runs the PostgreSQL dbaccessor worker against PostgreSQL.
// It is skipped unless pgtesting.URLEnvVar is set.
type pgWorkerSuite struct {
	coretesting.BaseSuite

	admin     *sql.DB
	placement postgresql.Placement
	modelUUID coremodel.UUID
}

func TestPgWorkerSuite(t *stdtesting.T) {
	testhelpers.PrintGoroutineLeaks(t, func(t *stdtesting.T) {
		tc.Run(t, &pgWorkerSuite{})
	})
}

func (s *pgWorkerSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)

	s.admin = pgtesting.OpenAdmin(c)
	s.placement = postgresql.Placement{
		URL:          pgtesting.URL(),
		SchemaPrefix: pgtesting.NewSchemaPrefix(c),
	}
	pgtesting.DropSchemasOnCleanup(c, s.admin, s.placement.SchemaPrefix)
	s.modelUUID = tc.Must0(c, coremodel.NewUUID)
}

func (s *pgWorkerSuite) TestRegistersControllerBeforeServing(c *tc.C) {
	s.bootstrap(c)

	w := s.newWorker(c, s.config(c, "1"))
	defer workertest.DirtyKill(c, w)

	controller, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.currentSchema(c, controller), tc.Equals, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Check(s.controllerNodes(c), tc.DeepEquals, []string{"0", "1"})

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestControllerNodeNotAlive(c *tc.C) {
	s.bootstrap(c)
	// The update triggers write to unqualified change_log, so it must run
	// with the controller schema on the search_path.
	controllerDB, err := s.placement.OpenNamespace(coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = controllerDB.Close() }()
	_, err = controllerDB.ExecContext(c.Context(),
		"UPDATE controller_node SET life_id = 1 WHERE controller_id = '0'")
	c.Assert(err, tc.ErrorIsNil)

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)

	c.Check(w.Wait(), tc.ErrorMatches, `.*controller node is not alive.*`)
	_, err = w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBAccessorDying)
}

func (s *pgWorkerSuite) TestMissingControllerSchema(c *tc.C) {
	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)

	c.Check(w.Wait(), tc.ErrorMatches, `.*controller schema ".*" (is not initialised|no longer exists)`)
	_, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBAccessorDying)

	// Only bootstrap creates the controller schema.
	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)
}

func (s *pgWorkerSuite) TestControllerSchemaWithoutMarker(c *tc.C) {
	err := postgresql.CreateSchema(c.Context(), s.admin, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Assert(err, tc.ErrorIsNil)

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)

	c.Check(w.Wait(), tc.ErrorMatches, `.*controller schema ".*" is not initialised`)
	c.Check(s.adminRelationExists(c, s.placement.SchemaName(coredatabase.ControllerNS), "controller_node"), tc.IsFalse)
}

func (s *pgWorkerSuite) TestGetDBModelNamespaces(c *tc.C) {
	s.bootstrap(c)
	s.registerNamespace(c, s.modelUUID.String())
	s.registerNamespace(c, "fresh")

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)

	model, err := w.GetDB(c.Context(), s.modelUUID.String())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.currentSchema(c, model), tc.Equals, s.placement.SchemaName(s.modelUUID.String()))

	// The runner caches the tracked database.
	again, err := w.GetDB(c.Context(), s.modelUUID.String())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(again, tc.Equals, model)

	// A registered namespace without a schema gets an initialised one.
	fresh, err := w.GetDB(c.Context(), "fresh")
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.currentSchema(c, fresh), tc.Equals, s.placement.SchemaName("fresh"))
	initialised, err := postgresql.SchemaInitialised(c.Context(), s.admin, s.placement.SchemaName("fresh"))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(initialised, tc.IsTrue)

	// An unregistered namespace gets no schema.
	_, err = w.GetDB(c.Context(), "unknown")
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBNotFound)
	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName("unknown"))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestRestart(c *tc.C) {
	s.bootstrap(c)

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	_, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	workertest.CleanKill(c, w)

	w = s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	controller, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.currentSchema(c, controller), tc.Equals, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Check(s.controllerNodes(c), tc.DeepEquals, []string{"0"})

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestKillWhileRegistering(c *tc.C) {
	s.bootstrap(c)

	// An exclusive lock on controller_node blocks registration.
	tx, err := s.admin.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(c.Context(),
		"LOCK TABLE "+s.controllerTable("controller_node")+" IN ACCESS EXCLUSIVE MODE")
	c.Assert(err, tc.ErrorIsNil)
	var blocker int
	err = tx.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&blocker)
	c.Assert(err, tc.ErrorIsNil)

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	waitForBlocked(c, s.admin, blocker)

	ctx, cancel := context.WithCancel(c.Context())
	cancel()
	_, err = w.GetDB(ctx, coredatabase.ControllerNS)
	c.Check(err, tc.ErrorIs, context.Canceled)

	workertest.CleanKill(c, w)
	_, err = w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBAccessorDying)

	err = tx.Rollback()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.controllerNodes(c), tc.DeepEquals, []string{"0"})
}

func (s *pgWorkerSuite) TestKillClosesPools(c *tc.C) {
	s.bootstrap(c)
	s.registerNamespace(c, s.modelUUID.String())

	// The application name identifies the worker's sessions.
	appName := s.placement.SchemaPrefix + "pgworker"
	cfg := s.config(c, "0")
	cfg.Placement = s.withApplicationName(c, appName)

	w := s.newWorker(c, cfg)
	defer workertest.DirtyKill(c, w)
	for _, namespace := range []string{coredatabase.ControllerNS, s.modelUUID.String()} {
		runner, err := w.GetDB(c.Context(), namespace)
		c.Assert(err, tc.ErrorIsNil)
		s.currentSchema(c, runner)
	}
	c.Assert(s.sessions(c, appName), tc.GreaterThan, 0)

	workertest.CleanKill(c, w)
	for s.sessions(c, appName) > 0 {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-c.Context().Done():
			c.Fatal("timed out waiting for the worker's sessions to end")
		}
	}
}

func (s *pgWorkerSuite) TestDroppedControllerSchemaIsFatal(c *tc.C) {
	s.bootstrap(c)

	cfg := s.config(c, "0")
	cfg.Clock = testclock.NewDilatedWallClock(time.Millisecond)
	w := s.newWorker(c, cfg)
	defer workertest.DirtyKill(c, w)
	_, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)

	err = postgresql.DropSchema(c.Context(), s.admin, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Assert(err, tc.ErrorIsNil)

	err = w.Wait()
	c.Check(err, tc.ErrorMatches, `.*controller schema ".*" no longer exists`)
	c.Check(err, tc.Not(tc.ErrorIs), coredatabase.ErrDBDead)
}

func (s *pgWorkerSuite) TestDeleteDB(c *tc.C) {
	s.bootstrap(c)
	s.registerNamespace(c, s.modelUUID.String())

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	model, err := w.GetDB(c.Context(), s.modelUUID.String())
	c.Assert(err, tc.ErrorIsNil)

	err = w.DeleteDB(coredatabase.ControllerNS)
	c.Check(err, tc.ErrorIs, errors.Forbidden)

	// Deletion stops the tracked database, then reports that dropping the
	// schema is not implemented.
	err = w.DeleteDB(s.modelUUID.String())
	c.Check(err, tc.ErrorIs, errors.NotImplemented)
	err = model.StdTxn(c.Context(), func(context.Context, *sql.Tx) error {
		c.Error("transaction ran on a deleted database")
		return nil
	})
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBDead)
	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName(s.modelUUID.String()))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsTrue)

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) bootstrap(c *tc.C) {
	err := database.BootstrapPostgres(c.Context(), s.placement, s.modelUUID, loggertesting.WrapCheckLog(c))
	c.Assert(err, tc.ErrorIsNil)
}

func (s *pgWorkerSuite) config(c *tc.C, controllerID string) PostgresWorkerConfig {
	return PostgresWorkerConfig{
		Placement:        s.placement,
		Clock:            clock.WallClock,
		MetricsCollector: NewMetricsCollector(),
		Logger:           loggertesting.WrapCheckLog(c),
		NewDBWorker:      NewTrackedDBWorker,
		ControllerID:     controllerID,
	}
}

func (s *pgWorkerSuite) newWorker(c *tc.C, cfg PostgresWorkerConfig) *pgWorker {
	w, err := NewPostgresWorker(cfg)
	c.Assert(err, tc.ErrorIsNil)
	return w
}

// withApplicationName returns the placement with an application_name
// parameter added to its URL.
func (s *pgWorkerSuite) withApplicationName(c *tc.C, name string) postgresql.Placement {
	// Errors from url.Parse quote the URL, which may hold a password.
	u, err := url.Parse(s.placement.URL)
	if err != nil {
		c.Fatal("cannot parse the test PostgreSQL URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		c.Skip("the test PostgreSQL URL is not a postgres:// URL")
	}
	query := u.Query()
	query.Set("application_name", name)
	u.RawQuery = query.Encode()

	placement := s.placement
	placement.URL = u.String()
	return placement
}

func (s *pgWorkerSuite) controllerTable(name string) string {
	return postgresql.QuoteIdentifier(s.placement.SchemaName(coredatabase.ControllerNS)) + "." +
		postgresql.QuoteIdentifier(name)
}

func (s *pgWorkerSuite) registerNamespace(c *tc.C, namespace string) {
	_, err := s.admin.ExecContext(c.Context(),
		"INSERT INTO "+s.controllerTable("namespace_list")+" (namespace) VALUES ($1)", namespace)
	c.Assert(err, tc.ErrorIsNil)
}

func (s *pgWorkerSuite) controllerNodes(c *tc.C) []string {
	rows, err := s.admin.QueryContext(c.Context(),
		"SELECT controller_id FROM "+s.controllerTable("controller_node")+" ORDER BY controller_id")
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		c.Assert(rows.Scan(&id), tc.ErrorIsNil)
		ids = append(ids, id)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	return ids
}

func (s *pgWorkerSuite) sessions(c *tc.C, appName string) int {
	var n int
	err := s.admin.QueryRowContext(c.Context(),
		"SELECT count(*) FROM pg_stat_activity WHERE application_name = $1", appName,
	).Scan(&n)
	c.Assert(err, tc.ErrorIsNil)
	return n
}

func (s *pgWorkerSuite) currentSchema(c *tc.C, runner coredatabase.TxnRunner) string {
	var name string
	err := runner.StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT current_schema()").Scan(&name)
	})
	c.Assert(err, tc.ErrorIsNil)
	return name
}

func (s *pgWorkerSuite) adminRelationExists(c *tc.C, schemaName, relation string) bool {
	var exists bool
	err := s.admin.QueryRowContext(c.Context(),
		"SELECT to_regclass($1) IS NOT NULL",
		postgresql.QuoteIdentifier(schemaName)+"."+postgresql.QuoteIdentifier(relation),
	).Scan(&exists)
	c.Assert(err, tc.ErrorIsNil)
	return exists
}
