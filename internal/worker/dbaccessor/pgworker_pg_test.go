// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
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
	pgtesting.WaitForBlocked(c, s.admin, blocker)

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
	s.waitForNoSessions(c, appName)
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
	modelNS := s.newModelNamespace(c)
	s.registerNamespace(c, "other")

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	model, err := w.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)
	other, err := w.GetDB(c.Context(), "other")
	c.Assert(err, tc.ErrorIsNil)

	err = w.DeleteDB(coredatabase.ControllerNS)
	c.Check(err, tc.ErrorIs, errors.Forbidden)
	c.Check(s.schemaInitialised(c, coredatabase.ControllerNS), tc.IsTrue)

	// As in the undertaker, the model is removed before its database.
	s.unregisterNamespace(c, modelNS)
	err = w.DeleteDB(modelNS)
	c.Assert(err, tc.ErrorIsNil)
	err = model.StdTxn(c.Context(), func(context.Context, *sql.Tx) error {
		c.Error("transaction ran on a deleted database")
		return nil
	})
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBDead)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

	_, err = w.GetDB(c.Context(), modelNS)
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBNotFound)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

	// The controller, its model and the other model are untouched.
	controller, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.currentSchema(c, controller), tc.Equals, s.placement.SchemaName(coredatabase.ControllerNS))
	c.Check(s.currentSchema(c, other), tc.Equals, s.placement.SchemaName("other"))
	c.Check(s.schemaInitialised(c, coredatabase.ControllerNS), tc.IsTrue)
	c.Check(s.schemaInitialised(c, s.modelUUID.String()), tc.IsTrue)
	c.Check(s.schemaInitialised(c, "other"), tc.IsTrue)

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestDeleteDBWithoutTrackedDatabase(c *tc.C) {
	s.bootstrap(c)
	modelNS := s.newModelNamespace(c)

	// An earlier worker created the schema.
	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	_, err := w.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)
	workertest.CleanKill(c, w)
	c.Assert(s.schemaInitialised(c, modelNS), tc.IsTrue)
	s.unregisterNamespace(c, modelNS)

	w = s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	err = w.DeleteDB(modelNS)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

	// Deleting it again, or deleting a namespace that never had a schema,
	// succeeds without creating one.
	err = w.DeleteDB(modelNS)
	c.Check(err, tc.ErrorIsNil)
	err = w.DeleteDB("never")
	c.Check(err, tc.ErrorIsNil)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)
	c.Check(s.schemaExists(c, "never"), tc.IsFalse)

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestDeleteDBForeignObject(c *tc.C) {
	s.bootstrap(c)
	outside := s.placement.SchemaName("outside")
	err := postgresql.CreateSchema(c.Context(), s.admin, outside)
	c.Assert(err, tc.ErrorIsNil)

	tests := []struct {
		about  string
		create string
		remove string
		err    string
	}{{
		about:  "object in the schema",
		create: "CREATE TYPE %[1]s.mood AS ENUM ('ok')",
		remove: "DROP TYPE %[1]s.mood",
		err:    `.*dropping schema: .*: type .*mood depends on schema .*`,
	}, {
		about:  "dependent outside the schema",
		create: "CREATE VIEW %[2]s.changes AS SELECT id FROM %[1]s.change_log",
		remove: "DROP VIEW %[2]s.changes",
		err:    `.*dropping tables: .*: view .*changes depends on table .*change_log`,
	}}

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		namespace := fmt.Sprintf("foreign-%d", i)
		s.registerNamespace(c, namespace)
		_, err := w.GetDB(c.Context(), namespace)
		c.Assert(err, tc.ErrorIsNil)
		s.unregisterNamespace(c, namespace)
		quoted := []any{
			postgresql.QuoteIdentifier(s.placement.SchemaName(namespace)),
			postgresql.QuoteIdentifier(outside),
		}
		s.adminExec(c, fmt.Sprintf(test.create, quoted...))

		// The delete fails, but not as NotFound, which the undertaker would
		// take as success. Nothing is dropped.
		err = w.DeleteDB(namespace)
		c.Check(err, tc.ErrorMatches, test.err)
		c.Check(err, tc.Not(tc.ErrorIs), errors.NotFound)
		c.Check(s.schemaInitialised(c, namespace), tc.IsTrue)

		// Once the foreign object has gone, a retry succeeds.
		s.adminExec(c, fmt.Sprintf(test.remove, quoted...))
		err = w.DeleteDB(namespace)
		c.Check(err, tc.ErrorIsNil)
		c.Check(s.schemaExists(c, namespace), tc.IsFalse)
	}

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestDeleteDBWaitsForLock(c *tc.C) {
	s.bootstrap(c)
	modelNS := s.newModelNamespace(c)

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	_, err := w.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)
	s.unregisterNamespace(c, modelNS)

	blocker, blockerPID := s.lockModelTable(c, modelNS, "change_log")
	done := make(chan error, 1)
	go func() {
		done <- w.DeleteDB(modelNS)
	}()
	pgtesting.WaitForBlocked(c, s.admin, blockerPID)

	// A cached database is still served while the delete waits.
	controller, err := w.GetDB(c.Context(), coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(s.currentSchema(c, controller), tc.Equals, s.placement.SchemaName(coredatabase.ControllerNS))

	c.Assert(blocker.Rollback(), tc.ErrorIsNil)
	c.Check(<-done, tc.ErrorIsNil)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestKillWhileDeleting(c *tc.C) {
	s.bootstrap(c)
	modelNS := s.newModelNamespace(c)

	// The application name identifies the worker's sessions.
	appName := s.placement.SchemaPrefix + "pgworker"
	cfg := s.config(c, "0")
	cfg.Placement = s.withApplicationName(c, appName)

	w := s.newWorker(c, cfg)
	defer workertest.DirtyKill(c, w)
	_, err := w.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)
	s.unregisterNamespace(c, modelNS)
	before := s.relationCount(c, modelNS)

	blocker, blockerPID := s.lockModelTable(c, modelNS, "change_log")
	done := make(chan error, 1)
	go func() {
		done <- w.DeleteDB(modelNS)
	}()
	pgtesting.WaitForBlocked(c, s.admin, blockerPID)

	// Killing the worker cancels the delete. Whether the caller sees the
	// worker dying or the cancelled delete is a race; either is an error.
	workertest.CleanKill(c, w)
	err = <-done
	if !errors.Is(err, coredatabase.ErrDBAccessorDying) {
		c.Check(err, tc.ErrorIs, context.Canceled)
	}

	// The cancelled session's backend is still waiting for the lock. Once
	// it is released, the backend finds its client gone and rolls back.
	c.Assert(blocker.Rollback(), tc.ErrorIsNil)
	s.waitForNoSessions(c, appName)
	c.Check(s.schemaInitialised(c, modelNS), tc.IsTrue)
	c.Check(s.relationCount(c, modelNS), tc.Equals, before)
}

func (s *pgWorkerSuite) TestDeleteDBFromAnotherController(c *tc.C) {
	s.bootstrap(c)
	modelNS := s.newModelNamespace(c)

	w0 := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w0)
	// The other controller pings its databases quickly.
	cfg1 := s.config(c, "1")
	cfg1.Clock = testclock.NewDilatedWallClock(time.Millisecond)
	w1 := s.newWorker(c, cfg1)
	defer workertest.DirtyKill(c, w1)

	_, err := w0.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)
	model1, err := w1.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)

	s.unregisterNamespace(c, modelNS)
	err = w0.DeleteDB(modelNS)
	c.Assert(err, tc.ErrorIsNil)

	// The other controller's tracked database notices that its schema has
	// gone, and is not replaced.
	for {
		err := model1.StdTxn(c.Context(), func(context.Context, *sql.Tx) error { return nil })
		if errors.Is(err, coredatabase.ErrDBDead) {
			break
		}
		c.Assert(err, tc.ErrorIsNil)
		select {
		case <-time.After(10 * time.Millisecond):
		case <-c.Context().Done():
			c.Fatal("timed out waiting for the other controller's database to die")
		}
	}
	_, err = w1.GetDB(c.Context(), modelNS)
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBNotFound)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

	workertest.CleanKill(c, w1)
	workertest.CleanKill(c, w0)
}

func (s *pgWorkerSuite) TestDeleteDBRacesUse(c *tc.C) {
	s.bootstrap(c)
	modelNS := s.newModelNamespace(c)

	w := s.newWorker(c, s.config(c, "0"))
	defer workertest.DirtyKill(c, w)
	_, err := w.GetDB(c.Context(), modelNS)
	c.Assert(err, tc.ErrorIsNil)
	s.unregisterNamespace(c, modelNS)

	// Users keep getting and using the database until it is not found.
	// Their transactions may fail while it is deleted.
	const users = 4
	var wg sync.WaitGroup
	for range users {
		wg.Go(func() {
			for {
				runner, err := w.GetDB(c.Context(), modelNS)
				if errors.Is(err, coredatabase.ErrDBNotFound) {
					return
				} else if err != nil {
					c.Errorf("getting database: %v", err)
					return
				}
				_ = runner.StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "SELECT count(*) FROM change_log")
					return err
				})
			}
		})
	}

	err = w.DeleteDB(modelNS)
	c.Check(err, tc.ErrorIsNil)
	wg.Wait()
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)
	_, err = w.GetDB(c.Context(), modelNS)
	c.Check(err, tc.ErrorIs, coredatabase.ErrDBNotFound)

	workertest.CleanKill(c, w)
}

func (s *pgWorkerSuite) TestDeleteDBWhileStarting(c *tc.C) {
	s.bootstrap(c)
	modelNS := s.newModelNamespace(c)

	// The model's tracked database applies the model DDL, but is held
	// before the runner sees it as started.
	starting := make(chan struct{}, 1)
	release := make(chan struct{})
	cfg := s.config(c, "0")
	cfg.NewDBWorker = func(
		ctx context.Context, opener DBOpener, namespace string, opts ...TrackedDBWorkerOption,
	) (TrackedDB, error) {
		db, err := NewTrackedDBWorker(ctx, opener, namespace, opts...)
		if namespace == modelNS {
			select {
			case starting <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return db, err
	}
	w := s.newWorker(c, cfg)
	defer workertest.DirtyKill(c, w)

	type result struct {
		runner coredatabase.TxnRunner
		err    error
	}
	got := make(chan result, 1)
	go func() {
		runner, err := w.GetDB(c.Context(), modelNS)
		got <- result{runner: runner, err: err}
	}()
	<-starting
	c.Assert(s.schemaInitialised(c, modelNS), tc.IsTrue)

	s.unregisterNamespace(c, modelNS)
	deleted := make(chan error, 1)
	go func() {
		deleted <- w.DeleteDB(modelNS)
	}()

	// The delete waits for the start to finish, so it cannot drop the
	// schema under a database that is about to be handed out.
	select {
	case err := <-deleted:
		c.Fatalf("delete finished while the database was starting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	c.Check(s.schemaInitialised(c, modelNS), tc.IsTrue)

	close(release)
	c.Check(<-deleted, tc.ErrorIsNil)
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

	// The database that GetDB returned, if any, has been stopped.
	res := <-got
	if res.err == nil {
		err := res.runner.StdTxn(c.Context(), func(context.Context, *sql.Tx) error { return nil })
		c.Check(err, tc.ErrorIs, coredatabase.ErrDBDead)
	}
	c.Check(s.schemaExists(c, modelNS), tc.IsFalse)

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

// newModelNamespace registers the namespace of a new hosted model, whose
// schema does not exist until a worker opens it.
func (s *pgWorkerSuite) newModelNamespace(c *tc.C) string {
	namespace := tc.Must0(c, coremodel.NewUUID).String()
	s.registerNamespace(c, namespace)
	c.Assert(s.schemaExists(c, namespace), tc.IsFalse)
	return namespace
}

func (s *pgWorkerSuite) unregisterNamespace(c *tc.C, namespace string) {
	_, err := s.admin.ExecContext(c.Context(),
		"DELETE FROM "+s.controllerTable("namespace_list")+" WHERE namespace = $1", namespace)
	c.Assert(err, tc.ErrorIsNil)
}

func (s *pgWorkerSuite) adminExec(c *tc.C, stmt string) {
	c.Helper()
	_, err := s.admin.ExecContext(c.Context(), stmt)
	c.Assert(err, tc.ErrorIsNil)
}

func (s *pgWorkerSuite) schemaExists(c *tc.C, namespace string) bool {
	exists, err := postgresql.SchemaExists(c.Context(), s.admin, s.placement.SchemaName(namespace))
	c.Assert(err, tc.ErrorIsNil)
	return exists
}

func (s *pgWorkerSuite) schemaInitialised(c *tc.C, namespace string) bool {
	initialised, err := postgresql.SchemaInitialised(c.Context(), s.admin, s.placement.SchemaName(namespace))
	c.Assert(err, tc.ErrorIsNil)
	return initialised
}

// relationCount counts the tables, views, indexes and sequences in the
// namespace's schema.
func (s *pgWorkerSuite) relationCount(c *tc.C, namespace string) int {
	var n int
	err := s.admin.QueryRowContext(c.Context(), `
SELECT count(*)
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = $1`, s.placement.SchemaName(namespace)).Scan(&n)
	c.Assert(err, tc.ErrorIsNil)
	return n
}

// lockModelTable starts a transaction holding a lock on a table in the
// namespace's schema, which is rolled back when the test finishes, and
// returns it with its backend PID.
func (s *pgWorkerSuite) lockModelTable(c *tc.C, namespace, table string) (*sql.Tx, int) {
	tx, err := s.admin.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = tx.Rollback() })

	var pid int
	err = tx.QueryRowContext(c.Context(), "SELECT pg_backend_pid()").Scan(&pid)
	c.Assert(err, tc.ErrorIsNil)
	_, err = tx.ExecContext(c.Context(), "LOCK TABLE "+
		postgresql.QuoteIdentifier(s.placement.SchemaName(namespace))+"."+postgresql.QuoteIdentifier(table)+
		" IN ACCESS SHARE MODE")
	c.Assert(err, tc.ErrorIsNil)
	return tx, pid
}

func (s *pgWorkerSuite) waitForNoSessions(c *tc.C, appName string) {
	for s.sessions(c, appName) > 0 {
		select {
		case <-time.After(10 * time.Millisecond):
		case <-c.Context().Done():
			c.Fatalf("timed out waiting for the sessions of %q to end", appName)
		}
	}
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
