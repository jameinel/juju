// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package rackspace

import (
	"io/ioutil"
	"os"
	"time"

	"github.com/juju/errors"

	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/config"
	"github.com/juju/juju/provider/common"
	jujuos "github.com/juju/utils/os"
	"github.com/juju/utils/ssh"
)

type environ struct {
	environs.Environ
}

var bootstrap = common.Bootstrap

// Bootstrap implements environs.Environ.
func (e environ) Bootstrap(ctx environs.BootstrapContext, params environs.BootstrapParams) (*environs.BootstrapResult, error) {
	// can't redirect to openstack provider as ussually, because correct environ should be passed for common.Bootstrap
	return bootstrap(ctx, e, params)
}

// BootstrapMessage is part of the Environ interface.
func (e environ) BootstrapMessage() string {
	return ""
}

var waitSSH = common.WaitSSH

// StartInstance implements environs.Environ.
func (e environ) StartInstance(args environs.StartInstanceParams) (*environs.StartInstanceResult, error) {
	fwmode := e.Config().FirewallMode()
	if args.InstanceConfig.OSType == jujuos.Windows && fwmode != config.FwNone {
		return nil, errors.Errorf("rackspace provider doesn't support firewalls for windows instances")

	}
	r, err := e.Environ.StartInstance(args)
	if err != nil {
		return nil, errors.Trace(err)
	}
	if fwmode != config.FwNone {
		interrupted := make(chan os.Signal, 1)
		timeout := environs.BootstrapDialOpts{
			Timeout:        time.Minute * 5,
			RetryDelay:     time.Second * 5,
			AddressesDelay: time.Second * 20,
		}
		addr, err := waitSSH(ioutil.Discard, interrupted, ssh.DefaultClient, common.GetCheckNonceCommand(args.InstanceConfig), &common.RefreshableInstance{r.Instance, e}, timeout)
		if err != nil {
			return nil, errors.Trace(err)
		}
		client := newInstanceConfigurator(addr)
		apiPort := 0
		if args.InstanceConfig.Controller != nil {
			apiPort = args.InstanceConfig.Controller.Config.APIPort()
		}
		err = client.DropAllPorts([]int{apiPort, 22}, addr)
		if err != nil {
			return nil, errors.Trace(err)
		}
	}
	return r, nil
}

var newInstanceConfigurator = common.NewSshInstanceConfigurator

// Provider implements environs.Environ.
func (e environ) Provider() environs.EnvironProvider {
	return providerInstance
}
