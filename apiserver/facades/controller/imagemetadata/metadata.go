// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package imagemetadata

import (
	"github.com/juju/loggo"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/common/imagecommon"
	"github.com/juju/juju/apiserver/facade"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/stateenvirons"
)

var logger = loggo.GetLogger("juju.apiserver.imagemetadata")

// API is the concrete implementation of the api end point
// for loud image metadata manipulations.
type API struct {
	metadata   metadataAccess
	newEnviron func() (environs.Environ, error)
}

// createAPI returns a new image metadata API facade.
func createAPI(
	st metadataAccess,
	newEnviron func() (environs.Environ, error),
	resources facade.Resources,
	authorizer facade.Authorizer,
) (*API, error) {
	if !authorizer.AuthController() {
		return nil, common.ErrPerm
	}

	return &API{
		metadata:   st,
		newEnviron: newEnviron,
	}, nil
}

// NewAPI returns a new cloud image metadata API facade.
func NewAPI(
	st *state.State,
	resources facade.Resources,
	authorizer facade.Authorizer,
) (*API, error) {
	newEnviron := func() (environs.Environ, error) {
		return stateenvirons.GetNewEnvironFunc(environs.New)(st)
	}
	return createAPI(getState(st), newEnviron, resources, authorizer)
}

// UpdateFromPublishedImages retrieves currently published image metadata and
// updates stored ones accordingly.
func (api *API) UpdateFromPublishedImages() error {
	updater := imagecommon.NewUpdaterFromPublished(api.newEnviron, logger, api.metadata)
	return updater.Update()
}

