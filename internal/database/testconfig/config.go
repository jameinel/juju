// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package testconfig centralises database backend selection for test harnesses.
// It must not be used to configure production databases.
package testconfig

import (
	"fmt"
	"os"
	"strings"

	"github.com/juju/juju/internal/database/dqlite"
)

const (
	// BackendEnvVar selects a test database backend independently of its URL.
	BackendEnvVar = "JUJU_TEST_DATABASE"

	// PostgreSQLURLEnvVar configures the PostgreSQL test connection.
	PostgreSQLURLEnvVar = "JUJU_TEST_POSTGRESQL_URL"
)

// Backend identifies the actual database backend, not its SQL dialect.
type Backend string

const (
	SQLite     Backend = "sqlite"
	Dqlite     Backend = "dqlite"
	PostgreSQL Backend = "postgresql"
)

// Config holds the database configuration selected for a test process.
type Config struct {
	Backend Backend

	// PostgreSQLURL is populated only when PostgreSQL is selected.
	PostgreSQLURL string
}

// Load reads the shared database test settings. An unset or empty selector
// preserves the build-tag-selected SQLite/dqlite default. The PostgreSQL URL
// is ignored unless PostgreSQL is explicitly selected.
//
// An unknown backend, a SQLite/dqlite selection incompatible with the build
// tags, or PostgreSQL without a URL returns an error. Backend harnesses must
// also reject configurations that they do not yet implement.
func Load() (Config, error) {
	compiledBackend := SQLite
	if dqlite.Enabled {
		compiledBackend = Dqlite
	}

	backend := Backend(os.Getenv(BackendEnvVar))
	if backend == "" {
		backend = compiledBackend
	}

	switch backend {
	case SQLite, Dqlite:
		if backend != compiledBackend {
			return Config{}, fmt.Errorf(
				"%s=%s requires a %s test build; this build uses %s",
				BackendEnvVar, backend, backend, compiledBackend,
			)
		}
		return Config{Backend: backend}, nil
	case PostgreSQL:
		url := os.Getenv(PostgreSQLURLEnvVar)
		if strings.TrimSpace(url) == "" {
			return Config{}, fmt.Errorf("%s is required when %s=%s",
				PostgreSQLURLEnvVar, BackendEnvVar, PostgreSQL)
		}
		return Config{Backend: backend, PostgreSQLURL: url}, nil
	default:
		return Config{}, fmt.Errorf("unknown %s value %q: expected sqlite, dqlite or postgresql",
			BackendEnvVar, backend)
	}
}
