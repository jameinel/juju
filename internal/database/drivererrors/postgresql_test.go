// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package drivererrors

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juju/tc"
	"github.com/mattn/go-sqlite3"

	coretesting "github.com/juju/juju/internal/testing"
)

type postgresqlSuite struct {
	coretesting.BaseSuite
}

func TestPostgreSQLSuite(t *testing.T) {
	tc.Run(t, &postgresqlSuite{})
}

func (s *postgresqlSuite) TestClassification(c *tc.C) {
	tests := []struct {
		code       string
		constraint string
		extended   sqlite3.ErrNoExtended
		retry      bool
		integrity  bool
	}{
		{code: "23502", extended: sqlite3.ErrConstraintNotNull, integrity: true},
		{code: "23503", extended: sqlite3.ErrConstraintForeignKey, integrity: true},
		{code: "23505", constraint: "parent_pkey", extended: sqlite3.ErrConstraintPrimaryKey, integrity: true},
		{code: "23505", constraint: "parent_name_key", extended: sqlite3.ErrConstraintUnique, integrity: true},
		{code: "23514", extended: sqlite3.ErrConstraintCheck, integrity: true},
		{code: "23P01", integrity: true},
		{code: "P0001", extended: sqlite3.ErrConstraintTrigger, integrity: true},
		{code: "40001", retry: true},
		{code: "40P01", retry: true},
		{code: "08006"},
		{code: "08007"},
		{code: "57P01"},
		{code: "57014"},
		{code: "42601"},
		{code: "42P01"},
	}
	codes := []sqlite3.ErrNoExtended{
		sqlite3.ErrConstraintCheck, sqlite3.ErrConstraintForeignKey,
		sqlite3.ErrConstraintNotNull, sqlite3.ErrConstraintPrimaryKey,
		sqlite3.ErrConstraintUnique, sqlite3.ErrConstraintTrigger, sqlite3.ErrConstraintRowID,
	}
	for _, test := range tests {
		// A server message must not trigger the SQLite string fallbacks.
		pgErr := &pgconn.PgError{Code: test.code, ConstraintName: test.constraint, Message: "bad connection"}
		for _, err := range []error{pgErr, fmt.Errorf("wrapped: %w", pgErr)} {
			c.Check(IsError(err), tc.IsTrue)
			c.Check(IsConstraintError(err), tc.Equals, test.integrity)
			c.Check(IsErrRetryable(err), tc.Equals, test.retry)
			for _, code := range codes {
				c.Check(IsExtendedErrorCode(err, code), tc.Equals, code == test.extended,
					tc.Commentf("SQLSTATE %s, extended code %d", test.code, code))
			}
		}
	}
	var pgErr *pgconn.PgError
	c.Check(IsErrorTarget(&pgErr), tc.IsTrue)
	c.Check(IsErrorTarget(&pgconn.PgError{}), tc.IsTrue)
	c.Check(IsErrorTarget("not a driver error"), tc.IsFalse)
	c.Check(IsError(nil), tc.IsFalse)
	c.Check(IsConstraintError(nil), tc.IsFalse)
	c.Check(IsErrRetryable(nil), tc.IsFalse)
}
