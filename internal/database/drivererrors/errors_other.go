//go:build !dqlite

// Copyright 2025 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package drivererrors

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

// IsExtendedErrorCode matches a SQLite extended code or its PostgreSQL
// equivalent against a database driver error.
// Note: ErrNo means error number, not no error.
func IsExtendedErrorCode(err error, code sqlite3.ErrNoExtended) bool {
	if pgErr, ok := pgError(err); ok {
		return isPGExtendedErrorCode(pgErr, code)
	}
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.ExtendedCode == code
	}

	return false
}

// IsErrLocked returns true if the error is locked, or a busy error, then we
// can't have a extended error code. In those cases, we just return early and
// prevent error unwrapping for common cases.
func IsErrLocked(err error) bool {
	return errors.Is(err, sqlite3.ErrLocked) || errors.Is(err, sqlite3.ErrBusy)
}

// IsError reports if the any type passed to it is a database driver error in
// Juju. The purpose of this function is so that our domain error masking can
// assert if a specific error needs to be hidden from layers above that of the
// domain/state.
func IsError(err error) bool {
	if _, ok := pgError(err); ok {
		return true
	}
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr)
}

// IsErrorTarget reports if the any type passed to it is a database driver
// error.
func IsErrorTarget(target any) bool {
	if isPGErrorTarget(target) {
		return true
	}
	if _, is := target.(*sqlite3.Error); is {
		return true
	}
	if _, is := target.(sqlite3.Error); is {
		return true
	}

	return false
}
