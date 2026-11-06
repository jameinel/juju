// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

//go:build linux || darwin

package testing

import (
	"errors"
	"os"
	"syscall"
)

// tryLockSlot opens the slot file and takes an exclusive flock without
// waiting. It returns nil and no error when another process holds the slot.
func tryLockSlot(name string) (*os.File, error) {
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return f, nil
	}
	_ = f.Close()
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, nil
	}
	return nil, err
}
