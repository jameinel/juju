// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"database/sql"
	"errors"
	"os"
	stdtesting "testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juju/tc"

	"github.com/juju/juju/core/database/dialect"
	coretesting "github.com/juju/juju/internal/testing"
)

type dialectSuite struct {
	coretesting.BaseSuite
}

func TestDialectSuite(t *stdtesting.T) {
	tc.Run(t, &dialectSuite{})
}

func (s *dialectSuite) TestSchemaChangeKey(c *tc.C) {
	const productionKey = 7671154592639640684

	key := schemaChangeKey(1234)
	c.Check(key>>24, tc.Equals, int64(schemaChangeKeyPrefix))
	c.Check(key&0xffffff, tc.Equals, int64(1234))
	c.Check(key > 0, tc.IsTrue)
	c.Check(key, tc.Not(tc.Equals), int64(productionKey))
	c.Check(schemaChangeKey(1235), tc.Not(tc.Equals), key)
	c.Check(schemaChangeKey(1<<24|1234), tc.Equals, key)
	c.Check(Dialect.BeginSchemaChangeSQL(), tc.Equals,
		processDialect{key: schemaChangeKey(os.Getpid())}.BeginSchemaChangeSQL())
	c.Check(Dialect.BeginSchemaChangeSQL(), tc.Contains,
		"SET TRANSACTION ISOLATION LEVEL READ COMMITTED;")
}

func (s *dialectSuite) TestDialectMatchesPostgreSQL(c *tc.C) {
	c.Check(Dialect.String(), tc.Equals, dialect.PostgreSQL.String())
	c.Check(Dialect.SchemaTableDDL(), tc.Equals, dialect.PostgreSQL.SchemaTableDDL())
	c.Check(Dialect.InsertSchemaVersionSQL(), tc.Equals,
		dialect.PostgreSQL.InsertSchemaVersionSQL())
	c.Check(Dialect.BeginSchemaChangeSQL(), tc.Not(tc.Equals),
		dialect.PostgreSQL.BeginSchemaChangeSQL())
}

// TestSchemaChangeLock checks that a test process's schema change lock does
// not wait for the production one, but does wait for itself.
func (s *dialectSuite) TestSchemaChangeLock(c *tc.C) {
	admin := OpenAdmin(c)

	beginSchemaChange(c, admin, dialect.PostgreSQL)
	c.Check(tryBeginSchemaChange(c, admin, Dialect), tc.ErrorIsNil)

	beginSchemaChange(c, admin, Dialect)
	err := tryBeginSchemaChange(c, admin, Dialect)
	var pgErr *pgconn.PgError
	c.Assert(errors.As(err, &pgErr), tc.IsTrue, tc.Commentf("%v", err))
	c.Check(pgErr.Code, tc.Equals, "55P03")
}

// beginSchemaChange begins a schema change in a transaction that is rolled
// back when the test finishes.
func beginSchemaChange(c *tc.C, db *sql.DB, d dialect.Dialect) {
	tx, err := db.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = tx.Rollback() })

	_, err = tx.ExecContext(c.Context(), d.BeginSchemaChangeSQL())
	c.Assert(err, tc.ErrorIsNil)
}

// tryBeginSchemaChange begins a schema change in a transaction that it then
// rolls back, waiting at most 100ms for the lock. It checks that the
// transaction runs at READ COMMITTED.
func tryBeginSchemaChange(c *tc.C, db *sql.DB, d dialect.Dialect) error {
	tx, err := db.BeginTx(c.Context(), &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(c.Context(), "SET LOCAL lock_timeout = 100")
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(c.Context(), d.BeginSchemaChangeSQL()); err != nil {
		return err
	}
	var level string
	err = tx.QueryRowContext(c.Context(), "SHOW transaction_isolation").Scan(&level)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(level, tc.Equals, "read committed")
	return nil
}
