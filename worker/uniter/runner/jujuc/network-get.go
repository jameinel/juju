// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package jujuc

import (
	"fmt"
	"strings"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/gnuflag"
)

// NetworkGetCommand implements the network-get command.
type NetworkGetCommand struct {
	cmd.CommandBase
	ctx Context

	bindingName     string
	primaryAddress  bool
	primaryHostname bool

	out cmd.Output
}

func NewNetworkGetCommand(ctx Context) (cmd.Command, error) {
	cmd := &NetworkGetCommand{ctx: ctx}
	return cmd, nil
}

// Info is part of the cmd.Command interface.
func (c *NetworkGetCommand) Info() *cmd.Info {
	args := "<binding-name> --primary-address|--primary-hostname"
	doc := `
network-get returns the network config for a given binding name. The only
supported flags for now are --primary-address or --primary-hostname.
One or the other must be specified. The first one returns the IP address
the local unit should advertise as its endpoint to its peers. The second
one returns the same IP address but as a hostname with the format:
'juju-ip-10-20-30-40' assuming --primary-address returns 10.20.30.40.
Those hostnames are resolved by the NSS Plugin Juju installs on each machine.
`
	return &cmd.Info{
		Name:    "network-get",
		Args:    args,
		Purpose: "get network config",
		Doc:     doc,
	}
}

// SetFlags is part of the cmd.Command interface.
func (c *NetworkGetCommand) SetFlags(f *gnuflag.FlagSet) {
	c.out.AddFlags(f, "smart", cmd.DefaultFormatters)
	f.BoolVar(&c.primaryAddress, "primary-address", false, "get the primary address for the binding")
	f.BoolVar(&c.primaryHostname, "primary-hostname", false, "get the primary hostname for the binding")
}

// Init is part of the cmd.Command interface.
func (c *NetworkGetCommand) Init(args []string) error {

	if len(args) < 1 {
		return errors.New("no arguments specified")
	}
	c.bindingName = args[0]
	if c.bindingName == "" {
		return fmt.Errorf("no binding name specified")
	}

	if !c.primaryAddress && !c.primaryHostname {
		return fmt.Errorf("--primary-address or --primary-hostname are currently required")
	}
	if c.primaryAddress && c.primaryHostname {
		return fmt.Errorf("--primary-address and --primary-hostname are mutually exclusive")
	}

	return cmd.CheckEmpty(args[1:])
}

func addressToHostname(address string) string {
	hostname := fmt.Sprintf("juju-ip-%s", address)
	hostname = strings.Replace(hostname, ".", "-", -1)
	return hostname
}

func (c *NetworkGetCommand) Run(ctx *cmd.Context) error {
	netConfig, err := c.ctx.NetworkConfig(c.bindingName)
	if err != nil {
		return errors.Trace(err)
	}
	if len(netConfig) < 1 {
		return fmt.Errorf("no network config found for binding %q", c.bindingName)
	}

	if c.primaryAddress {
		return c.out.Write(ctx, netConfig[0].Address)
	}
	if c.primaryHostname {
		return c.out.Write(ctx, addressToHostname(netConfig[0].Address))
	}

	return nil // never reached as --primary-address or --primary-hostname are required.
}
