// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package drivererrors

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL SQLSTATE codes that callers distinguish.
// See https://www.postgresql.org/docs/16/errcodes-appendix.html
const (
	pgRaiseException        = "P0001"
	pgForeignKeyViolation   = "23503"
	pgNotNullViolation      = "23502"
	pgUniqueViolation       = "23505"
	pgCheckViolation        = "23514"
	pgSerializationFailure  = "40001"
	pgDeadlockDetected      = "40P01"
	pgConnectionClassPrefix = "08"
	pgAdminShutdownPrefix   = "57P0"
)

// pgError returns the PostgreSQL error in the chain of err, if any.
func pgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr, true
	}
	return nil, false
}

// IsPGErrorCode returns true if err is a PostgreSQL error with the given
// SQLSTATE code.
func IsPGErrorCode(err error, code string) bool {
	pgErr, ok := pgError(err)
	return ok && pgErr.Code == code
}

// isPGRetryable returns true if the error is a PostgreSQL error that is
// transient: a serialisation failure, a deadlock, a connection exception
// (class 08) or a server shutdown. It also returns true for connection
// level failures reported by pgx that are not PostgreSQL errors.
func isPGRetryable(err error) bool {
	if pgErr, ok := pgError(err); ok {
		switch {
		case pgErr.Code == pgSerializationFailure,
			pgErr.Code == pgDeadlockDetected,
			strings.HasPrefix(pgErr.Code, pgConnectionClassPrefix),
			strings.HasPrefix(pgErr.Code, pgAdminShutdownPrefix):
			return true
		}
		return false
	}

	// Errors raised before the server answered, for example a connection
	// reset, carry no SQLSTATE and are marked safe to retry by pgconn.
	return pgconn.SafeToRetry(err)
}

// isPGConstraintError returns true if err is any PostgreSQL integrity
// constraint violation handled by Juju.
func isPGConstraintError(err error) bool {
	pgErr, ok := pgError(err)
	if !ok {
		return false
	}
	switch pgErr.Code {
	case pgCheckViolation, pgForeignKeyViolation, pgNotNullViolation,
		pgUniqueViolation, pgRaiseException:
		return true
	}
	return false
}

// IsPGError reports whether err is a PostgreSQL server error.
func IsPGError(err error) bool {
	_, ok := pgError(err)
	return ok
}

// isPGErrorTarget reports whether target is a pointer to a PostgreSQL error
// type, for errors.As.
func isPGErrorTarget(target any) bool {
	switch target.(type) {
	case *pgconn.PgError, **pgconn.PgError:
		return true
	}
	return false
}

// IsPGConstraintCheck returns true if the error is a PostgreSQL check
// constraint violation.
func IsPGConstraintCheck(err error) bool { return IsPGErrorCode(err, pgCheckViolation) }

// IsPGConstraintForeignKey returns true if the error is a PostgreSQL foreign
// key violation.
func IsPGConstraintForeignKey(err error) bool {
	return IsPGErrorCode(err, pgForeignKeyViolation)
}

// IsPGConstraintNotNull returns true if the error is a PostgreSQL not null
// violation.
func IsPGConstraintNotNull(err error) bool { return IsPGErrorCode(err, pgNotNullViolation) }

// IsPGConstraintUnique returns true if the error is a PostgreSQL unique
// violation that is not on a primary key.
func IsPGConstraintUnique(err error) bool {
	pgErr, ok := pgError(err)
	return ok && pgErr.Code == pgUniqueViolation && !isPrimaryKeyName(pgErr)
}

// IsPGConstraintPrimaryKey returns true if the error is a PostgreSQL unique
// violation on a primary key. PostgreSQL reports both with the same SQLSTATE,
// so primary keys are recognised by the default "_pkey" constraint name.
func IsPGConstraintPrimaryKey(err error) bool {
	pgErr, ok := pgError(err)
	return ok && pgErr.Code == pgUniqueViolation && isPrimaryKeyName(pgErr)
}

// IsPGConstraintTrigger returns true if the error was raised by a trigger
// function with RAISE EXCEPTION, the PostgreSQL equivalent of RAISE(ABORT).
func IsPGConstraintTrigger(err error) bool { return IsPGErrorCode(err, pgRaiseException) }

func isPrimaryKeyName(pgErr *pgconn.PgError) bool {
	return strings.HasSuffix(pgErr.ConstraintName, "_pkey")
}
