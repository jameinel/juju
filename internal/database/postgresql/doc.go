// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package postgresql provides a database/sql driver for PostgreSQL that
// accepts the placeholder styles used by Juju's dqlite code, so that sqlair
// and raw database/sql queries run unchanged against either backend.
//
// The driver wraps the pgx v5 stdlib driver. Queries are rewritten from the
// SQLite style placeholders ("?" and "@sqlair_N") to PostgreSQL's "$N".
package postgresql
