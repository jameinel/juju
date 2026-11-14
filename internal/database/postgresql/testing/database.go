// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/postgresql"
	"github.com/juju/juju/internal/database/testconfig"
	"github.com/juju/juju/internal/uuid"
)

// Open creates a unique run-owned schema and closes all pools before dropping
// it during cleanup, including failed setup. It never sweeps other schemas.
// Tests using this helper run only when PostgreSQL is explicitly selected.
func Open(c *tc.C) *sql.DB {
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	if config.Backend != testconfig.PostgreSQL {
		c.Skip("requires JUJU_TEST_DATABASE=postgresql")
	}
	admin, err := postgresql.Open(config.PostgreSQLURL, withTimeouts)
	c.Assert(err, tc.ErrorIsNil)
	admin.SetMaxOpenConns(2)
	admin.SetMaxIdleConns(2)
	c.Cleanup(func() { c.Check(admin.Close(), tc.ErrorIsNil) })
	c.Assert(admin.PingContext(c.Context()), tc.ErrorIsNil)

	name := "juju-pgtest-" + uuid.MustNewUUID().String()
	quoted := pgx.Identifier{name}.Sanitize()
	c.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		c.Check(err, tc.ErrorIsNil)
	})
	_, err = admin.ExecContext(c.Context(), "CREATE SCHEMA "+quoted)
	c.Assert(err, tc.ErrorIsNil)
	db, err := postgresql.Open(config.PostgreSQLURL, withTimeouts, postgresql.WithSearchPath(name))
	c.Assert(err, tc.ErrorIsNil)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	c.Cleanup(func() { c.Check(db.Close(), tc.ErrorIsNil) })
	c.Assert(db.PingContext(c.Context()), tc.ErrorIsNil)
	return db
}

func withTimeouts(config *pgx.ConnConfig) error {
	config.ConnectTimeout = 10 * time.Second
	config.RuntimeParams["lock_timeout"] = "30s"
	config.RuntimeParams["statement_timeout"] = "60s"
	config.RuntimeParams["idle_in_transaction_session_timeout"] = "60s"
	return nil
}
