// Copyright 2022 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	"context"
	"testing"
	"time"

	"github.com/canonical/gomock/gomock"
	"github.com/juju/errors"
	"github.com/juju/tc"
	"github.com/juju/worker/v5/dependency"
	dt "github.com/juju/worker/v5/dependency/testing"
	"github.com/juju/worker/v5/workertest"

	"github.com/juju/juju/controller"
	coredatabase "github.com/juju/juju/core/database"
	"github.com/juju/juju/core/logger"
	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/app"
	"github.com/juju/juju/internal/database/connsource"
	"github.com/juju/juju/internal/testhelpers"
)

type stubStartupValuesProvider struct{}

func (stubStartupValuesProvider) ControllerStartupValues() (ControllerStartupValues, error) {
	return ControllerStartupValues{
		ControllerID:          "0",
		DataDir:               "/var/lib/juju",
		DqlitePort:            17666,
		QueryTracingEnabled:   true,
		QueryTracingThreshold: time.Second,
		DqliteBusyTimeout:     time.Second,
		CACert:                "ca-cert",
		ControllerCert:        "controller-cert",
		ControllerPrivateKey:  "controller-key",
	}, nil
}

type manifoldSuite struct {
	baseSuite
}

type stubConnectionSource struct {
	details connsource.Details
	err     error
}

func (s stubConnectionSource) Details(context.Context) (connsource.Details, error) {
	return s.details, s.err
}

func TestManifoldSuite(t *testing.T) {
	testhelpers.PrintGoroutineLeaks(t, func(t *testing.T) {
		tc.Run(t, &manifoldSuite{})
	})
}

func (s *manifoldSuite) TestValidateConfig(c *tc.C) {
	defer s.setupMocks(c).Finish()

	cfg := s.getConfig()
	c.Check(cfg.Validate(), tc.ErrorIsNil)

	cfg = s.getConfig()
	cfg.QueryLoggerName = ""
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.ControllerAgentConfigName = ""
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.ControllerStartupValues = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.DBConnectionSource = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.Logger = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.PrometheusRegisterer = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.NewApp = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.NewDBWorker = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.NewMetricsCollector = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)

	cfg = s.getConfig()
	cfg.NewNodeManager = nil
	c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid)
}

func (s *manifoldSuite) TestStartDqliteNeedsDqliteInputs(c *tc.C) {
	defer s.setupMocks(c).Finish()

	// The dqlite worker reads the controller agent config before it
	// registers its metrics, so a missing input stops it first.
	_, err := Manifold(s.getConfig()).Start(c.Context(), dt.StubGetter(map[string]any{
		"controller-agent-config": dependency.ErrMissing,
	}))
	c.Check(err, tc.ErrorIs, dependency.ErrMissing)
}

func (s *manifoldSuite) TestStartPostgreSQL(c *tc.C) {
	defer s.setupMocks(c).Finish()

	s.prometheusRegisterer.EXPECT().Register(gomock.Any()).Return(nil)
	s.prometheusRegisterer.EXPECT().Unregister(gomock.Any()).Return(true)

	cfg := s.getConfig()
	cfg.DBConnectionSource = stubConnectionSource{details: connsource.Details{
		Backend: controller.DBBackendPostgreSQL,
		URL:     "postgres://127.0.0.1:1/none",
	}}
	cfg.NewDBWorker = func(context.Context, DBOpener, string, ...TrackedDBWorkerOption) (TrackedDB, error) {
		return nil, errors.New("boom")
	}

	// The PostgreSQL worker needs neither the query logger nor the
	// controller agent config.
	w, err := Manifold(cfg).Start(c.Context(), dt.StubGetter(nil))
	c.Assert(err, tc.ErrorIsNil)
	defer workertest.DirtyKill(c, w)

	var getter coredatabase.DBGetter
	c.Assert(dbAccessorOutput(w, &getter), tc.ErrorIsNil)
	c.Check(getter, tc.FitsTypeOf, &pgWorker{})

	var deleter coredatabase.DBDeleter
	c.Assert(dbAccessorOutput(w, &deleter), tc.ErrorIsNil)
	c.Check(deleter, tc.FitsTypeOf, &pgWorker{})

	var describer coredatabase.ClusterDescriber
	c.Assert(dbAccessorOutput(w, &describer), tc.ErrorIsNil)
	nodes, err := describer.ClusterDetails(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(nodes, tc.HasLen, 0)

	// The controller database cannot start, which is fatal. Waiting also
	// runs the cleanup that unregisters the metrics.
	c.Check(w.Wait(), tc.ErrorMatches, `.*boom`)
}

func (s *manifoldSuite) TestStartConnectionDetailsError(c *tc.C) {
	defer s.setupMocks(c).Finish()

	cfg := s.getConfig()
	cfg.DBConnectionSource = stubConnectionSource{err: errors.New("boom")}

	_, err := Manifold(cfg).Start(c.Context(), dt.StubGetter(nil))
	c.Check(err, tc.ErrorMatches, `reading database connection details: boom`)
}

func (s *manifoldSuite) TestStartUnknownBackend(c *tc.C) {
	defer s.setupMocks(c).Finish()

	cfg := s.getConfig()
	cfg.DBConnectionSource = stubConnectionSource{details: connsource.Details{Backend: "mysql"}}

	_, err := Manifold(cfg).Start(c.Context(), dt.StubGetter(nil))
	c.Check(err, tc.ErrorMatches, `unknown database backend "mysql"`)
}

func (s *manifoldSuite) getConfig() ManifoldConfig {
	return ManifoldConfig{
		QueryLoggerName:           "query-logger",
		ControllerAgentConfigName: "controller-agent-config",
		ControllerStartupValues:   stubStartupValuesProvider{},
		DBConnectionSource: stubConnectionSource{details: connsource.Details{
			Backend: controller.DBBackendDqlite,
		}},
		Logger:               s.logger,
		PrometheusRegisterer: s.prometheusRegisterer,
		NewApp: func(string, ...app.Option) (DBApp, error) {
			return s.dbApp, nil
		},
		NewDBWorker: func(context.Context, DBOpener, string, ...TrackedDBWorkerOption) (TrackedDB, error) {
			return nil, nil
		},
		NewMetricsCollector: func() *Collector {
			return &Collector{}
		},
		NewNodeManager: func(database.NodeManagerConfig, logger.Logger, coredatabase.SlowQueryLogger) NodeManager {
			return s.nodeManager
		},
	}
}
