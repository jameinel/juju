// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package drivererrors

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
)

func pgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	ok := errors.As(err, &pgErr)
	return pgErr, ok && pgErr != nil
}

func isPGErrorTarget(target any) bool {
	switch target.(type) {
	case *pgconn.PgError, **pgconn.PgError:
		return true
	}
	return false
}

// PostgreSQL uses one SQLSTATE for primary-key and unique violations. Generated
// PostgreSQL schemas must retain the default "_pkey" primary-key naming
// convention to preserve Juju's distinction between these checks.
func isPGExtendedErrorCode(err *pgconn.PgError, code sqlite3.ErrNoExtended) bool {
	switch code {
	case sqlite3.ErrConstraintCheck:
		return err.Code == "23514"
	case sqlite3.ErrConstraintForeignKey:
		return err.Code == "23503"
	case sqlite3.ErrConstraintNotNull:
		return err.Code == "23502"
	case sqlite3.ErrConstraintPrimaryKey:
		return err.Code == "23505" && strings.HasSuffix(err.ConstraintName, "_pkey")
	case sqlite3.ErrConstraintUnique:
		return err.Code == "23505" && !strings.HasSuffix(err.ConstraintName, "_pkey")
	case sqlite3.ErrConstraintTrigger:
		return err.Code == "P0001"
	}
	return false
}
