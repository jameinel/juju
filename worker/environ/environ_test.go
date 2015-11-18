// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package environ_test

import (
	"strings"
	"sync"
	"time"

	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/environs/config"
	"github.com/juju/juju/provider/dummy"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/juju/watcher"
	"github.com/juju/juju/worker"
	"github.com/juju/juju/worker/environ"
)

type TrackerSuite struct {
	coretesting.BaseSuite

	st *fakeState
}

var _ = gc.Suite(&TrackerSuite{})

func (s *TrackerSuite) SetUpTest(c *gc.C) {
	s.BaseSuite.SetUpTest(c)
	s.st = &fakeState{
		Stub:    &testing.Stub{},
		changes: make(chan struct{}, 100),
	}
	s.st.SetConfig(c, nil)
}

func (s *TrackerSuite) TestValidateObserver(c *gc.C) {
	config := environ.Config{}
	check := func(err error) {
		c.Check(err, jc.Satisfies, errors.IsNotValid)
		c.Check(err, gc.ErrorMatches, "nil Observer not valid")
	}

	err := config.Validate()
	check(err)

	tracker, err := environ.NewTracker(config)
	c.Check(tracker, gc.IsNil)
	check(err)

	s.st.CheckCallNames(c)
}

func (s *TrackerSuite) TestEnvironConfigFails(c *gc.C) {
	s.st.Stub.SetErrors(errors.New("no yuo"))

	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Check(err, gc.ErrorMatches, "cannot read environ config: no yuo")
	c.Check(tracker, gc.IsNil)

	s.st.CheckCallNames(c, "EnvironConfig")
}

func (s *TrackerSuite) TestEnvironConfigInvalid(c *gc.C) {
	s.st.SetConfig(c, coretesting.Attrs{
		"type": "unknown",
	})

	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Check(err, gc.ErrorMatches, `cannot create environ: no registered provider for "unknown"`)
	c.Check(tracker, gc.IsNil)

	s.st.CheckCallNames(c, "EnvironConfig")
}

func (s *TrackerSuite) TestEnvironConfigValid(c *gc.C) {
	s.st.SetConfig(c, coretesting.Attrs{
		"name": "this-particular-name",
	})

	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Check(err, jc.ErrorIsNil)
	defer stopWorker(c, tracker)
	gotEnviron := tracker.Environ()
	c.Assert(gotEnviron, gc.NotNil)

	c.Check(gotEnviron.Config().Name(), gc.Equals, "this-particular-name")
}

func (s *TrackerSuite) TestWatchFails(c *gc.C) {
	s.st.Stub.SetErrors(nil, errors.New("grrk splat"))

	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Check(err, jc.ErrorIsNil)
	defer worker.Stop(tracker)
	gotEnviron := tracker.Environ()
	c.Assert(gotEnviron, gc.NotNil)

	wait := make(chan error)
	go func() {
		wait <- tracker.Wait()
	}()
	select {
	case err := <-wait:
		c.Check(err, gc.ErrorMatches, "cannot watch environ config: grrk splat")
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timed out waiting for tracker to fail")
	}

	s.st.CheckCallNames(c, "EnvironConfig", "WatchForEnvironConfigChanges")
}

func (s *TrackerSuite) TestWatchedEnvironConfigFails(c *gc.C) {
	s.st.Stub.SetErrors(nil, nil, errors.New("blam ouch"))

	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Check(err, jc.ErrorIsNil)
	defer worker.Stop(tracker)
	gotEnviron := tracker.Environ()
	c.Assert(gotEnviron, gc.NotNil)

	wait := make(chan error)
	go func() {
		wait <- tracker.Wait()
	}()
	select {
	case err := <-wait:
		c.Check(err, gc.ErrorMatches, "cannot read environ config: blam ouch")
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timed out waiting for tracker to fail")
	}

	s.st.CheckCallNames(c, "EnvironConfig", "WatchForEnvironConfigChanges", "EnvironConfig")
}

func (s *TrackerSuite) TestWatchedEnvironConfigInvalid(c *gc.C) {
	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Check(err, jc.ErrorIsNil)
	defer worker.Stop(tracker)
	gotEnviron := tracker.Environ()
	c.Assert(gotEnviron, gc.NotNil)

	s.st.SetConfig(c, coretesting.Attrs{
		"type": "unknown",
	})
	s.st.Send()
	wait := make(chan error)
	go func() {
		wait <- tracker.Wait()
	}()
	select {
	case err := <-wait:
		c.Check(err, gc.ErrorMatches, `cannot create environ: no registered provider for "unknown"`)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timed out waiting for tracker to fail")
	}

	s.st.CheckCallNames(c, "EnvironConfig", "WatchForEnvironConfigChanges", "EnvironConfig")
}

func (s *TrackerSuite) TestWatchedEnvironConfigIncompatible(c *gc.C) {
	c.Fatalf("xxx")
}

func (s *TrackerSuite) TestWatchedEnvironConfigUpdates(c *gc.C) {
	c.Fatalf("xxx")
}

func (s *TrackerSuite) TestInvalidEnvironError(c *gc.C) {
	s.st.SetConfig(c, coretesting.Attrs{
		"type": "unknown",
	})
	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Assert(err, gc.ErrorMatches,
		`cannot create environ: no registered provider for "unknown"`,
	)
	c.Assert(tracker, gc.IsNil)
	s.st.CheckCallNames(c, "EnvironConfig")
}

func (s *TrackerSuite) TestEnvironmentChanges(c *gc.C) {
	s.st.SetConfig(c, nil)

	logc := make(logChan, 1000)
	c.Assert(loggo.RegisterWriter("testing", logc, loggo.WARNING), gc.IsNil)
	defer loggo.RemoveWriter("testing")

	tracker, err := environ.NewTracker(environ.Config{
		Observer: s.st,
	})
	c.Assert(err, jc.ErrorIsNil)

	env := tracker.Environ()
	s.st.AssertConfig(c, env.Config())

	// Change to an invalid configuration and check that the tracker fails.
	originalConfig, err := s.st.EnvironConfig()
	c.Assert(err, jc.ErrorIsNil)

	s.st.SetConfig(c, coretesting.Attrs{
		"type": "invalid",
	})

	// Wait for the observer to register the invalid environment
loop:
	for {
		select {
		case msg := <-logc:
			if strings.Contains(msg, "error creating an environment") {
				break loop
			}
		case <-time.After(coretesting.LongWait):
			c.Fatalf("timed out waiting to see broken environment")
		}
	}
	// Check that the returned environ is still the same.
	env = tracker.Environ()
	c.Assert(env.Config().AllAttrs(), jc.DeepEquals, originalConfig.AllAttrs())

	// Change the environment back to a valid configuration
	// with a different name and check that we see it.
	s.st.SetConfig(c, coretesting.Attrs{
		"name": "a-new-name",
	})

	for a := coretesting.LongAttempt.Start(); a.Next(); {
		env := tracker.Environ()
		if !a.HasNext() {
			c.Fatalf("timed out waiting for new environ")
		}
		if env.Config().Name() == "a-new-name" {
			break
		}
	}
}

type logChan chan string

func (logc logChan) Write(level loggo.Level, name, filename string, line int, timestamp time.Time, message string) {
	logc <- message
}

type fakeState struct {
	changes chan struct{}

	mu sync.Mutex
	*testing.Stub
	config  map[string]interface{}
	workers []worker.Worker
}

// WatchForEnvironConfigChanges implements EnvironConfigObserver.
func (s *fakeState) WatchForEnvironConfigChanges() (watcher.NotifyWatcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.MethodCall(s, "WatchForEnvironConfigChanges")
	if err := s.NextErr(); err != nil {
		return nil, err
	}

	w := &fakeWatcher{
		changes: s.changes,
	}
	go func() {
		defer w.tomb.Done()
		<-w.tomb.Dying
	}()
	return w, nil
}

// EnvironConfig implements EnvironConfigObserver.
func (s *fakeState) EnvironConfig() (*config.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.MethodCall(s, "EnvironConfig")
	if err := s.NextErr(); err != nil {
		return nil, err
	}
	return config.New(config.NoDefaults, s.config)
}

// SetConfig changes the stored environment config with the given
// extraAttrs and triggers a change for the watcher.
func (s *fakeState) SetConfig(c *gc.C, extraAttrs coretesting.Attrs) {
	s.mu.Lock()
	defer s.mu.Unlock()

	attrs := dummy.SampleConfig()
	for k, v := range extraAttrs {
		attrs[k] = v
	}

	// Simulate it's prepared.
	attrs["broken"] = ""
	attrs["state-id"] = "42"
	s.config = coretesting.CustomEnvironConfig(c, attrs).AllAttrs()
}

func (s *fakeState) SendNotify() {
	s.changes <- struct{}{}
}

func (s *fakeState) CloseNotify() {
	close(s.changes)
}

func (s *fakeState) AssertConfig(c *gc.C, expected *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c.Assert(s.config, jc.DeepEquals, expected.AllAttrs())
}

type fakeWatcher struct {
	tomb    tomb.Tomb
	changes watcher.NotifyChan
	err     error
}

func (w *fakeWatcher) Kill() {
	w.tomb.Kill(w.err)
}

func (w *fakeWatcher) Wait() error {
	return w.tomb.Wait()
}

func stopWorker(c *gc.C, w worker.Worker) {
	err := worker.Stop(w)
	c.Check(err, jc.ErrorIsNil)
}
