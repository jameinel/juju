// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package tools

var (
	Setenv           = setenv
	CheckToolsSeries = checkToolsSeries
	ArchiveAndSHA256 = archiveAndSHA256
	FindExecutable   = findExecutable
)

// SetSigningPublicKey sets a new public key for testing and returns the original key.
func SetSigningPublicKey(key string) string {
	oldKey := simplestreamsToolsPublicKey
	simplestreamsToolsPublicKey = key
	return oldKey
}
