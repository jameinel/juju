// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql_test

import (
	"context"
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type schemaSuite struct {
	coretesting.BaseSuite

	placement postgresql.Placement
}

func TestSchemaSuite(t *testing.T) {
	tc.Run(t, &schemaSuite{})
}

func (s *schemaSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)

	admin := pgtesting.OpenAdmin(c)
	s.placement = postgresql.Placement{
		URL:          pgtesting.URL(),
		SchemaPrefix: pgtesting.NewSchemaPrefix(c),
	}
	pgtesting.DropSchemasOnCleanup(c, admin, s.placement.SchemaPrefix)
}

func (s *schemaSuite) TestSchemaLifecycle(c *tc.C) {
	admin, err := s.placement.OpenAdmin()
	c.Assert(err, tc.ErrorIsNil)
	defer admin.Close()

	name := s.placement.SchemaName("0b6c6d2e-1c55-4a0c-8f4f-2a3c1e9d7b10")
	c.Cleanup(func() {
		_ = postgresql.DropSchema(context.Background(), admin, name)
	})

	exists, err := postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)

	err = postgresql.CreateSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	exists, err = postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsTrue)

	err = postgresql.CreateSchema(c.Context(), admin, name)
	c.Check(err, tc.ErrorIs, postgresql.ErrSchemaExists)

	err = postgresql.EnsureSchema(c.Context(), admin, name)
	c.Check(err, tc.ErrorIsNil)

	db, err := s.placement.OpenNamespace("0b6c6d2e-1c55-4a0c-8f4f-2a3c1e9d7b10")
	c.Assert(err, tc.ErrorIsNil)
	defer db.Close()
	_, err = db.ExecContext(c.Context(), "CREATE TABLE t (id INT PRIMARY KEY)")
	c.Assert(err, tc.ErrorIsNil)
	var schema string
	err = db.QueryRowContext(c.Context(),
		"SELECT table_schema FROM information_schema.tables WHERE table_name = 't' AND table_schema = current_schema()",
	).Scan(&schema)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(schema, tc.Equals, name)

	err = postgresql.DropSchema(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	exists, err = postgresql.SchemaExists(c.Context(), admin, name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(exists, tc.IsFalse)

	err = postgresql.DropSchema(c.Context(), admin, name)
	c.Check(err, tc.ErrorIsNil)
}

func (s *schemaSuite) TestEnsureSchemaConcurrent(c *tc.C) {
	admin, err := s.placement.OpenAdmin()
	c.Assert(err, tc.ErrorIsNil)
	defer admin.Close()

	name := s.placement.SchemaName("concurrent")
	c.Cleanup(func() {
		_ = postgresql.DropSchema(context.Background(), admin, name)
	})

	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			errs <- postgresql.EnsureSchema(c.Context(), admin, name)
		}()
	}
	for range n {
		c.Check(<-errs, tc.ErrorIsNil)
	}
}
