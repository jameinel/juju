// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package dialect_test

import (
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/core/database/dialect"
	coretesting "github.com/juju/juju/internal/testing"
)

type dialectSuite struct{}

func TestDialectSuite(t *testing.T) {
	tc.Run(t, &dialectSuite{})
}

func (*dialectSuite) TestImports(c *tc.C) {
	found := coretesting.FindJujuCoreImports(c, "github.com/juju/juju/core/database/dialect")

	// This package must stay a leaf, so that the database test suites can
	// import it without creating import cycles.
	c.Check(found, tc.HasLen, 0)
}

func (*dialectSuite) TestDialects(c *tc.C) {
	c.Check(dialect.SQLite.String(), tc.Equals, "sqlite")
	c.Check(dialect.PostgreSQL.String(), tc.Equals, "postgresql")
	c.Check(dialect.SQLite, tc.Not(tc.Equals), dialect.PostgreSQL)

	c.Check(dialect.SQLite.SchemaTableDDL(), tc.Contains, "CREATE TABLE IF NOT EXISTS schema")
	c.Check(dialect.PostgreSQL.SchemaTableDDL(), tc.Contains, "CREATE TABLE IF NOT EXISTS schema")
	c.Check(dialect.SQLite.InsertSchemaVersionSQL(), tc.Contains, "VALUES (?, ?,")
	c.Check(dialect.PostgreSQL.InsertSchemaVersionSQL(), tc.Contains, "VALUES ($1, $2,")
}
