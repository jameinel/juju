// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package profile

import (
	"github.com/juju/errors"

	"github.com/juju/juju/api/base"
	"github.com/juju/juju/apiserver/params"
)

// Client wraps the backups API for the client.
type Client struct {
	base.ClientFacade
	facade base.FacadeCaller
}

// NewClient returns a new backups API client.
func NewClient(st base.APICallCloser) *Client {
	frontend, backend := base.NewClientFacade(st, "Profile")
	return &Client{ClientFacade: frontend, facade: backend}
}

// StartCPUProfile sends a request to have the API server start CPU profiling
func (c *Client) StartCPUProfile() error {
	if c.facade.BestAPIVersion() <= 0 {
		return errors.NotImplementedf("API server is not new enough")
	}
	if err := c.facade.FacadeCall("StartCPUProfile", nil, nil); err != nil {
		return errors.Trace(err)
	}
	return nil
}

// StopCPUProfile sends a request to have the API server stop CPU profiling and
// return the profile results
func (c *Client) StopCPUProfile() ([]byte, error) {
	if c.facade.BestAPIVersion() <= 0 {
		return nil, errors.NotImplementedf("API server is not new enough")
	}
	var result params.ProfileResult
	if err := c.facade.FacadeCall("StopCPUProfile", nil, &result); err != nil {
		return nil, errors.Trace(err)
	}
	return result.Profile, nil
}
