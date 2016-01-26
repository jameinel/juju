// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state_test

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/state"
)

type NetworkInterfaceSuite struct {
	ConnSuite
	machine *state.Machine
	iface1  *state.NetworkInterface
	iface2  *state.NetworkInterface
}

var _ = gc.Suite(&NetworkInterfaceSuite{})

func (s *NetworkInterfaceSuite) SetUpTest(c *gc.C) {
	s.ConnSuite.SetUpTest(c)
	var err error
	s.machine, err = s.State.AddMachine("quantal", state.JobHostUnits)
	s.iface1, err = s.machine.AddNetworkInterface(state.NetworkInterfaceInfo{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		InterfaceName: "eth0",
	})
	c.Assert(err, jc.ErrorIsNil)
	s.iface2, err = s.machine.AddNetworkInterface(state.NetworkInterfaceInfo{
		MACAddress:    "aa:bb:cc:dd:ee:ff",
		InterfaceName: "eth0.42",
	})
	c.Assert(err, jc.ErrorIsNil)
}

func (s *NetworkInterfaceSuite) TestGetterMethods(c *gc.C) {
	c.Assert(s.iface1.Id(), gc.Not(gc.Equals), "")
	c.Assert(s.iface1.MACAddress(), gc.Equals, "aa:bb:cc:dd:ee:ff")
	c.Assert(s.iface1.InterfaceName(), gc.Equals, "eth0")
	c.Assert(s.iface1.MachineId(), gc.Equals, s.machine.Id())
	c.Assert(s.iface1.MachineTag(), gc.Equals, s.machine.Tag())
	c.Assert(s.iface1.IsDisabled(), jc.IsFalse)

	c.Assert(s.iface2.MACAddress(), gc.Equals, "aa:bb:cc:dd:ee:ff")
	c.Assert(s.iface2.InterfaceName(), gc.Equals, "eth0.42")
	c.Assert(s.iface2.IsDisabled(), jc.IsFalse)
}
