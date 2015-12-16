// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package networker_test

import (
	"runtime"
	"sort"

	"github.com/juju/names"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/api"
	"github.com/juju/juju/api/networker"
	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/juju/testing"
	"github.com/juju/juju/network"
	"github.com/juju/juju/state"
)

type networkerSuite struct {
	testing.JujuConnSuite

	subnets []state.SubnetInfo

	machine         *state.Machine
	container       *state.Machine
	nestedContainer *state.Machine

	machineIfaces   []state.NetworkInterfaceInfo
	containerIfaces []state.NetworkInterfaceInfo
	nestedIfaces    []state.NetworkInterfaceInfo

	st        api.Connection
	networker networker.State
}

var _ = gc.Suite(&networkerSuite{})

// Create several subnets.
func (s *networkerSuite) setUpSubnets(c *gc.C) {
	s.subnets = []state.SubnetInfo{{
		ProviderId: "net1",
		CIDR:       "0.1.2.0/24",
		VLANTag:    0,
	}, {
		ProviderId: "vlan42",
		CIDR:       "0.2.2.0/24",
		VLANTag:    42,
	}, {
		ProviderId: "vlan69",
		CIDR:       "0.3.2.0/24",
		VLANTag:    69,
	}, {
		ProviderId: "vlan123",
		CIDR:       "0.4.2.0/24",
		VLANTag:    123,
	}, {
		ProviderId: "net2",
		CIDR:       "0.5.2.0/24",
		VLANTag:    0,
	}}
	for _, info := range s.subnets {
		_, err := s.State.AddSubnet(info)
		c.Check(err, jc.ErrorIsNil)
	}
}

// Create a machine and login to it.
func (s *networkerSuite) setUpMachine(c *gc.C) {
	var err error
	s.machine, err = s.State.AddMachine("quantal", state.JobHostUnits)
	c.Assert(err, jc.ErrorIsNil)
	password, err := utils.RandomPassword()
	c.Assert(err, jc.ErrorIsNil)
	err = s.machine.SetPassword(password)
	c.Assert(err, jc.ErrorIsNil)
	hwChars := instance.MustParseHardware("cpu-cores=123", "mem=4G")
	s.machineIfaces = []state.NetworkInterfaceInfo{{
		MACAddress: "aa:bb:cc:dd:ee:f0",
		DeviceName: "eth0",
		SubnetID:   "0.1.2.0/24",
		IsVirtual:  false,
	}, {
		MACAddress: "aa:bb:cc:dd:ee:f1",
		DeviceName: "eth1",
		SubnetID:   "0.1.2.0/24",
		IsVirtual:  false,
	}, {
		MACAddress: "aa:bb:cc:dd:ee:f1",
		DeviceName: "eth1.42",
		SubnetID:   "0.2.2.0/24",
		IsVirtual:  true,
	}, {
		MACAddress: "aa:bb:cc:dd:ee:f0",
		DeviceName: "eth0.69",
		SubnetID:   "0.3.2.0/24",
		IsVirtual:  true,
	}, {
		MACAddress: "aa:bb:cc:dd:ee:f2",
		DeviceName: "eth2",
		SubnetID:   "0.5.2.0/24",
		IsVirtual:  false,
	}}
	err = s.machine.SetInstanceInfo("i-am", "fake_nonce", &hwChars, nil, s.machineIfaces, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	s.st = s.OpenAPIAsMachine(c, s.machine.Tag(), password, "fake_nonce")
	c.Assert(s.st, gc.NotNil)
}

// Create and provision a container and a nested container.
func (s *networkerSuite) setUpContainers(c *gc.C) {
	template := state.MachineTemplate{
		Series: "quantal",
		Jobs:   []state.MachineJob{state.JobHostUnits},
	}
	var err error
	s.container, err = s.State.AddMachineInsideMachine(template, s.machine.Id(), instance.LXC)
	c.Assert(err, jc.ErrorIsNil)
	s.containerIfaces = []state.NetworkInterfaceInfo{{
		MACAddress: "aa:bb:cc:dd:ee:e0",
		DeviceName: "eth0",
		SubnetID:   "0.1.2.0/24",
		IsVirtual:  false,
	}, {
		MACAddress: "aa:bb:cc:dd:ee:e1",
		DeviceName: "eth1",
		SubnetID:   "0.1.2.0/24",
		IsVirtual:  false,
	}, {
		MACAddress: "aa:bb:cc:dd:ee:e1",
		DeviceName: "eth1.42",
		SubnetID:   "0.2.2.0/24",
		IsVirtual:  true,
	}}
	hwChars := instance.MustParseHardware("arch=i386", "mem=4G")
	err = s.container.SetInstanceInfo("i-container", "fake_nonce", &hwChars, nil, s.containerIfaces, nil, nil)
	c.Assert(err, jc.ErrorIsNil)

	s.nestedContainer, err = s.State.AddMachineInsideMachine(template, s.container.Id(), instance.LXC)
	c.Assert(err, jc.ErrorIsNil)
	s.nestedIfaces = []state.NetworkInterfaceInfo{{
		MACAddress: "aa:bb:cc:dd:ee:d0",
		DeviceName: "eth0",
		SubnetID:   "0.1.2.0/24",
		IsVirtual:  false,
	}}
	err = s.nestedContainer.SetInstanceInfo("i-too", "fake_nonce", &hwChars, nil, s.nestedIfaces, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
}

func (s *networkerSuite) SetUpTest(c *gc.C) {
	s.JujuConnSuite.SetUpTest(c)

	s.setUpSubnets(c)
	s.setUpMachine(c)
	s.setUpContainers(c)

	// Create the networker API facade.
	s.networker = s.st.Networker()
	c.Assert(s.networker, gc.NotNil)
}

func (s *networkerSuite) TestMachineNetworkConfigPermissionDenied(c *gc.C) {
	info, err := s.networker.MachineNetworkConfig(names.NewMachineTag("1"))
	c.Assert(err, gc.ErrorMatches, "permission denied")
	c.Assert(err, jc.Satisfies, params.IsCodeUnauthorized)
	c.Assert(info, gc.IsNil)
}

func (s *networkerSuite) TestMachineNetworkConfigNameChange(c *gc.C) {
	var called bool
	networker.PatchFacadeCall(s, s.networker, func(request string, args, response interface{}) error {
		if !called {
			called = true
			c.Assert(request, gc.Equals, "MachineNetworkConfig")
			return &params.Error{
				Message: "MachineNetworkConfig",
				Code:    params.CodeNotImplemented,
			}
		}
		c.Assert(request, gc.Equals, "MachineNetworkInfo")
		expected := params.Entities{
			Entities: []params.Entity{{Tag: names.NewMachineTag("42").String()}},
		}
		c.Assert(args, gc.DeepEquals, expected)
		result := response.(*params.MachineNetworkConfigResults)
		result.Results = make([]params.MachineNetworkConfigResult, 1)
		result.Results[0].Error = common.ServerError(common.ErrPerm)
		return nil
	})
	// Make a call, in this case result is "permission denied".
	info, err := s.networker.MachineNetworkConfig(names.NewMachineTag("42"))
	c.Assert(err, gc.ErrorMatches, "permission denied")
	c.Assert(err, jc.Satisfies, params.IsCodeUnauthorized)
	c.Assert(info, gc.IsNil)
}

type orderedIfc []network.InterfaceInfo

func (o orderedIfc) Len() int {
	return len(o)
}

func (o orderedIfc) Less(i, j int) bool {
	if o[i].MACAddress < o[j].MACAddress {
		return true
	}
	if o[i].MACAddress > o[j].MACAddress {
		return false
	}
	if o[i].CIDR < o[j].CIDR {
		return true
	}
	if o[i].CIDR > o[j].CIDR {
		return false
	}
	return o[i].VLANTag < o[j].VLANTag
}

func (o orderedIfc) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

func (s *networkerSuite) TestMachineNetworkConfig(c *gc.C) {
	// TODO(bogdanteleaga): Find out what's the problem with this test
	// It seems to work on some machines
	if runtime.GOOS == "windows" {
		c.Skip("bug 1403084: currently does not work on windows")
	}
	// Expected results of MachineNetworkInfo for a machine and containers
	expectedMachineInfo := []network.InterfaceInfo{{
		MACAddress:    "aa:bb:cc:dd:ee:f0",
		CIDR:          "0.1.2.0/24",
		VLANTag:       0,
		InterfaceName: "eth0",
		NetworkName:   "juju-public",
	}, {
		MACAddress:    "aa:bb:cc:dd:ee:f1",
		CIDR:          "0.1.2.0/24",
		VLANTag:       0,
		InterfaceName: "eth1",
		NetworkName:   "juju-public",
	}, {
		MACAddress:    "aa:bb:cc:dd:ee:f1",
		CIDR:          "0.2.2.0/24",
		VLANTag:       42,
		InterfaceName: "eth1.42",
		NetworkName:   "juju-public",
	}, {
		MACAddress:    "aa:bb:cc:dd:ee:f0",
		CIDR:          "0.3.2.0/24",
		VLANTag:       69,
		InterfaceName: "eth0.69",
		NetworkName:   "juju-public",
	}, {
		MACAddress:    "aa:bb:cc:dd:ee:f2",
		CIDR:          "0.5.2.0/24",
		VLANTag:       0,
		InterfaceName: "eth2",
		NetworkName:   "juju-public",
	}}
	sort.Sort(orderedIfc(expectedMachineInfo))

	expectedContainerInfo := []network.InterfaceInfo{{
		MACAddress:    "aa:bb:cc:dd:ee:e0",
		CIDR:          "0.1.2.0/24",
		VLANTag:       0,
		InterfaceName: "eth0",
		NetworkName:   "juju-public",
	}, {
		MACAddress:    "aa:bb:cc:dd:ee:e1",
		CIDR:          "0.1.2.0/24",
		VLANTag:       0,
		InterfaceName: "eth1",
		NetworkName:   "juju-public",
	}, {
		MACAddress:    "aa:bb:cc:dd:ee:e1",
		CIDR:          "0.2.2.0/24",
		VLANTag:       42,
		InterfaceName: "eth1.42",
		NetworkName:   "juju-public",
	}}
	sort.Sort(orderedIfc(expectedContainerInfo))

	expectedNestedContainerInfo := []network.InterfaceInfo{{
		MACAddress:    "aa:bb:cc:dd:ee:d0",
		CIDR:          "0.1.2.0/24",
		VLANTag:       0,
		InterfaceName: "eth0",
		NetworkName:   "juju-public",
	}}
	sort.Sort(orderedIfc(expectedNestedContainerInfo))

	results, err := s.networker.MachineNetworkConfig(names.NewMachineTag("0"))
	c.Assert(err, jc.ErrorIsNil)
	sort.Sort(orderedIfc(results))
	c.Assert(results, jc.DeepEquals, expectedMachineInfo)

	results, err = s.networker.MachineNetworkConfig(names.NewMachineTag("0/lxc/0"))
	c.Assert(err, jc.ErrorIsNil)
	sort.Sort(orderedIfc(results))
	c.Assert(results, jc.DeepEquals, expectedContainerInfo)

	results, err = s.networker.MachineNetworkConfig(names.NewMachineTag("0/lxc/0/lxc/0"))
	c.Assert(err, jc.ErrorIsNil)
	sort.Sort(orderedIfc(results))
	c.Assert(results, jc.DeepEquals, expectedNestedContainerInfo)
}

func (s *networkerSuite) TestWatchInterfacesPermissionDenied(c *gc.C) {
	w, err := s.networker.WatchInterfaces(names.NewMachineTag("1"))
	c.Assert(err, gc.ErrorMatches, "permission denied")
	c.Assert(err, jc.Satisfies, params.IsCodeUnauthorized)
	c.Assert(w, gc.IsNil)
}
