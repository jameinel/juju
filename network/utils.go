// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// +build go1.3

package network

import (
	"os"
)

// GetDefaultLXDBridgeName returns the name of the default bridge for lxd.
func GetDefaultLXDBridgeName() (string, error) {
	_, err := os.Lstat("/sys/class/net/lxdbr0/bridge")
	if err == nil {
		return "lxdbr0", nil
	}

	/* if it was some unknown error, return that */
	if !os.IsNotExist(err) {
		return "", err
	}

	return "lxcbr0", nil
}
