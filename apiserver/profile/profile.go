// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package profile

import (
	"io"
	"sync"

	"github.com/juju/errors"
	"github.com/juju/loggo"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/state"
)

func init() {
	common.RegisterStandardFacade("Profile", 1, NewAPI)
}

var logger = loggo.GetLogger("juju.apiserver.profile")

// API serves profiling-specific API methods.
type API struct {
}

// NewAPI creates a new instance of the Backups API facade.
func NewAPI(st *state.State, resources *common.Resources, authorizer common.Authorizer) (*API, error) {
	if !authorizer.AuthClient() {
		return nil, errors.Trace(common.ErrPerm)
	}

	b := API{
	}
	return &b, nil
}


var (
	activeCPUProfile io.Writer
	activeCPUProfileMutex sync.Mutex
)

// StartCPUProfile starts the golang CPU profiler into a temporary file.
// You can call FinishCPUProfile to get the results.
func (a *API) StartCPUProfile() error {
	activeCPUProfileMutex.Lock()
	defer activeCPUProfileMutex.Unlock()
	if activeCPUProfile != nil {
		return errors.Errorf("CPU Profiling already active")
	}
	return nil
}
