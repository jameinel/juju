// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"github.com/juju/names"
	"gopkg.in/mgo.v2/bson"
)

// NetworkInterface represents the state of a machine network
// interface.
type NetworkInterface struct {
	st  *State
	doc networkInterfaceDoc
}

// NetworkInterfaceInfo describes a single network interface available
// on an instance.
type NetworkInterfaceInfo struct {
	// MACAddress is the network interface's hardware MAC address
	// (e.g. "aa:bb:cc:dd:ee:ff").
	MACAddress string

	// InterfaceName is the OS-specific network device name (e.g.
	// "eth0", or "eth1.42" for a VLAN virtual interface, or
	// "eth1:suffix" for a network alias).
	InterfaceName string

	// Disabled returns whether the interface is disabled.
	Disabled bool
}

// networkInterfaceDoc represents a network interface for a machine on
// a given network.
type networkInterfaceDoc struct {
	Id            bson.ObjectId `bson:"_id"`
	EnvUUID       string        `bson:"env-uuid"`
	MACAddress    string        `bson:"macaddress"`
	InterfaceName string        `bson:"interfacename"`
	MachineId     string        `bson:"machineid"`
	IsDisabled    bool          `bson:"isdisabled"`
}

// Id returns the internal juju-specific id of the interface.
func (ni *NetworkInterface) Id() string {
	return ni.doc.Id.String()
}

// MACAddress returns the MAC address of the interface.
func (ni *NetworkInterface) MACAddress() string {
	return ni.doc.MACAddress
}

// InterfaceName returns the name of the interface.
func (ni *NetworkInterface) InterfaceName() string {
	return ni.doc.InterfaceName
}

// MachineId returns the machine id of the interface.
func (ni *NetworkInterface) MachineId() string {
	return ni.doc.MachineId
}

// MachineTag returns the machine tag of the interface.
func (ni *NetworkInterface) MachineTag() names.MachineTag {
	return names.NewMachineTag(ni.doc.MachineId)
}

// IsDisabled returns whether the interface is disabled.
func (ni *NetworkInterface) IsDisabled() bool {
	return ni.doc.IsDisabled
}

func newNetworkInterface(st *State, doc *networkInterfaceDoc) *NetworkInterface {
	return &NetworkInterface{st, *doc}
}

func newNetworkInterfaceDoc(machineID, envUUID string, args NetworkInterfaceInfo) *networkInterfaceDoc {
	return &networkInterfaceDoc{
		Id:            bson.NewObjectId(),
		EnvUUID:       envUUID,
		MachineId:     machineID,
		MACAddress:    args.MACAddress,
		InterfaceName: args.InterfaceName,
		IsDisabled:    args.Disabled,
	}
}
