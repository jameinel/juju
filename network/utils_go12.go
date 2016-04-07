// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// +build !go1.3

package network

func GetDefaultLXDBridgeName() (string, error) {
	/* lxd not supported in go1.2 */
	return "lxcbr0", nil
}
