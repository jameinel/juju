// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package workertest

import (
	"launchpad.net/tomb"
)

// NewErrorWorker returns a Worker that runs until Kill()ed; at which point it
// fails with the supplied error. The caller takes responsibility for causing
// it to be Kill()ed, lest the goroutine be leaked, but the worker has no
// outside interactions or safety concerns so there's no particular need to
// Wait() for it.
func NewErrorWorker(err error) *errorWorker {
	w := &errorWorker{err: err}
	go func() {
		defer w.tomb.Done()
		<-w.tomb.Dying()
	}()
	return w
}

type errorWorker struct {
	tomb tomb.Tomb
	err  error
}

// Kill is part of the worker.Worker interface.
func (w *errorWorker) Kill() {
	w.tomb.Kill(w.err)
}

// Wait is part of the worker.Worker interface.
func (w *errorWorker) Wait() error {
	return w.tomb.Wait()
}
