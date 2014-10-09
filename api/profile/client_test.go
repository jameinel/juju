// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package profile_test

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/api/profile"
)

type profileSuite struct {
	baseSuite
}

var _ = gc.Suite(&profileSuite{})

func (s *profileSuite) TestClient(c *gc.C) {
	facade := profile.ExposeFacade(s.client)

	c.Check(facade.Name(), gc.Equals, "Profile")
}

func (s *profileSuite) TestStartStopCPUProfile(c *gc.C) {
	err := s.client.StartCPUProfile()
	c.Assert(err, gc.IsNil)
	result, err := s.client.StopCPUProfile()
	c.Assert(err, gc.IsNil)
	c.Check(len(result), jc.GreaterThan, 0)
}
