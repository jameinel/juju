// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package profile_test

import (
//	"io"

	"github.com/juju/errors"
	"github.com/juju/names"
	gc "gopkg.in/check.v1"
	jc "github.com/juju/testing/checkers"

	"github.com/juju/juju/apiserver/profile"
	"github.com/juju/juju/apiserver/common"
	apiservertesting "github.com/juju/juju/apiserver/testing"
	"github.com/juju/juju/juju/testing"
)

type profileSuite struct {
	testing.JujuConnSuite
	resources  *common.Resources
	authorizer *apiservertesting.FakeAuthorizer
	api        *profile.API
}

var _ = gc.Suite(&profileSuite{})

func (s *profileSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)
	s.resources = common.NewResources()
	tag := names.NewLocalUserTag("spam")
	s.authorizer = &apiservertesting.FakeAuthorizer{Tag: tag}
	var err error
	s.api, err = profile.NewAPI(s.State, s.resources, s.authorizer)
	c.Assert(err, gc.IsNil)
}

func (s *profileSuite) TestRegistered(c *gc.C) {
	// Profile was not in 1.18, so it should not have a v0 registered
	_, err := common.Facades.GetType("Profile", 0)
	c.Check(err, jc.Satisfies, errors.IsNotFound)
	_, err = common.Facades.GetType("Profile", 1)
	c.Check(err, gc.IsNil)
}

func (s *profileSuite) TestNewAPINotAuthorized(c *gc.C) {
	s.authorizer.Tag = names.NewServiceTag("eggs")
	_, err := profile.NewAPI(s.State, s.resources, s.authorizer)

	c.Check(errors.Cause(err), gc.Equals, common.ErrPerm)
}

func (s *profileSuite) TestNewAPIOkay(c *gc.C) {
	_, err := profile.NewAPI(s.State, s.resources, s.authorizer)
	c.Assert(err, gc.IsNil)
}

