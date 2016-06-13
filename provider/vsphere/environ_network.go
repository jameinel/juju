// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// +build !gccgo

package vsphere

import (
	"github.com/juju/errors"
	"gopkg.in/juju/names.v2"

	"github.com/juju/juju/instance"
	"github.com/juju/juju/network"
)

// SupportsSpaces is specified on environs.Networking.
func (env *environ) SupportsSpaces() (bool, error) {
	return false, errors.NotSupportedf("spaces")
}

// Subnets implements environs.Environ.
func (env *environ) Subnets(inst instance.Id, ids []network.Id) ([]network.SubnetInfo, error) {
	return env.client.Subnets(inst, ids)
}

// NetworkInterfaces implements environs.Environ.
func (env *environ) NetworkInterfaces(inst instance.Id) ([]network.InterfaceInfo, error) {
	return env.client.GetNetworkInterfaces(inst, env.ecfg)
}

// OpenPorts opens the given port ranges for the whole environment.
// Must only be used if the environment was setup with the
// FwGlobal firewall mode.
func (env *environ) OpenPorts(ports []network.PortRange) error {
	return errors.Trace(errors.NotSupportedf("ClosePorts"))
}

// ClosePorts closes the given port ranges for the whole environment.
// Must only be used if the environment was setup with the
// FwGlobal firewall mode.
func (env *environ) ClosePorts(ports []network.PortRange) error {
	return errors.Trace(errors.NotSupportedf("ClosePorts"))
}

// Ports returns the port ranges opened for the whole environment.
// Must only be used if the environment was setup with the
// FwGlobal firewall mode.
func (env *environ) Ports() ([]network.PortRange, error) {
	return nil, errors.Trace(errors.NotSupportedf("Ports"))
}

func (e *environ) AllocateContainerAddresses(hostInstanceID instance.Id, containerTag names.MachineTag, preparedInfo []network.InterfaceInfo) ([]network.InterfaceInfo, error) {
	return nil, errors.NotSupportedf("container address allocation")
}
