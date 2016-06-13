// Copyright 2015 Canonical Ltd.
// Copyright 2015 Cloudbase Solutions SRL
// Licensed under the AGPLv3, see LICENCE file for details.

package containerinit_test

import (
	"fmt"
	"path/filepath"
	"strings"
	stdtesting "testing"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/yaml.v2"

	"github.com/juju/juju/cloudconfig/cloudinit"
	"github.com/juju/juju/cloudconfig/containerinit"
	"github.com/juju/juju/container"
	containertesting "github.com/juju/juju/container/testing"
	"github.com/juju/juju/network"
	"github.com/juju/juju/testing"
)

func Test(t *stdtesting.T) {
	gc.TestingT(t)
}

type UserDataSuite struct {
	testing.BaseSuite

	networkInterfacesFile string
	fakeInterfaces        []network.InterfaceInfo
	expectedNetConfig     string
}

var _ = gc.Suite(&UserDataSuite{})

func (s *UserDataSuite) SetUpTest(c *gc.C) {
	s.BaseSuite.SetUpTest(c)
	s.networkInterfacesFile = filepath.Join(c.MkDir(), "interfaces")
	s.fakeInterfaces = []network.InterfaceInfo{{
		InterfaceName:    "eth0",
		CIDR:             "0.1.2.0/24",
		ConfigType:       network.ConfigStatic,
		NoAutoStart:      false,
		Address:          network.NewAddress("0.1.2.3"),
		DNSServers:       network.NewAddresses("ns1.invalid", "ns2.invalid"),
		DNSSearchDomains: []string{"foo", "bar"},
		GatewayAddress:   network.NewAddress("0.1.2.1"),
		MACAddress:       "aa:bb:cc:dd:ee:f0",
	}, {
		InterfaceName:    "eth1",
		CIDR:             "0.1.2.0/24",
		ConfigType:       network.ConfigStatic,
		NoAutoStart:      false,
		Address:          network.NewAddress("0.1.2.4"),
		DNSServers:       network.NewAddresses("ns1.invalid", "ns2.invalid"),
		DNSSearchDomains: []string{"foo", "bar"},
		GatewayAddress:   network.NewAddress("0.1.2.1"),
		MACAddress:       "aa:bb:cc:dd:ee:f0",
	}, {
		InterfaceName: "eth2",
		ConfigType:    network.ConfigDHCP,
		NoAutoStart:   true,
	}}
	s.expectedNetConfig = `
auto eth0 eth1 lo

iface lo inet loopback
  dns-nameservers ns1.invalid ns2.invalid
  dns-search bar foo

iface eth0 inet static
  address 0.1.2.3/24
  gateway 0.1.2.1

iface eth1 inet static
  address 0.1.2.4/24

iface eth2 inet manual
`
	s.PatchValue(containerinit.NetworkInterfacesFile, s.networkInterfacesFile)
}

func (s *UserDataSuite) TestGenerateNetworkConfig(c *gc.C) {
	// No config or no interfaces - no error, but also noting to generate.
	data, err := containerinit.GenerateNetworkConfig(nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(data, gc.HasLen, 0)
	netConfig := container.BridgeNetworkConfig("foo", 0, nil)
	data, err = containerinit.GenerateNetworkConfig(netConfig)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(data, gc.HasLen, 0)

	// Test with all interface types.
	netConfig = container.BridgeNetworkConfig("foo", 0, s.fakeInterfaces)
	data, err = containerinit.GenerateNetworkConfig(netConfig)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(data, gc.Equals, s.expectedNetConfig)
}

func (s *UserDataSuite) TestNewCloudInitConfigWithNetworks(c *gc.C) {
	netConfig := container.BridgeNetworkConfig("foo", 0, s.fakeInterfaces)
	cloudConf, err := containerinit.NewCloudInitConfigWithNetworks("quantal", netConfig)
	c.Assert(err, jc.ErrorIsNil)
	// We need to indent expectNetConfig to make it valid YAML,
	// dropping the last new line and using unindented blank lines.
	lines := strings.Split(s.expectedNetConfig, "\n")
	indentedNetConfig := strings.Join(lines[:len(lines)-2], "\n  ")
	indentedNetConfig = strings.Replace(indentedNetConfig, "\n  \n", "\n\n", -1)
	expected := `
#cloud-config
bootcmd:
- install -D -m 644 /dev/null '%[1]s'
- |-
  printf '%%s\n' '
  auto eth0 eth1 lo

  iface lo inet loopback
    dns-nameservers ns1.invalid ns2.invalid
    dns-search bar foo

  iface eth0 inet static
    address 0.1.2.3/24
    gateway 0.1.2.1

  iface eth1 inet static
    address 0.1.2.4/24

  iface eth2 inet manual
  ' > '%[1]s'
runcmd:
- ifup -a || true
`[1:]
	assertUserData(c, cloudConf, fmt.Sprintf(expected, s.networkInterfacesFile))
}

func (s *UserDataSuite) TestNewCloudInitConfigWithNetworksNoConfig(c *gc.C) {
	netConfig := container.BridgeNetworkConfig("foo", 0, nil)
	cloudConf, err := containerinit.NewCloudInitConfigWithNetworks("quantal", netConfig)
	c.Assert(err, jc.ErrorIsNil)
	expected := "#cloud-config\n{}\n"
	assertUserData(c, cloudConf, expected)
}

func (s *UserDataSuite) TestCloudInitUserData(c *gc.C) {
	instanceConfig, err := containertesting.MockMachineConfig("1/lxc/0")
	c.Assert(err, jc.ErrorIsNil)
	networkConfig := container.BridgeNetworkConfig("foo", 0, nil)
	data, err := containerinit.CloudInitUserData(instanceConfig, networkConfig)
	c.Assert(err, jc.ErrorIsNil)
	// No need to test the exact contents here, as they are already
	// tested separately.
	c.Assert(string(data), jc.HasPrefix, "#cloud-config\n")
}

func assertUserData(c *gc.C, cloudConf cloudinit.CloudConfig, expected string) {
	data, err := cloudConf.RenderYAML()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(string(data), gc.Equals, expected)
	// Make sure it's valid YAML as well.
	out := make(map[string]interface{})
	err = yaml.Unmarshal(data, &out)
	c.Assert(err, jc.ErrorIsNil)
	if len(cloudConf.BootCmds()) > 0 {
		outcmds := out["bootcmd"].([]interface{})
		confcmds := cloudConf.BootCmds()
		c.Assert(len(outcmds), gc.Equals, len(confcmds))
		for i, _ := range outcmds {
			c.Assert(outcmds[i].(string), gc.Equals, confcmds[i])
		}
	} else {
		c.Assert(out["bootcmd"], gc.IsNil)
	}
}
