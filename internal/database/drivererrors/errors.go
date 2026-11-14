// Copyright 2023 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package drivererrors

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
)

// IsErrRetryable returns true if the given error might be
// transient and the interaction can be safely retried.
// See: https://github.com/canonical/go-dqlite/issues/220
func IsErrRetryable(err error) bool {
	if pgErr, ok := pgError(err); ok {
		// These abort the transaction. Connection loss and server shutdown
		// can leave commit outcome unknown, so do not retry them blindly.
		return pgErr.Code == "40001" || pgErr.Code == "40P01"
	}
	if pgconn.SafeToRetry(err) {
		return true
	}
	if IsErrLocked(err) {
		return true
	}

	// Unwrap errors one at a time.
	for ; err != nil; err = errors.Unwrap(err) {
		errStr := err.Error()

		if strings.Contains(errStr, "database is locked") {
			return true
		}

		if strings.Contains(errStr, "cannot start a transaction within a transaction") {
			return true
		}

		if strings.Contains(errStr, "bad connection") {
			return true
		}

		if strings.Contains(errStr, "checkpoint in progress") {
			return true
		}
	}

	return false
}

// IsConstraintError returns true if the given error is a constraint error.
func IsConstraintError(err error) bool {
	if pgErr, ok := pgError(err); ok {
		return strings.HasPrefix(pgErr.Code, "23") || pgErr.Code == "P0001"
	}
	if err == nil || IsErrLocked(err) {
		return false
	}
	return IsExtendedErrorCode(err, sqlite3.ErrConstraintCheck) ||
		IsExtendedErrorCode(err, sqlite3.ErrConstraintForeignKey) ||
		IsExtendedErrorCode(err, sqlite3.ErrConstraintNotNull) ||
		IsExtendedErrorCode(err, sqlite3.ErrConstraintPrimaryKey) ||
		IsExtendedErrorCode(err, sqlite3.ErrConstraintTrigger) ||
		IsExtendedErrorCode(err, sqlite3.ErrConstraintUnique) ||
		IsExtendedErrorCode(err, sqlite3.ErrConstraintRowID)
}
