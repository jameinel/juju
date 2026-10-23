// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package connsource provides the connection details for the external
// database backend. Callers read the details through the Source interface
// so that the agent.conf backed implementation can later be replaced by a
// relation or secret backed one that supports credential rotation.
package connsource

import (
	"context"

	"github.com/juju/juju/controller"
	"github.com/juju/juju/internal/errors"
)

// Details describes how to reach the database backend.
type Details struct {
	// Backend is either controller.DBBackendDqlite or
	// controller.DBBackendPostgreSQL.
	Backend string

	// URL is the connection URL of the external database. It is empty for
	// dqlite.
	URL string

	// CACert is the PEM CA certificate used to verify the external
	// database server. It is empty if TLS verification is not required.
	CACert string
}

// Source supplies the current connection Details.
type Source interface {
	// Details returns the current connection details.
	Details(ctx context.Context) (Details, error)
}

// Config is the subset of agent configuration that holds the database
// connection details.
type Config interface {
	DBBackend() string
	DBURL() string
	DBCACert() string
}

// NewAgentConfigSource returns a Source that reads the details from the
// agent configuration. An unset backend is reported as dqlite.
func NewAgentConfigSource(cfg Config) Source {
	return agentConfigSource{cfg: cfg}
}

type agentConfigSource struct {
	cfg Config
}

// Details implements Source.
func (s agentConfigSource) Details(context.Context) (Details, error) {
	backend := s.cfg.DBBackend()
	if backend == "" {
		backend = controller.DefaultDBBackend
	}

	d := Details{
		Backend: backend,
		URL:     s.cfg.DBURL(),
		CACert:  s.cfg.DBCACert(),
	}
	switch backend {
	case controller.DBBackendDqlite:
	case controller.DBBackendPostgreSQL:
		if d.URL == "" {
			return Details{}, errors.Errorf("database backend %q requires a URL", backend)
		}
	default:
		return Details{}, errors.Errorf("unknown database backend %q", backend)
	}
	return d, nil
}
