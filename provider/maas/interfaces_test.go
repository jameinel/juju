// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	"github.com/juju/gomaasapi"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/network"
)

////////////////////////////////////////////////////////////////////////////////
// New (1.9 and later) environs.NetworkInterfaces() implementation tests follow.

type interfacesSuite struct {
	providerSuite
}

var _ = gc.Suite(&interfacesSuite{})

func newAddressOnSpaceWithId(space string, id network.Id, address string) network.Address {
	newAddress := network.NewAddressOnSpace(space, address)
	newAddress.SpaceProviderId = id
	return newAddress
}

const exampleInterfaceSetJSON = `
[
	{
		"name": "eth0",
		"links": [
			{
				"subnet": {
					"dns_servers": ["10.20.19.2", "10.20.19.3"],
					"name": "pxe",
					"space": "default",
					"vlan": {
						"name": "untagged",
						"vid": 0,
						"mtu": 1500,
						"fabric": "managed",
						"id": 5001,
						"resource_uri": "/MAAS/api/1.0/vlans/5001/"
					},
					"gateway_ip": "10.20.19.2",
					"cidr": "10.20.19.0/24",
					"id": 3,
					"resource_uri": "/MAAS/api/1.0/subnets/3/"
				},
				"ip_address": "10.20.19.103",
				"id": 436,
				"mode": "static"
			},
			{
				"subnet": {
					"dns_servers": ["10.20.19.2", "10.20.19.3"],
					"name": "pxe",
					"space": "default",
					"vlan": {
						"name": "untagged",
						"vid": 0,
						"mtu": 1500,
						"fabric": "managed",
						"id": 5001,
						"resource_uri": "/MAAS/api/1.0/vlans/5001/"
					},
					"gateway_ip": "10.20.19.2",
					"cidr": "10.20.19.0/24",
					"id": 3,
					"resource_uri": "/MAAS/api/1.0/subnets/3/"
				},
				"ip_address": "10.20.19.104",
				"id": 437,
				"mode": "static"
			}
		],
		"tags": [],
		"vlan": {
			"name": "untagged",
			"vid": 0,
			"mtu": 1500,
			"fabric": "managed",
			"id": 5001,
			"resource_uri": "/MAAS/api/1.0/vlans/5001/"
		},
		"enabled": true,
		"id": 91,
		"discovered": [
			{
				"subnet": {
					"dns_servers": [],
					"name": "pxe",
					"space": "default",
					"vlan": {
						"name": "untagged",
						"vid": 0,
						"mtu": 1500,
						"fabric": "managed",
						"id": 5001,
						"resource_uri": "/MAAS/api/1.0/vlans/5001/"
					},
					"gateway_ip": "10.20.19.2",
					"cidr": "10.20.19.0/24",
					"id": 3,
					"resource_uri": "/MAAS/api/1.0/subnets/3/"
				},
				"ip_address": "10.20.19.20"
			}
		],
		"mac_address": "52:54:00:70:9b:fe",
		"parents": [],
		"effective_mtu": 1500,
		"params": {},
		"type": "physical",
		"children": [
			"eth0.100",
			"eth0.250",
			"eth0.50"
		],
		"resource_uri": "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/91/"
	},
	{
		"name": "eth0.50",
		"links": [
			{
				"subnet": {
					"dns_servers": [],
					"name": "admin",
					"space": "admin",
					"vlan": {
						"name": "admin",
						"vid": 50,
						"mtu": 1500,
						"fabric": "managed",
						"id": 5004,
						"resource_uri": "/MAAS/api/1.0/vlans/5004/"
					},
					"gateway_ip": "10.50.19.2",
					"cidr": "10.50.19.0/24",
					"id": 5,
					"resource_uri": "/MAAS/api/1.0/subnets/5/"
				},
				"ip_address": "10.50.19.103",
				"id": 517,
				"mode": "static"
			}
		],
		"tags": [],
		"vlan": {
			"name": "admin",
			"vid": 50,
			"mtu": 1500,
			"fabric": "managed",
			"id": 5004,
			"resource_uri": "/MAAS/api/1.0/vlans/5004/"
		},
		"enabled": true,
		"id": 150,
		"discovered": null,
		"mac_address": "52:54:00:70:9b:fe",
		"parents": [
			"eth0"
		],
		"effective_mtu": 1500,
		"params": {},
		"type": "vlan",
		"children": [],
		"resource_uri": "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/150/"
	},
	{
		"name": "eth0.100",
		"links": [
			{
				"subnet": {
					"dns_servers": [],
					"name": "public",
					"space": "public",
					"vlan": {
						"name": "public",
						"vid": 100,
						"mtu": 1500,
						"fabric": "managed",
						"id": 5005,
						"resource_uri": "/MAAS/api/1.0/vlans/5005/"
					},
					"gateway_ip": "10.100.19.2",
					"cidr": "10.100.19.0/24",
					"id": 6,
					"resource_uri": "/MAAS/api/1.0/subnets/6/"
				},
				"ip_address": "10.100.19.103",
				"id": 519,
				"mode": "static"
			}
		],
		"tags": [],
		"vlan": {
			"name": "public",
			"vid": 100,
			"mtu": 1500,
			"fabric": "managed",
			"id": 5005,
			"resource_uri": "/MAAS/api/1.0/vlans/5005/"
		},
		"enabled": true,
		"id": 151,
		"discovered": null,
		"mac_address": "52:54:00:70:9b:fe",
		"parents": [
			"eth0"
		],
		"effective_mtu": 1500,
		"params": {},
		"type": "vlan",
		"children": [],
		"resource_uri": "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/151/"
	},
	{
		"name": "eth0.250",
		"links": [
			{
				"subnet": {
					"dns_servers": [],
					"name": "storage",
					"space": "storage",
					"vlan": {
						"name": "storage",
						"vid": 250,
						"mtu": 1500,
						"fabric": "managed",
						"id": 5008,
						"resource_uri": "/MAAS/api/1.0/vlans/5008/"
					},
					"gateway_ip": "10.250.19.2",
					"cidr": "10.250.19.0/24",
					"id": 8,
					"resource_uri": "/MAAS/api/1.0/subnets/8/"
				},
				"ip_address": "10.250.19.103",
				"id": 523,
				"mode": "static"
			}
		],
		"tags": [],
		"vlan": {
			"name": "storage",
			"vid": 250,
			"mtu": 1500,
			"fabric": "managed",
			"id": 5008,
			"resource_uri": "/MAAS/api/1.0/vlans/5008/"
		},
		"enabled": true,
		"id": 152,
		"discovered": null,
		"mac_address": "52:54:00:70:9b:fe",
		"parents": [
			"eth0"
		],
		"effective_mtu": 1500,
		"params": {},
		"type": "vlan",
		"children": [],
		"resource_uri": "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/152/"
	}
]`

func (s *interfacesSuite) TestParseInterfacesNoJSON(c *gc.C) {
	result, err := parseInterfaces(nil)
	c.Check(err, gc.ErrorMatches, "parsing interfaces: unexpected end of JSON input")
	c.Check(result, gc.IsNil)
}

func (s *interfacesSuite) TestParseInterfacesBadJSON(c *gc.C) {
	result, err := parseInterfaces([]byte("$bad"))
	c.Check(err, gc.ErrorMatches, `parsing interfaces: invalid character '\$' .*`)
	c.Check(result, gc.IsNil)
}

func (s *interfacesSuite) TestParseInterfacesExampleJSON(c *gc.C) {

	vlan0 := maasVLAN{
		ID:          5001,
		Name:        "untagged",
		VID:         0,
		MTU:         1500,
		Fabric:      "managed",
		ResourceURI: "/MAAS/api/1.0/vlans/5001/",
	}

	vlan50 := maasVLAN{
		ID:          5004,
		Name:        "admin",
		VID:         50,
		MTU:         1500,
		Fabric:      "managed",
		ResourceURI: "/MAAS/api/1.0/vlans/5004/",
	}

	vlan100 := maasVLAN{
		ID:          5005,
		Name:        "public",
		VID:         100,
		MTU:         1500,
		Fabric:      "managed",
		ResourceURI: "/MAAS/api/1.0/vlans/5005/",
	}

	vlan250 := maasVLAN{
		ID:          5008,
		Name:        "storage",
		VID:         250,
		MTU:         1500,
		Fabric:      "managed",
		ResourceURI: "/MAAS/api/1.0/vlans/5008/",
	}

	subnetPXE := maasSubnet{
		ID:          3,
		Name:        "pxe",
		Space:       "default",
		VLAN:        vlan0,
		GatewayIP:   "10.20.19.2",
		DNSServers:  []string{"10.20.19.2", "10.20.19.3"},
		CIDR:        "10.20.19.0/24",
		ResourceURI: "/MAAS/api/1.0/subnets/3/",
	}

	expected := []maasInterface{{
		ID:          91,
		Name:        "eth0",
		Type:        "physical",
		Enabled:     true,
		MACAddress:  "52:54:00:70:9b:fe",
		VLAN:        vlan0,
		EffectveMTU: 1500,
		Links: []maasInterfaceLink{{
			ID:        436,
			Subnet:    &subnetPXE,
			IPAddress: "10.20.19.103",
			Mode:      "static",
		}, {
			ID:        437,
			Subnet:    &subnetPXE,
			IPAddress: "10.20.19.104",
			Mode:      "static",
		}},
		Parents:     []string{},
		Children:    []string{"eth0.100", "eth0.250", "eth0.50"},
		ResourceURI: "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/91/",
	}, {
		ID:          150,
		Name:        "eth0.50",
		Type:        "vlan",
		Enabled:     true,
		MACAddress:  "52:54:00:70:9b:fe",
		VLAN:        vlan50,
		EffectveMTU: 1500,
		Links: []maasInterfaceLink{{
			ID: 517,
			Subnet: &maasSubnet{
				ID:          5,
				Name:        "admin",
				Space:       "admin",
				VLAN:        vlan50,
				GatewayIP:   "10.50.19.2",
				DNSServers:  []string{},
				CIDR:        "10.50.19.0/24",
				ResourceURI: "/MAAS/api/1.0/subnets/5/",
			},
			IPAddress: "10.50.19.103",
			Mode:      "static",
		}},
		Parents:     []string{"eth0"},
		Children:    []string{},
		ResourceURI: "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/150/",
	}, {
		ID:          151,
		Name:        "eth0.100",
		Type:        "vlan",
		Enabled:     true,
		MACAddress:  "52:54:00:70:9b:fe",
		VLAN:        vlan100,
		EffectveMTU: 1500,
		Links: []maasInterfaceLink{{
			ID: 519,
			Subnet: &maasSubnet{
				ID:          6,
				Name:        "public",
				Space:       "public",
				VLAN:        vlan100,
				GatewayIP:   "10.100.19.2",
				DNSServers:  []string{},
				CIDR:        "10.100.19.0/24",
				ResourceURI: "/MAAS/api/1.0/subnets/6/",
			},
			IPAddress: "10.100.19.103",
			Mode:      "static",
		}},
		Parents:     []string{"eth0"},
		Children:    []string{},
		ResourceURI: "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/151/",
	}, {
		ID:          152,
		Name:        "eth0.250",
		Type:        "vlan",
		Enabled:     true,
		MACAddress:  "52:54:00:70:9b:fe",
		VLAN:        vlan250,
		EffectveMTU: 1500,
		Links: []maasInterfaceLink{{
			ID: 523,
			Subnet: &maasSubnet{
				ID:          8,
				Name:        "storage",
				Space:       "storage",
				VLAN:        vlan250,
				GatewayIP:   "10.250.19.2",
				DNSServers:  []string{},
				CIDR:        "10.250.19.0/24",
				ResourceURI: "/MAAS/api/1.0/subnets/8/",
			},
			IPAddress: "10.250.19.103",
			Mode:      "static",
		}},
		Parents:     []string{"eth0"},
		Children:    []string{},
		ResourceURI: "/MAAS/api/1.0/nodes/node-18489434-9eb0-11e5-bdef-00163e40c3b6/interfaces/152/",
	}}

	result, err := parseInterfaces([]byte(exampleInterfaceSetJSON))
	c.Check(err, jc.ErrorIsNil)
	c.Check(result, jc.DeepEquals, expected)
}

var (
	vlan50  = maasVLAN{VID: 50}
	vlan100 = maasVLAN{VID: 100}
	vlan200 = maasVLAN{VID: 200}
)

func (s *interfacesSuite) TestSortInterfacesByTypeThenNameNoBonds(c *gc.C) {
	input := []maasInterface{
		{Name: "eth1", Type: "physical"},
		{Name: "eth0", Type: "physical"},
		{Name: "eth3", Type: "physical"},
		{Name: "eth2", Type: "physical"},
		{Name: "eth0.50", Type: "vlan", Parents: []string{"eth0"}, VLAN: vlan50},
		{Name: "eth2.100", Type: "vlan", Parents: []string{"eth2"}, VLAN: vlan100},
		{Name: "eth1.200", Type: "vlan", Parents: []string{"eth1"}, VLAN: vlan200},
		{Name: "eth0.100", Type: "vlan", Parents: []string{"eth0"}, VLAN: vlan100},
	}
	expected := []maasInterface{
		{Name: "eth0", Type: "physical"},
		{Name: "eth1", Type: "physical"},
		{Name: "eth2", Type: "physical"},
		{Name: "eth3", Type: "physical"},
		{Name: "eth0.50", Type: "vlan", Parents: []string{"eth0"}, VLAN: vlan50},
		{Name: "eth0.100", Type: "vlan", Parents: []string{"eth0"}, VLAN: vlan100},
		{Name: "eth1.200", Type: "vlan", Parents: []string{"eth1"}, VLAN: vlan200},
		{Name: "eth2.100", Type: "vlan", Parents: []string{"eth2"}, VLAN: vlan100},
	}
	inputCopy := input
	ordered := &byTypeThenName{interfaces: input}
	sort.Sort(ordered)

	c.Check(ordered.interfaces, jc.DeepEquals, expected)
	// Input shouldn't be modified.
	c.Check(input, jc.DeepEquals, inputCopy)
}

func (s *interfacesSuite) TestSortInterfacesByTypeThenNameWithBonds(c *gc.C) {
	input := []maasInterface{
		{Name: "eth1", Type: "physical"},
		{Name: "eth0", Type: "physical"},
		{Name: "eth3", Type: "physical"},
		{Name: "eth2", Type: "physical"},
		{Name: "bond0", Type: "bond"},
		{Name: "bond0.50", Type: "vlan", Parents: []string{"bond0"}, VLAN: vlan50},
		{Name: "bond0.100", Type: "vlan", Parents: []string{"bond0"}, VLAN: vlan100},
		{Name: "bond1", Type: "bond"},
		{Name: "bond1.200", Type: "vlan", Parents: []string{"bond1"}, VLAN: vlan200},
		{Name: "bond1.100", Type: "vlan", Parents: []string{"bond1"}, VLAN: vlan100},
	}
	expected := []maasInterface{
		{Name: "bond0", Type: "bond"},
		{Name: "bond1", Type: "bond"},
		{Name: "eth0", Type: "physical"},
		{Name: "eth1", Type: "physical"},
		{Name: "eth2", Type: "physical"},
		{Name: "eth3", Type: "physical"},
		{Name: "bond0.50", Type: "vlan", Parents: []string{"bond0"}, VLAN: vlan50},
		{Name: "bond0.100", Type: "vlan", Parents: []string{"bond0"}, VLAN: vlan100},
		{Name: "bond1.100", Type: "vlan", Parents: []string{"bond1"}, VLAN: vlan100},
		{Name: "bond1.200", Type: "vlan", Parents: []string{"bond1"}, VLAN: vlan200},
	}
	inputCopy := input
	ordered := &byTypeThenName{interfaces: input}
	sort.Sort(ordered)

	c.Check(ordered.interfaces, jc.DeepEquals, expected)
	// Input shouldn't be modified.
	c.Check(input, jc.DeepEquals, inputCopy)
}

func (s *interfacesSuite) TestSortInterfaceInfoByPreferredAddresses(c *gc.C) {
	input := []network.InterfaceInfo{
		{Address: network.Address{}, InterfaceName: "eth1"},
		{Address: network.Address{}, InterfaceName: "eth0"},
		{Address: network.NewAddress("10.20.19.111"), InterfaceName: "bond1"},
		{Address: network.NewAddress("10.100.19.101"), InterfaceName: "bond0.100"},
		{Address: network.NewAddress("10.50.19.142"), InterfaceName: "bond0.50"},
		{Address: network.NewAddress("10.20.19.102"), InterfaceName: "bond0"},
	}
	inputCopy := input
	ordered := &byPreferedAddresses{
		addressesOrder: nil, // no preferred addresses ordering, no change expected.
		infos:          input,
	}
	sort.Sort(ordered)
	c.Check(ordered.infos, gc.DeepEquals, input)
	c.Check(inputCopy, gc.DeepEquals, input)

	ordered.addressesOrder = map[network.Address]int{
		network.NewAddress("10.20.19.102"):  0,
		network.NewAddress("10.20.19.111"):  1,
		network.NewAddress("10.50.19.142"):  22,
		network.NewAddress("10.100.19.101"): 99,
	}
	sort.Sort(ordered)
	c.Check(ordered.infos, gc.DeepEquals, []network.InterfaceInfo{
		{Address: network.NewAddress("10.20.19.102"), InterfaceName: "bond0"},
		{Address: network.NewAddress("10.20.19.111"), InterfaceName: "bond1"},
		{Address: network.NewAddress("10.50.19.142"), InterfaceName: "bond0.50"},
		{Address: network.NewAddress("10.100.19.101"), InterfaceName: "bond0.100"},
		{Address: network.Address{}, InterfaceName: "eth1"},
		{Address: network.Address{}, InterfaceName: "eth0"},
	})
	c.Check(inputCopy, gc.DeepEquals, input)
}

func (s *interfacesSuite) TestMAASObjectPXEMACAddressAndHostname(c *gc.C) {
	const (
		nodeJSONPrefix = `{"system_id": "foo"`

		noPXEMAC   = ""
		nullPXEMAC = `,"pxe_mac": null`
		badPXEMAC  = `,"pxe_mac": 42`

		noPXEMACAddress   = `,"pxe_mac": {}`
		nullPXEMACAddress = `,"pxe_mac": {"mac_address": null}`
		badPXEMACAddress  = `,"pxe_mac": {"mac_address": 42}`
		goodPXEMACAddress = `,"pxe_mac": {"mac_address": "mac"}`

		noHostname   = ""
		nullHostname = `,"hostname": null`
		badHostname  = `,"hostname": 42`
		goodHostname = `,"hostname": "node-xyz.maas"`

		errNoPXEMAC        = "missing or null pxe_mac field"
		errNoPXEMACAddress = `missing or null pxe_mac\.mac_address field`
		errNoHostname      = "missing or null hostname field"
		errBadTypeSuffix   = ": Requested .*, got float.*"
	)

	node := func(pxeMACJSON, hostnameJSON string) *gomaasapi.MAASObject {
		fullJSON := nodeJSONPrefix + pxeMACJSON + hostnameJSON + "}"
		nodeObject := s.testMAASObject.TestServer.NewNode(fullJSON)
		return &nodeObject
	}

	_, _, err := maasObjectPXEMACAddressAndHostname(node(noPXEMAC, noHostname))
	c.Check(err, gc.ErrorMatches, errNoPXEMAC)
	_, _, err = maasObjectPXEMACAddressAndHostname(node(nullPXEMAC, noHostname))
	c.Check(err, gc.ErrorMatches, errNoPXEMAC)

	_, _, err = maasObjectPXEMACAddressAndHostname(node(badPXEMAC, noHostname))
	c.Check(err, gc.ErrorMatches, "getting pxe_mac map failed"+errBadTypeSuffix)

	_, _, err = maasObjectPXEMACAddressAndHostname(node(noPXEMACAddress, noHostname))
	c.Check(err, gc.ErrorMatches, errNoPXEMACAddress)
	_, _, err = maasObjectPXEMACAddressAndHostname(node(nullPXEMACAddress, noHostname))
	c.Check(err, gc.ErrorMatches, errNoPXEMACAddress)

	_, _, err = maasObjectPXEMACAddressAndHostname(node(badPXEMACAddress, noHostname))
	c.Check(err, gc.ErrorMatches, "getting pxe_mac.mac_address failed"+errBadTypeSuffix)

	_, _, err = maasObjectPXEMACAddressAndHostname(node(goodPXEMACAddress, noHostname))
	c.Check(err, gc.ErrorMatches, errNoHostname)
	_, _, err = maasObjectPXEMACAddressAndHostname(node(goodPXEMACAddress, nullHostname))
	c.Check(err, gc.ErrorMatches, errNoHostname)

	_, _, err = maasObjectPXEMACAddressAndHostname(node(goodPXEMACAddress, badHostname))
	c.Check(err, gc.ErrorMatches, "getting hostname failed"+errBadTypeSuffix)

	mac, hostname, err := maasObjectPXEMACAddressAndHostname(node(goodPXEMACAddress, goodHostname))
	c.Check(err, jc.ErrorIsNil)
	c.Check(mac, gc.Equals, "mac")
	c.Check(hostname, gc.Equals, "node-xyz.maas")
}

func (s *interfacesSuite) TestMAASObjectNetworkInterfaces(c *gc.C) {
	// Expect to see hostname on top, followed by the first address of the PXE
	// interface.
	nodeJSON := fmt.Sprintf(`{
        "system_id": "foo",
        "hostname": "foo.bar.maas",
        "pxe_mac": {"mac_address": "52:54:00:70:9b:fe"},
        "interface_set": %s
    }`, exampleInterfaceSetJSON)
	obj := s.testMAASObject.TestServer.NewNode(nodeJSON)
	subnetsMap := make(map[string]network.Id)
	subnetsMap["10.250.19.0/24"] = network.Id("3")
	subnetsMap["192.168.1.0/24"] = network.Id("0")

	expected := []network.InterfaceInfo{{
		DeviceIndex:       0,
		MACAddress:        "52:54:00:70:9b:fe",
		CIDR:              "10.20.19.0/24",
		ProviderId:        "91",
		ProviderSubnetId:  "3",
		AvailabilityZones: nil,
		VLANTag:           0,
		ProviderVLANId:    "5001",
		ProviderAddressId: "",
		InterfaceName:     "eth0",
		InterfaceType:     "ethernet",
		Disabled:          false,
		NoAutoStart:       false,
		ConfigType:        "static",
		Address:           network.NewAddressOnSpace("default", "foo.bar.maas"),
		DNSServers:        network.NewAddressesOnSpace("default", "10.20.19.2", "10.20.19.3"),
		DNSSearchDomains:  nil,
		MTU:               1500,
		GatewayAddress:    network.NewAddressOnSpace("default", "10.20.19.2"),
	}, {
		DeviceIndex:       0,
		MACAddress:        "52:54:00:70:9b:fe",
		CIDR:              "10.20.19.0/24",
		ProviderId:        "91",
		ProviderSubnetId:  "3",
		AvailabilityZones: nil,
		VLANTag:           0,
		ProviderVLANId:    "5001",
		ProviderAddressId: "436",
		InterfaceName:     "eth0",
		InterfaceType:     "ethernet",
		Disabled:          false,
		NoAutoStart:       false,
		ConfigType:        "static",
		Address:           network.NewAddressOnSpace("default", "10.20.19.103"),
		DNSServers:        network.NewAddressesOnSpace("default", "10.20.19.2", "10.20.19.3"),
		DNSSearchDomains:  nil,
		MTU:               1500,
		GatewayAddress:    network.NewAddressOnSpace("default", "10.20.19.2"),
	}, {
		DeviceIndex:       0,
		MACAddress:        "52:54:00:70:9b:fe",
		CIDR:              "10.20.19.0/24",
		ProviderId:        "91",
		ProviderSubnetId:  "3",
		AvailabilityZones: nil,
		VLANTag:           0,
		ProviderVLANId:    "5001",
		ProviderAddressId: "437",
		InterfaceName:     "eth0",
		InterfaceType:     "ethernet",
		Disabled:          false,
		NoAutoStart:       false,
		ConfigType:        "static",
		Address:           network.NewAddressOnSpace("default", "10.20.19.104"),
		DNSServers:        network.NewAddressesOnSpace("default", "10.20.19.2", "10.20.19.3"),
		DNSSearchDomains:  nil,
		MTU:               1500,
		GatewayAddress:    network.NewAddressOnSpace("default", "10.20.19.2"),
	}, {
		DeviceIndex:         1,
		MACAddress:          "52:54:00:70:9b:fe",
		CIDR:                "10.50.19.0/24",
		ProviderId:          "150",
		ProviderSubnetId:    "5",
		AvailabilityZones:   nil,
		VLANTag:             50,
		ProviderVLANId:      "5004",
		ProviderAddressId:   "517",
		InterfaceName:       "eth0.50",
		ParentInterfaceName: "eth0",
		InterfaceType:       "802.1q",
		Disabled:            false,
		NoAutoStart:         false,
		ConfigType:          "static",
		Address:             network.NewAddressOnSpace("admin", "10.50.19.103"),
		DNSServers:          nil,
		DNSSearchDomains:    nil,
		MTU:                 1500,
		GatewayAddress:      network.NewAddressOnSpace("admin", "10.50.19.2"),
	}, {
		DeviceIndex:         2,
		MACAddress:          "52:54:00:70:9b:fe",
		CIDR:                "10.100.19.0/24",
		ProviderId:          "151",
		ProviderSubnetId:    "6",
		AvailabilityZones:   nil,
		VLANTag:             100,
		ProviderVLANId:      "5005",
		ProviderAddressId:   "519",
		InterfaceName:       "eth0.100",
		ParentInterfaceName: "eth0",
		InterfaceType:       "802.1q",
		Disabled:            false,
		NoAutoStart:         false,
		ConfigType:          "static",
		Address:             network.NewAddressOnSpace("public", "10.100.19.103"),
		DNSServers:          nil,
		DNSSearchDomains:    nil,
		MTU:                 1500,
		GatewayAddress:      network.NewAddressOnSpace("public", "10.100.19.2"),
	}, {
		DeviceIndex:         3,
		MACAddress:          "52:54:00:70:9b:fe",
		CIDR:                "10.250.19.0/24",
		ProviderId:          "152",
		ProviderSubnetId:    "8",
		AvailabilityZones:   nil,
		VLANTag:             250,
		ProviderVLANId:      "5008",
		ProviderAddressId:   "523",
		ProviderSpaceId:     "3",
		InterfaceName:       "eth0.250",
		ParentInterfaceName: "eth0",
		InterfaceType:       "802.1q",
		Disabled:            false,
		NoAutoStart:         false,
		ConfigType:          "static",
		Address:             newAddressOnSpaceWithId("storage", network.Id("3"), "10.250.19.103"),
		DNSServers:          nil,
		DNSSearchDomains:    nil,
		MTU:                 1500,
		GatewayAddress:      newAddressOnSpaceWithId("storage", network.Id("3"), "10.250.19.2"),
	}}

	infos, err := maasObjectNetworkInterfaces(&obj, subnetsMap)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(infos, jc.DeepEquals, expected)
}

func (s *interfacesSuite) TestMAAS2NetworkInterfaces(c *gc.C) {
	vlan0 := fakeVLAN{
		id:  5001,
		vid: 0,
		mtu: 1500,
	}

	vlan50 := fakeVLAN{
		id:  5004,
		vid: 50,
		mtu: 1500,
	}

	vlan100 := fakeVLAN{
		id:  5005,
		vid: 100,
		mtu: 1500,
	}

	vlan250 := fakeVLAN{
		id:  5008,
		vid: 250,
		mtu: 1500,
	}

	subnetPXE := fakeSubnet{
		id:         3,
		space:      "default",
		vlan:       vlan0,
		gateway:    "10.20.19.2",
		cidr:       "10.20.19.0/24",
		dnsServers: []string{"10.20.19.2", "10.20.19.3"},
	}

	exampleInterfaces := []gomaasapi.Interface{
		&fakeInterface{
			id:         91,
			name:       "eth0",
			type_:      "physical",
			enabled:    true,
			macAddress: "52:54:00:70:9b:fe",
			vlan:       vlan0,
			links: []gomaasapi.Link{
				&fakeLink{
					id:        436,
					subnet:    &subnetPXE,
					ipAddress: "10.20.19.103",
					mode:      "static",
				},
				&fakeLink{
					id:        437,
					subnet:    &subnetPXE,
					ipAddress: "10.20.19.104",
					mode:      "static",
				},
			},
			parents:  []string{},
			children: []string{"eth0.100", "eth0.250", "eth0.50"},
		},
		&fakeInterface{
			id:         150,
			name:       "eth0.50",
			type_:      "vlan",
			enabled:    true,
			macAddress: "52:54:00:70:9b:fe",
			vlan:       vlan50,
			links: []gomaasapi.Link{
				&fakeLink{
					id: 517,
					subnet: &fakeSubnet{
						id:         5,
						space:      "admin",
						vlan:       vlan50,
						gateway:    "10.50.19.2",
						cidr:       "10.50.19.0/24",
						dnsServers: []string{},
					},
					ipAddress: "10.50.19.103",
					mode:      "static",
				},
			},
			parents:  []string{"eth0"},
			children: []string{},
		},
		&fakeInterface{
			id:         151,
			name:       "eth0.100",
			type_:      "vlan",
			enabled:    true,
			macAddress: "52:54:00:70:9b:fe",
			vlan:       vlan100,
			links: []gomaasapi.Link{
				&fakeLink{
					id: 519,
					subnet: &fakeSubnet{
						id:         6,
						space:      "public",
						vlan:       vlan100,
						gateway:    "10.100.19.2",
						cidr:       "10.100.19.0/24",
						dnsServers: []string{},
					},
					ipAddress: "10.100.19.103",
					mode:      "static",
				},
			},
			parents:  []string{"eth0"},
			children: []string{},
		},
		&fakeInterface{
			id:         152,
			name:       "eth0.250",
			type_:      "vlan",
			enabled:    true,
			macAddress: "52:54:00:70:9b:fe",
			vlan:       vlan250,
			links: []gomaasapi.Link{
				&fakeLink{
					id: 523,
					subnet: &fakeSubnet{
						id:         8,
						space:      "storage",
						vlan:       vlan250,
						gateway:    "10.250.19.2",
						cidr:       "10.250.19.0/24",
						dnsServers: []string{},
					},
					ipAddress: "10.250.19.103",
					mode:      "static",
				},
			},
			parents:  []string{"eth0"},
			children: []string{},
		},
	}

	subnetsMap := make(map[string]network.Id)
	subnetsMap["10.250.19.0/24"] = network.Id("3")
	subnetsMap["192.168.1.0/24"] = network.Id("0")

	expected := []network.InterfaceInfo{{
		DeviceIndex:       0,
		MACAddress:        "52:54:00:70:9b:fe",
		CIDR:              "10.20.19.0/24",
		ProviderId:        "91",
		ProviderSubnetId:  "3",
		AvailabilityZones: nil,
		VLANTag:           0,
		ProviderVLANId:    "5001",
		ProviderAddressId: "436",
		InterfaceName:     "eth0",
		InterfaceType:     "ethernet",
		Disabled:          false,
		NoAutoStart:       false,
		ConfigType:        "static",
		Address:           network.NewAddressOnSpace("default", "10.20.19.103"),
		DNSServers:        network.NewAddressesOnSpace("default", "10.20.19.2", "10.20.19.3"),
		DNSSearchDomains:  nil,
		MTU:               1500,
		GatewayAddress:    network.NewAddressOnSpace("default", "10.20.19.2"),
	}, {
		DeviceIndex:       0,
		MACAddress:        "52:54:00:70:9b:fe",
		CIDR:              "10.20.19.0/24",
		ProviderId:        "91",
		ProviderSubnetId:  "3",
		AvailabilityZones: nil,
		VLANTag:           0,
		ProviderVLANId:    "5001",
		ProviderAddressId: "437",
		InterfaceName:     "eth0",
		InterfaceType:     "ethernet",
		Disabled:          false,
		NoAutoStart:       false,
		ConfigType:        "static",
		Address:           network.NewAddressOnSpace("default", "10.20.19.104"),
		DNSServers:        network.NewAddressesOnSpace("default", "10.20.19.2", "10.20.19.3"),
		DNSSearchDomains:  nil,
		MTU:               1500,
		GatewayAddress:    network.NewAddressOnSpace("default", "10.20.19.2"),
	}, {
		DeviceIndex:         1,
		MACAddress:          "52:54:00:70:9b:fe",
		CIDR:                "10.50.19.0/24",
		ProviderId:          "150",
		ProviderSubnetId:    "5",
		AvailabilityZones:   nil,
		VLANTag:             50,
		ProviderVLANId:      "5004",
		ProviderAddressId:   "517",
		InterfaceName:       "eth0.50",
		ParentInterfaceName: "eth0",
		InterfaceType:       "802.1q",
		Disabled:            false,
		NoAutoStart:         false,
		ConfigType:          "static",
		Address:             network.NewAddressOnSpace("admin", "10.50.19.103"),
		DNSServers:          nil,
		DNSSearchDomains:    nil,
		MTU:                 1500,
		GatewayAddress:      network.NewAddressOnSpace("admin", "10.50.19.2"),
	}, {
		DeviceIndex:         2,
		MACAddress:          "52:54:00:70:9b:fe",
		CIDR:                "10.100.19.0/24",
		ProviderId:          "151",
		ProviderSubnetId:    "6",
		AvailabilityZones:   nil,
		VLANTag:             100,
		ProviderVLANId:      "5005",
		ProviderAddressId:   "519",
		InterfaceName:       "eth0.100",
		ParentInterfaceName: "eth0",
		InterfaceType:       "802.1q",
		Disabled:            false,
		NoAutoStart:         false,
		ConfigType:          "static",
		Address:             network.NewAddressOnSpace("public", "10.100.19.103"),
		DNSServers:          nil,
		DNSSearchDomains:    nil,
		MTU:                 1500,
		GatewayAddress:      network.NewAddressOnSpace("public", "10.100.19.2"),
	}, {
		DeviceIndex:         3,
		MACAddress:          "52:54:00:70:9b:fe",
		CIDR:                "10.250.19.0/24",
		ProviderId:          "152",
		ProviderSubnetId:    "8",
		AvailabilityZones:   nil,
		VLANTag:             250,
		ProviderVLANId:      "5008",
		ProviderAddressId:   "523",
		ProviderSpaceId:     "3",
		InterfaceName:       "eth0.250",
		ParentInterfaceName: "eth0",
		InterfaceType:       "802.1q",
		Disabled:            false,
		NoAutoStart:         false,
		ConfigType:          "static",
		Address:             newAddressOnSpaceWithId("storage", network.Id("3"), "10.250.19.103"),
		DNSServers:          nil,
		DNSSearchDomains:    nil,
		MTU:                 1500,
		GatewayAddress:      newAddressOnSpaceWithId("storage", network.Id("3"), "10.250.19.2"),
	}}

	instance := &maas2Instance{machine: &fakeMachine{interfaceSet: exampleInterfaces}}

	infos, err := maas2NetworkInterfaces(instance, subnetsMap)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(infos, jc.DeepEquals, expected)
}

func (s *interfacesSuite) TestMAAS2InterfacesNilVLAN(c *gc.C) {
	vlan0 := fakeVLAN{
		id:  5001,
		vid: 0,
		mtu: 1500,
	}

	subnetPXE := fakeSubnet{
		id:         3,
		space:      "default",
		vlan:       vlan0,
		gateway:    "10.20.19.2",
		cidr:       "10.20.19.0/24",
		dnsServers: []string{"10.20.19.2", "10.20.19.3"},
	}

	exampleInterfaces := []gomaasapi.Interface{
		&fakeInterface{
			id:         91,
			name:       "eth0",
			type_:      "physical",
			enabled:    true,
			macAddress: "52:54:00:70:9b:fe",
			vlan:       nil,
			links: []gomaasapi.Link{&fakeLink{
				id:        436,
				subnet:    &subnetPXE,
				ipAddress: "10.20.19.103",
				mode:      "static",
			}},
			parents:  []string{},
			children: []string{"eth0.100", "eth0.250", "eth0.50"},
		},
	}

	instance := &maas2Instance{machine: &fakeMachine{interfaceSet: exampleInterfaces}}

	expected := []network.InterfaceInfo{{
		DeviceIndex:       0,
		MACAddress:        "52:54:00:70:9b:fe",
		CIDR:              "10.20.19.0/24",
		ProviderId:        "91",
		ProviderSubnetId:  "3",
		AvailabilityZones: nil,
		VLANTag:           0,
		ProviderVLANId:    "5001",
		ProviderAddressId: "436",
		InterfaceName:     "eth0",
		InterfaceType:     "ethernet",
		Disabled:          false,
		NoAutoStart:       false,
		ConfigType:        "static",
		Address:           network.NewAddressOnSpace("default", "10.20.19.103"),
		DNSServers:        network.NewAddressesOnSpace("default", "10.20.19.2", "10.20.19.3"),
		DNSSearchDomains:  nil,
		MTU:               1500,
		GatewayAddress:    network.NewAddressOnSpace("default", "10.20.19.2"),
	}}

	infos, err := maas2NetworkInterfaces(instance, map[string]network.Id{})
	c.Assert(err, jc.ErrorIsNil)
	c.Check(infos, jc.DeepEquals, expected)
}

const lshwXMLTemplate = `
<?xml version="1.0" standalone="yes" ?>
<!-- generated by lshw-B.02.16 -->
<list>
<node id="node1" claimed="true" class="system" handle="DMI:0001">
 <description>Computer</description>
 <product>VirtualBox ()</product>
 <width units="bits">64</width>
  <node id="core" claimed="true" class="bus" handle="DMI:0008">
   <description>Motherboard</description>
    <node id="pci" claimed="true" class="bridge" handle="PCIBUS:0000:00">
     <description>Host bridge</description>{{$list := .}}{{range $mac, $ifi := $list}}
      <node id="network{{if gt (len $list) 1}}:{{$ifi.DeviceIndex}}{{end}}"{{if $ifi.Disabled}} disabled="true"{{end}} claimed="true" class="network" handle="PCI:0000:00:03.0">
       <description>Ethernet interface</description>
       <product>82540EM Gigabit Ethernet Controller</product>
       <logicalname>{{$ifi.InterfaceName}}</logicalname>
       <serial>{{$mac}}</serial>
      </node>{{end}}
    </node>
  </node>
</node>
</list>
`

func (suite *environSuite) generateHWTemplate(netMacs map[string]ifaceInfo) (string, error) {
	tmpl, err := template.New("test").Parse(lshwXMLTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, netMacs)
	if err != nil {
		return "", err
	}
	return string(buf.Bytes()), nil
}
