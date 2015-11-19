// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package workertest

import (
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/worker"
	"github.com/juju/juju/worker/catacomb"
)

// ContextTimeout is the maximum time a TestFunc will be allowed to run
// without triggering a failure. It needs to be significantly longer than
// KillTimeout, lest it obscure more important failures from Kill helpers.
const ContextTimeout = 2 * KillTimeout

// TestFunc is a test case to be Run by a Fixture on its own goroutine. Any
// workers started by the TestFunc should be Track()ed by the Context to make
// sure they complete (or, if they don't, fail the test).
//
// Because a TestFunc is run on its own goroutine, it should avoid Asserts.
type TestFunc func(Context)

// Context supplies worker-Track()ing services to TestFuncs. A Context is only
// valid within the TestFunc to which it is passed.
type Context interface {

	// Track registers the worker with the Context, ensuring that either the
	// worker will complete or the test will fail.
	//
	// Note that if a worker fails while the TestFunc is running, any other
	// tracked workers will be stopped, and subsequent Tracks may fail the
	// test. This is sometimes convenient, and sometimes not; when you expect
	// a worker to fail mid-test, an inline CheckKilled or a deferred DirtyKill
	// is probably more suitable.
	Track(w worker.Worker)
}

// Config specifies the behaviour that varies across Fixtures.
type Config struct {

	// IgnoreErrors, if false, will cause a Fixture.Run to fail the test if
	// *any* Track()ed worker returns an error from Wait().
	IgnoreErrors bool
}

// Fixture defines a template for some number of test runs.
type Fixture struct {

	// Config specifies behaviour that applies to all test runs with the Fixture.
	Config Config
}

// Run invokes the TestFunc on a new goroutine with a fresh Context; runs the
// test; and ensures that all Track()ed workers complete, or the test fails.
// By default, it will also fail the test if any worker returns an error; this
// can be disabled by setting Config.IgnoreErrors to true.
//
// Run will time out and fail the test if it takes more than twice as long as
// coretesting.LongWait; it uses Assert, and so must not be called from a non-
// testcase goroutine.
func (fix Fixture) Run(c *gc.C, test TestFunc) {
	context := &context{
		c:      c,
		config: fix.Config,
	}
	err := catacomb.Invoke(catacomb.Plan{
		Site: &context.catacomb,
		Work: func() error {
			c.Logf("running test...")
			test(context)
			c.Logf("...test run complete")
			c.Logf("cleaning up context...")
			return nil
		},
	})
	c.Assert(err, jc.ErrorIsNil)

	wait := make(chan error)
	go func() {
		wait <- context.catacomb.Wait()
	}()
	select {
	case err := <-wait:
		c.Logf("...context cleanup complete")
		if !fix.Config.IgnoreErrors {
			c.Assert(err, jc.ErrorIsNil)
		}
	case <-time.After(ContextTimeout):
		c.Fatalf("timed out waiting for test to complete")
	}
}

// context implements Context.
type context struct {
	c        *gc.C
	config   Config
	catacomb catacomb.Catacomb
}

// Track is part of the Context interface.
func (context *context) Track(w worker.Worker) {
	err := context.catacomb.Add(w)
	// IgnoreErrors should not apply here: the only error condition that can
	// apply here is when a worker is Track()ed outside a TestFunc, and that's
	// broken regardless. In that situation, we're likely to be on the main
	// test goroutine (or just in a situation of complete and desperate chaos)
	// so we may as well Assert and fail as fast as possible.
	context.c.Assert(err, jc.ErrorIsNil)
}
