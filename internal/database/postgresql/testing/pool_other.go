// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

//go:build !linux && !darwin

package testing

import (
	"errors"
	"os"
)

func tryLockSlot(string) (*os.File, error) {
	return nil, errors.New("PostgreSQL test database pools need flock")
}
