// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"embed"
	"fmt"

	"github.com/juju/juju/core/database/schema"
)

// The PostgreSQL schema is generated from the dqlite schema, so it must be
// regenerated after the dqlite schema or its triggers change.
//go:generate go run ./../../generate/pgschemagen -dqlite=./dqlite -dest=./postgresql

//go:embed postgresql/controller/sql/*.sql
var postgresqlControllerSchemaDir embed.FS

//go:embed postgresql/model/sql/*.sql
var postgresqlModelSchemaDir embed.FS

// Backend identifies the database engine a schema is built for. The values
// match the controller config values that select the database backend.
type Backend string

const (
	// BackendDqlite is the dqlite database backend.
	BackendDqlite Backend = "dqlite"
	// BackendPostgreSQL is the PostgreSQL database backend.
	BackendPostgreSQL Backend = "postgresql"
)

// ControllerDDLForBackend returns the controller database schema for the
// given backend.
func ControllerDDLForBackend(backend Backend) (*schema.Schema, error) {
	switch backend {
	case BackendDqlite:
		return ControllerDDL(), nil
	case BackendPostgreSQL:
		return postgresqlSchema(postgresqlControllerSchemaDir, "postgresql/controller/sql")
	}
	return nil, fmt.Errorf("unknown database backend %q", backend)
}

// ModelDDLForBackend returns the model database schema for the given
// backend.
func ModelDDLForBackend(backend Backend) (*schema.Schema, error) {
	switch backend {
	case BackendDqlite:
		return ModelDDL(), nil
	case BackendPostgreSQL:
		return postgresqlSchema(postgresqlModelSchemaDir, "postgresql/model/sql")
	}
	return nil, fmt.Errorf("unknown database backend %q", backend)
}

// postgresqlSchema builds a schema from generated PostgreSQL patch files.
// Unlike the dqlite schema, the files include the change log triggers.
func postgresqlSchema(fs ReadFileDirFS, dir string) (*schema.Schema, error) {
	patches, err := readPatches(fs, dir)
	if err != nil {
		return nil, fmt.Errorf("reading PostgreSQL schema: %w", err)
	}
	ddl := schema.NewForDialect(schema.PostgreSQL)
	for _, fn := range patches {
		ddl.Add(fn())
	}
	return ddl, nil
}
