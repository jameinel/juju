// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package profile

import (
	"bytes"
	"runtime/pprof"
	"sync"

	"github.com/juju/errors"
	"github.com/juju/loggo"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
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

	b := API{}
	return &b, nil
}

var (
	// TODO: We probably don't want to buffer all of this in memory, but
	// create a temporary file on disk to buffer it until we are done with
	// it
	activeCPUProfile      *bytes.Buffer
	activeCPUProfileMutex sync.Mutex
	startCPUProfile       = pprof.StartCPUProfile
	stopCPUProfile        = pprof.StopCPUProfile
)

// StartCPUProfile starts the golang CPU profiler into a temporary file.
// You can call FinishCPUProfile to get the results.
func (a *API) StartCPUProfile() error {
	activeCPUProfileMutex.Lock()
	defer activeCPUProfileMutex.Unlock()
	if activeCPUProfile != nil {
		return errors.Errorf("CPU profiling already active")
	}
	// TODO: This should really be spooled to a local file
	newBuff := &bytes.Buffer{}
	if err := startCPUProfile(newBuff); err == nil {
		activeCPUProfile = newBuff
		return nil
	} else {
		return err
	}

}

// StopCPUProfile finishes the CPU profile and dumps the results to the user.
func (a *API) StopCPUProfile() (params.ProfileResult, error) {
	activeCPUProfileMutex.Lock()
	defer activeCPUProfileMutex.Unlock()
	if activeCPUProfile == nil {
		return params.ProfileResult{}, errors.Errorf("CPU profiling not active")
	}
	// stopCPUProfile doesn't return a value
	stopCPUProfile()
	result := params.ProfileResult{Profile: activeCPUProfile.String()}
	activeCPUProfile = nil
	return result, nil
}
