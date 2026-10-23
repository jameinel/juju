// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package drivererrors

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type postgresqlSuite struct {
	coretesting.BaseSuite
}

func TestPostgreSQLSuite(t *testing.T) {
	tc.Run(t, &postgresqlSuite{})
}

func (s *postgresqlSuite) TestRetryable(c *tc.C) {
	for _, code := range []string{"40001", "40P01", "08000", "08006", "57P01"} {
		err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code})
		c.Check(IsErrRetryable(err), tc.IsTrue, tc.Commentf("code %s", code))
	}
	for _, code := range []string{"23505", "23503", "42P01", "P0001"} {
		err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code})
		c.Check(IsErrRetryable(err), tc.IsFalse, tc.Commentf("code %s", code))
	}
}

func (s *postgresqlSuite) TestConstraintErrors(c *tc.C) {
	for _, code := range []string{"23502", "23503", "23505", "23514", "P0001"} {
		err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code})
		c.Check(IsConstraintError(err), tc.IsTrue, tc.Commentf("code %s", code))
	}
	c.Check(IsConstraintError(&pgconn.PgError{Code: "40001"}), tc.IsFalse)
}

func (s *postgresqlSuite) TestUniqueAndPrimaryKey(c *tc.C) {
	pk := &pgconn.PgError{Code: "23505", ConstraintName: "machine_pkey"}
	uq := &pgconn.PgError{Code: "23505", ConstraintName: "idx_machine_name"}

	c.Check(IsPGConstraintPrimaryKey(pk), tc.IsTrue)
	c.Check(IsPGConstraintUnique(pk), tc.IsFalse)
	c.Check(IsPGConstraintUnique(uq), tc.IsTrue)
	c.Check(IsPGConstraintPrimaryKey(uq), tc.IsFalse)
}

func (s *postgresqlSuite) TestIsError(c *tc.C) {
	c.Check(IsError(fmt.Errorf("x: %w", &pgconn.PgError{Code: "23505"})), tc.IsTrue)
	c.Check(IsError(fmt.Errorf("plain")), tc.IsFalse)
	c.Check(IsErrorTarget(&pgconn.PgError{}), tc.IsTrue)
	var pgErr *pgconn.PgError
	c.Check(IsErrorTarget(&pgErr), tc.IsTrue)
}
