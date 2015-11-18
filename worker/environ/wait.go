// Copyright 2012-2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package environ

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"

	"github.com/juju/juju/environs"
	"github.com/juju/juju/watcher"
)

// TODO(fwereade) remove WaitForEnviron, use a manifold-managed Tracker to share
// a single environs.Environ among firewaller, instancepoller, provisioner.

var logger = loggo.GetLogger("juju.worker.environ")

// loadedInvalid is exposed via export_test.go so that tests can be informed of
// WaitForEnviron's progress through the loop. This is a Bad Thing. Desired
// behaviour should be verifiable via the exported interface; this may sometimes
// mean passing things in "just for tests", but the benefits of knowing you're
// using the exact same code at test- and at runtime outweight any inelegance.
var loadedInvalid = func() {}

// ErrWaitAborted is returned from WaitForEnviron when the wait is terminated by
// closing the abort chan.
var ErrWaitAborted = errors.New("environ wait aborted")

// WaitForEnviron waits for an valid environment to arrive from the given
// watcher. It terminates with ErrWaitAborted if it receives a value on abort.
//
// In practice, it shouldn't wait at all: juju *should* never deliver invalid
// environ configs. Regardless, it should be considered deprecated; clients
// should prefer to access an Environ via a shared Tracker.
//
// It never takes responsibility for the supplied watcher; the client remains
// responsible for detecting and handling any watcher errors that may occur,
// whether this func succeeds or fails.
func WaitForEnviron(w watcher.NotifyWatcher, getter ConfigGetter, abort <-chan struct{}) (environs.Environ, error) {
	for {
		select {
		case <-abort:
			return nil, ErrWaitAborted
		case _, ok := <-w.Changes():
			if !ok {
				return nil, errors.New("watcher closed channel")
			}
			config, err := getter.EnvironConfig()
			if err != nil {
				return nil, errors.Trace(err)
			}
			environ, err := environs.New(config)
			if err == nil {
				return environ, nil
			}
			logger.Errorf("loaded invalid environment configuration: %v", err)
			loadedInvalid()
		}
	}
}
