// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// pgschemagen translates assembled dqlite schema patches into checked-in
// PostgreSQL SQL. Unsupported forms fail before any output is written.
//
// The initial implementation supports typed tables, literal seed rows, plain
// indexes, ADD COLUMN post-patches and table-level foreign keys whose target
// key already exists. Forward references, deferred foreign keys, views,
// triggers, mixed-case quoted identifiers and INTEGER PRIMARY KEY allocation
// remain unsupported; complete Juju schema generation is not yet available.
// Run with -check to compare contents and file sets without modifying the
// destination.
package main
