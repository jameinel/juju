// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package profile_test

import (
	//	"io"

	"github.com/juju/errors"
	"github.com/juju/names"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/profile"
	apiservertesting "github.com/juju/juju/apiserver/testing"
)

type profileSuite struct {
	resources  *common.Resources
	authorizer *apiservertesting.FakeAuthorizer
	api        *profile.API
}

var _ = gc.Suite(&profileSuite{})

func (s *profileSuite) SetUpTest(c *gc.C) {
	s.resources = common.NewResources()
	tag := names.NewLocalUserTag("spam")
	s.authorizer = &apiservertesting.FakeAuthorizer{Tag: tag}
	var err error
	s.api, err = profile.NewAPI(nil, s.resources, s.authorizer)
	c.Assert(err, gc.IsNil)
	// TODO: Consider setting up stubs for the real Start/StopCPUProfile so
	// that we don't actually change global state while running this test
	// suite
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
	_, err := profile.NewAPI(nil, s.resources, s.authorizer)

	c.Check(errors.Cause(err), gc.Equals, common.ErrPerm)
}

func (s *profileSuite) TestNewAPIOkay(c *gc.C) {
	_, err := profile.NewAPI(nil, s.resources, s.authorizer)
	c.Assert(err, gc.IsNil)
}

func (s *profileSuite) TestStartCPUProfile(c *gc.C) {
	err := s.api.StartCPUProfile()
	c.Assert(err, gc.IsNil)
	result, err := s.api.StopCPUProfile()
	c.Assert(err, gc.IsNil)
	c.Check(len(result.Profile), jc.GreaterThan, 0)
}

func (s *profileSuite) TestStartCPUProfileAlreadyStarted(c *gc.C) {
	err := s.api.StartCPUProfile()
	c.Assert(err, gc.IsNil)
	defer s.api.StopCPUProfile()
	err = s.api.StartCPUProfile()
	c.Assert(err, gc.ErrorMatches, "CPU profiling already active")
}

func (s *profileSuite) TestStopCPUProfileNotStarted(c *gc.C) {
	_, err := s.api.StopCPUProfile()
	c.Check(err, gc.ErrorMatches, "CPU profiling not active")
}
