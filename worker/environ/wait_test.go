// Copyright 2012-2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package environ_test

import (
	"errors"
	"time"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/environs"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/worker/environ"
)

type WaitSuite struct {
	coretesting.BaseSuite

	st *fakeState
}

var _ = gc.Suite(&WaitSuite{})

func (s *WaitSuite) SetUpTest(c *gc.C) {
	s.BaseSuite.SetUpTest(c)
	s.st = &fakeState{
		Stub:    &testing.Stub{},
		changes: make(chan struct{}, 100),
	}
}

func (s *WaitSuite) TestStop(c *gc.C) {
	s.st.SetErrors(
		nil,                // WatchForEnvironConfigChanges
		errors.New("err1"), // Changes (closing the channel)
	)
	s.st.SetConfig(c, coretesting.Attrs{
		"type": "invalid",
	})

	w, err := s.st.WatchForEnvironConfigChanges()
	c.Assert(err, jc.ErrorIsNil)
	defer stopWorker(c, w)
	stop := make(chan struct{})
	close(stop) // close immediately so the loop exits.
	done := make(chan error)
	go func() {
		env, err := environ.WaitForEnviron(w, s.st, stop)
		c.Check(env, gc.IsNil)
		done <- err
	}()
	select {
	case <-environ.LoadedInvalid:
		c.Errorf("expected changes watcher to be closed")
	case err := <-done:
		c.Assert(err, gc.Equals, environ.ErrWaitAborted)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout waiting for the WaitForEnviron to stop")
	}
	s.st.CheckCallNames(c, "WatchForEnvironConfigChanges", "Changes")
}

func (s *WaitSuite) TestInvalidConfig(c *gc.C) {
	s.st.SetConfig(c, coretesting.Attrs{
		"type": "unknown",
	})

	w, err := s.st.WatchForEnvironConfigChanges()
	c.Assert(err, jc.ErrorIsNil)
	defer stopWorker(c, w)
	done := make(chan environs.Environ)
	go func() {
		env, err := environ.WaitForEnviron(w, s.st, nil)
		c.Check(err, jc.ErrorIsNil)
		done <- env
	}()
	select {
	case <-environ.LoadedInvalid:
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout waiting for the LoadedInvalid notification")
	}
	s.st.CheckCallNames(c, "WatchForEnvironConfigChanges", "EnvironConfig")
}
