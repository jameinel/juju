// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storage_test

import (
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/juju/storage"
)

var expectedSubCommmandNames = []string{
	"create-pool",
	"help",
	"list",
	"list-pools",
	"list-volumes",
	"show",
}

type storageSuite struct {
	HelpStorageSuite
}

var _ = gc.Suite(&storageSuite{})

func (s *storageSuite) TestHelp(c *gc.C) {
	s.command = storage.NewSuperCommand().(*storage.Command)
	s.assertHelp(c, expectedSubCommmandNames)
}
