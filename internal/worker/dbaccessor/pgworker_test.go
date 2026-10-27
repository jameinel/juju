// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dbaccessor

import (
	stdtesting "testing"

	"github.com/juju/clock"
	"github.com/juju/errors"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/postgresql"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type pgWorkerConfigSuite struct {
	coretesting.BaseSuite
}

func TestPgWorkerConfigSuite(t *stdtesting.T) {
	tc.Run(t, &pgWorkerConfigSuite{})
}

func (s *pgWorkerConfigSuite) TestValidate(c *tc.C) {
	valid := func() PostgresWorkerConfig {
		return PostgresWorkerConfig{
			Placement:        postgresql.Placement{URL: "postgres://db.example/juju"},
			Clock:            clock.WallClock,
			MetricsCollector: NewMetricsCollector(),
			Logger:           loggertesting.WrapCheckLog(c),
			NewDBWorker:      NewTrackedDBWorker,
			ControllerID:     "0",
		}
	}
	c.Check(valid().Validate(), tc.ErrorIsNil)

	for name, breakIt := range map[string]func(*PostgresWorkerConfig){
		"url":          func(cfg *PostgresWorkerConfig) { cfg.Placement.URL = "" },
		"clock":        func(cfg *PostgresWorkerConfig) { cfg.Clock = nil },
		"metrics":      func(cfg *PostgresWorkerConfig) { cfg.MetricsCollector = nil },
		"logger":       func(cfg *PostgresWorkerConfig) { cfg.Logger = nil },
		"newDBWorker":  func(cfg *PostgresWorkerConfig) { cfg.NewDBWorker = nil },
		"controllerID": func(cfg *PostgresWorkerConfig) { cfg.ControllerID = "" },
	} {
		cfg := valid()
		breakIt(&cfg)
		c.Check(cfg.Validate(), tc.ErrorIs, errors.NotValid, tc.Commentf("missing %s", name))
	}
}
