// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"

	"github.com/juju/clock"
	"github.com/juju/errors"
	"github.com/juju/worker/v5"
	"github.com/juju/worker/v5/catacomb"

	"github.com/juju/juju/core/database"
	"github.com/juju/juju/core/logger"
	"github.com/juju/juju/domain/controllernode/service"
	"github.com/juju/juju/domain/controllernode/state"
	"github.com/juju/juju/internal/database/postgresql"
)

// PostgresWorkerConfig configures the dbaccessor worker of a controller whose
// databases are schemas in an external PostgreSQL database.
type PostgresWorkerConfig struct {
	// Placement locates the schema of each namespace.
	Placement postgresql.Placement

	Clock            clock.Clock
	MetricsCollector *Collector
	Logger           logger.Logger
	NewDBWorker      NewDBWorkerFunc

	// ControllerID uniquely identifies the controller that this
	// worker is running on. It is equivalent to the machine ID.
	ControllerID string
}

// Validate ensures that the config values are valid.
func (c PostgresWorkerConfig) Validate() error {
	if c.Placement.URL == "" {
		return errors.NotValidf("missing Placement URL")
	}
	if c.Clock == nil {
		return errors.NotValidf("missing Clock")
	}
	if c.MetricsCollector == nil {
		return errors.NotValidf("missing metrics collector")
	}
	if c.Logger == nil {
		return errors.NotValidf("missing Logger")
	}
	if c.NewDBWorker == nil {
		return errors.NotValidf("missing NewDBWorker")
	}
	if c.ControllerID == "" {
		return errors.NotValidf("missing ControllerID")
	}
	return nil
}

// pgWorker serves tracked databases from the schemas of an external
// PostgreSQL database. Unlike the dqlite worker it runs no database node, so
// it has no cluster membership, address binding or handover.
type pgWorker struct {
	cfg        PostgresWorkerConfig
	namespaces *PostgresNamespaces
	catacomb   catacomb.Catacomb
	dbRunner   *worker.Runner

	// dbReady is closed once this controller is registered and requests can
	// be served.
	dbReady chan struct{}

	// dbRequests serialises GetDB and DeleteDB requests through the worker
	// loop.
	dbRequests chan dbRequest
}

// NewPostgresWorker returns a dbaccessor worker for PostgreSQL.
func NewPostgresWorker(cfg PostgresWorkerConfig) (*pgWorker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Trace(err)
	}

	runner, err := newTrackedDBRunner(cfg.Clock, cfg.Logger)
	if err != nil {
		return nil, errors.Trace(err)
	}

	w := &pgWorker{
		cfg:        cfg,
		namespaces: NewPostgresNamespaces(cfg.Placement),
		dbRunner:   runner,
		dbReady:    make(chan struct{}),
		dbRequests: make(chan dbRequest),
	}

	if err := catacomb.Invoke(catacomb.Plan{
		Name: "db-accessor",
		Site: &w.catacomb,
		Work: w.loop,
		Init: []worker.Worker{
			w.dbRunner,
		},
	}); err != nil {
		return nil, errors.Trace(err)
	}
	return w, nil
}

func (w *pgWorker) loop() error {
	ctx, cancel := context.WithCancel(w.catacomb.Context(context.Background()))
	defer cancel()

	if err := w.start(ctx); err != nil {
		select {
		case <-w.catacomb.Dying():
			return w.catacomb.ErrDying()
		default:
		}
		// The runner can die before the catacomb notices. Its error says
		// why, so prefer it to the ErrDead seen by start.
		if errors.Is(err, worker.ErrDead) {
			if runnerErr := w.dbRunner.Wait(); runnerErr != nil {
				return errors.Trace(runnerErr)
			}
		}
		return errors.Trace(err)
	}

	for {
		select {
		case req := <-w.dbRequests:
			// The done channel is buffered, so this never blocks.
			req.done <- w.handleRequest(ctx, req)
		case <-w.catacomb.Dying():
			return w.catacomb.ErrDying()
		}
	}
}

// start opens the controller database, checks that bootstrap initialised
// it, and registers this controller before any request is served.
func (w *pgWorker) start(ctx context.Context) error {
	if err := w.openDatabase(ctx, database.ControllerNS); err != nil {
		return errors.Annotate(err, "opening controller database")
	}
	controllerDB, err := w.workerFromCache(ctx, database.ControllerNS)
	if err != nil {
		return errors.Annotate(err, "getting controller database")
	} else if controllerDB == nil {
		return errors.New("controller database is not running")
	}
	if err := w.namespaces.CheckControllerInitialised(ctx, controllerDB); err != nil {
		return errors.Trace(err)
	}

	// A PostgreSQL controller is not a dqlite node, so it registers only its
	// controller ID.
	if err := w.nodeService().AddDqliteNodeID(ctx, w.cfg.ControllerID); err != nil {
		return errors.Annotatef(err, "registering controller %q", w.cfg.ControllerID)
	}

	close(w.dbReady)
	return nil
}

func (w *pgWorker) handleRequest(ctx context.Context, req dbRequest) error {
	switch req.op {
	case getOp:
		if err := w.ensureNamespace(ctx, req.namespace); err != nil {
			return errors.Annotatef(err, "ensuring namespace %q", req.namespace)
		}
		if err := w.openDatabase(ctx, req.namespace); err != nil {
			return errors.Annotatef(err, "opening database for namespace %q", req.namespace)
		}
		return nil
	case delOp:
		w.cfg.Logger.Infof(ctx, "deleting database for namespace %q", req.namespace)
		if err := w.deleteDatabase(ctx, req.namespace); err != nil {
			return errors.Annotatef(err, "deleting database for namespace %q", req.namespace)
		}
		return nil
	default:
		return errors.Errorf("unknown op %v", req.op)
	}
}

// Kill is part of the worker.Worker interface.
func (w *pgWorker) Kill() {
	w.catacomb.Kill(nil)
}

// Wait is part of the worker.Worker interface.
func (w *pgWorker) Wait() error {
	return w.catacomb.Wait()
}

// Report provides information for the engine report.
func (w *pgWorker) Report(ctx context.Context) map[string]any {
	return w.dbRunner.Report(w.catacomb.Context(ctx))
}

// GetDB returns a transaction runner for the database that contains the data
// for the specified namespace.
func (w *pgWorker) GetDB(ctx context.Context, namespace string) (database.TxnRunner, error) {
	select {
	case <-w.dbReady:
	case <-w.catacomb.Dying():
		return nil, database.ErrDBAccessorDying
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if db, err := w.workerFromCache(ctx, namespace); err != nil {
		if errors.Is(err, w.catacomb.ErrDying()) {
			return nil, database.ErrDBAccessorDying
		}
		return nil, errors.Trace(err)
	} else if db != nil {
		return db, nil
	}

	req := makeDBGetRequest(namespace)
	select {
	case w.dbRequests <- req:
	case <-w.catacomb.Dying():
		return nil, database.ErrDBAccessorDying
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case err := <-req.done:
		if err != nil {
			return nil, errors.Trace(err)
		}
	case <-w.catacomb.Dying():
		return nil, database.ErrDBAccessorDying
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tracked, err := w.dbRunner.Worker(namespace, w.catacomb.Dying())
	if err != nil {
		return nil, errors.Trace(err)
	}
	return tracked.(database.TxnRunner), nil
}

// DeleteDB stops the tracked database for the namespace, then drops the
// namespace's schema without CASCADE (D47). An object that Juju did not
// create, or a lock held for longer than the delete's lock timeout, fails the
// delete and nothing is dropped; the caller retries it. Deleting a namespace
// whose schema does not exist succeeds. It refuses the controller namespace.
func (w *pgWorker) DeleteDB(namespace string) error {
	select {
	case <-w.dbReady:
	case <-w.catacomb.Dying():
		return database.ErrDBAccessorDying
	}

	req := makeDBDelRequest(namespace)
	select {
	case w.dbRequests <- req:
	case <-w.catacomb.Dying():
		return database.ErrDBAccessorDying
	}

	select {
	case err := <-req.done:
		return errors.Trace(err)
	case <-w.catacomb.Dying():
		return database.ErrDBAccessorDying
	}
}

// openDatabase starts a tracked database for the namespace, unless one is
// already running. The runner starts it asynchronously.
func (w *pgWorker) openDatabase(ctx context.Context, namespace string) error {
	err := w.dbRunner.StartWorker(ctx, namespace, func(ctx context.Context) (worker.Worker, error) {
		return w.cfg.NewDBWorker(ctx,
			w.namespaces, namespace,
			WithBackend(w.namespaces),
			WithClock(w.cfg.Clock),
			WithLogger(w.cfg.Logger.Child(database.ShortNamespace(namespace))),
			WithMetricsCollector(w.cfg.MetricsCollector),
		)
	})
	if errors.Is(err, errors.AlreadyExists) {
		return nil
	}
	return errors.Trace(err)
}

func (w *pgWorker) deleteDatabase(ctx context.Context, namespace string) error {
	if namespace == database.ControllerNS {
		return errors.Forbiddenf("cannot delete controller database")
	}

	// Stop the tracked database first, so nothing hands the namespace out
	// while it is deleted.
	tracked, err := w.workerFromCache(ctx, namespace)
	if err != nil {
		return errors.Trace(err)
	}
	if tracked != nil {
		killable, ok := tracked.(killableWorker)
		if !ok {
			return errors.Errorf("worker for namespace %q is not killable", namespace)
		}
		killable.KillWithReason(database.ErrDBDead)
		if err := killable.Wait(); err != nil && !errors.Is(err, database.ErrDBDead) {
			return errors.Annotatef(err, "waiting for worker to die")
		}
	}

	// This runs on the worker loop, so other requests wait behind it. Each
	// lock wait is bounded, and a delete that fails is retried by the
	// caller.
	return errors.Trace(w.namespaces.Delete(ctx, namespace))
}

func (w *pgWorker) workerFromCache(ctx context.Context, namespace string) (database.TxnRunner, error) {
	merged, cancel := firstClosed(ctx.Done(), w.catacomb.Dying())
	defer cancel()

	if tracked, err := w.dbRunner.Worker(namespace, merged); err == nil {
		return tracked.(database.TxnRunner), nil
	} else if errors.Is(errors.Cause(err), worker.ErrDead) {
		select {
		case <-w.catacomb.Dying():
			return nil, w.catacomb.ErrDying()
		default:
			return nil, errors.Trace(err)
		}
	} else if !errors.Is(errors.Cause(err), errors.NotFound) {
		return nil, errors.Trace(err)
	}
	return nil, nil
}

// ensureNamespace returns an error satisfying errors.Is(err,
// database.ErrDBNotFound) unless the namespace is the controller's or is
// registered in the controller's namespace list.
func (w *pgWorker) ensureNamespace(ctx context.Context, namespace string) error {
	if namespace == database.ControllerNS {
		return nil
	}
	known, err := w.nodeService().IsKnownDatabaseNamespace(ctx, namespace)
	if err != nil {
		return errors.Trace(err)
	} else if !known {
		return database.ErrDBNotFound
	}
	return nil
}

// nodeService returns a controller node service backed by the controller's
// tracked database. Call it only once start has opened that database.
func (w *pgWorker) nodeService() *service.Service {
	return service.NewService(
		state.NewState(
			database.NewTxnRunnerFactoryForNamespace(w.workerFromCache, database.ControllerNS)),
		w.cfg.Logger.Child("controllernode"),
	)
}

// noClusterDescriber reports no cluster nodes, because a PostgreSQL
// controller is not a member of a dqlite cluster.
type noClusterDescriber struct{}

// ClusterDetails returns no nodes.
func (noClusterDescriber) ClusterDetails(context.Context) ([]database.ClusterNodeInfo, error) {
	return nil, nil
}
