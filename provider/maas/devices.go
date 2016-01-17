// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	"encoding/json"
	"net/url"

	"launchpad.net/gomaasapi"

	"github.com/juju/errors"

	"github.com/juju/juju/network"
)

// TODO(dimitern): The types below should be part of gomaasapi.
// LKK Card: https://canonical.leankit.com/Boards/View/101652562/119310616

type maasZone struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ResourceURI string `json:"resource_uri"`
}

type maasMACAddress struct {
	Value string `json:"mac_address"`
}

type maasDevice struct {
	SystemID     string           `json:"system_id"`
	ParentID     *string          `json:"parent,omitempty"`
	Hostname     string           `json:"hostname"`
	IPAddresses  []string         `json:"ip_addresses,omitempty"`
	MACAddresses []maasMACAddress `json:"macaddress_set"`
	Zone         maasZone         `json:"zone"`
	Owner        string           `json:"owner"`
	TagNames     []string         `json:"tag_names,omitempty"`
	ResourceURI  string           `json:"resource_uri"`
}

// parseDevices extracts the raw JSON from the given jsonData and then parses
// the result into a slice of maasDevice structs. If the JSON contains a single
// device rather than a list, parseDevices will still work and a single entry
// will be returned.
func parseDevices(jsonData json.Marshaler) ([]maasDevice, error) {

	if jsonData == nil {
		return nil, errors.New("nil JSON data")
	}

	rawBytes, err := getJSONBytes(jsonData)
	if err != nil {
		return nil, errors.Trace(err)
	}

	// Try parsing it first as a list of devices.
	var devices []maasDevice
	err = json.Unmarshal(rawBytes, &devices)
	if err == nil {
		return devices, nil
	}
	if _, ok := err.(*json.UnmarshalTypeError); !ok {
		return nil, errors.Annotate(err, "parsing devices")
	}

	// Finally, try parsing it as a single device.
	var device maasDevice
	if err := json.Unmarshal(rawBytes, &device); err != nil {
		return nil, errors.Annotate(err, "parsing device")
	}
	return []maasDevice{device}, nil
}

// deviceAPI defines a subset of the gomaasapi.MAASObject methods needed for the
// devices API calls.
type devicesAPI interface {
	GetSubObject(uri string) devicesAPI
	CallGet(op string, params url.Values) (devicesAPI, error)
	CallPost(op string, params url.Values) (devicesAPI, error)
	Delete() error
	Update(params url.Values) (devicesAPI, error)
	Get() (devicesAPI, error)
	MarshalJSON() ([]byte, error)
}

// devicesAPIShim adapts both gomaasapi.MAASObject and gomaasapi.JSONObject to
// devicesAPI interface, as devicesAPI uses different return types, which can't
// be changed.
//
// TODO(dimitern): Drop this once the gomaasapi test server allows inspecting
// requests/responses and/or supports better the needed API calls.
//
// LKK Card: https://canonical.leankit.com/Boards/View/101652562/119310616
type devicesAPIShim struct {
	*gomaasapi.MAASObject
	*gomaasapi.JSONObject
}

func (d *devicesAPIShim) GetSubObject(uri string) devicesAPI {
	obj := d.MAASObject.GetSubObject(uri)
	return &devicesAPIShim{MAASObject: &obj}
}

func (d *devicesAPIShim) CallGet(op string, params url.Values) (devicesAPI, error) {
	result, err := d.MAASObject.CallGet(op, params)
	if err != nil {
		return nil, err // don't wrap so we don't obscure a ServerError.
	}
	return &devicesAPIShim{JSONObject: &result}, nil
}

func (d *devicesAPIShim) CallPost(op string, params url.Values) (devicesAPI, error) {
	result, err := d.MAASObject.CallPost(op, params)
	if err != nil {
		return nil, err // don't wrap so we don't obscure a ServerError.
	}
	return &devicesAPIShim{JSONObject: &result}, nil
}

func (d *devicesAPIShim) Update(params url.Values) (devicesAPI, error) {
	result, err := d.MAASObject.Update(params)
	if err != nil {
		return nil, err // don't wrap so we don't obscure a ServerError.
	}
	// NOTE: The following case is only manually tested live, as gomaasapi test
	// server does not support device updates.
	return &devicesAPIShim{MAASObject: &result}, nil
}

func (d *devicesAPIShim) Get() (devicesAPI, error) {
	result, err := d.MAASObject.Get()
	if err != nil {
		return nil, err // don't wrap so we don't obscure a ServerError.
	}
	return &devicesAPIShim{MAASObject: &result}, nil
}

func (d *devicesAPIShim) MarshalJSON() ([]byte, error) {
	if d.JSONObject != nil {
		return d.JSONObject.MarshalJSON()
	}
	if d.MAASObject != nil {
		return d.MAASObject.MarshalJSON()
	}
	// This can't happen in practice as one of the objects will be always set,
	// but for completeness sake we have a test for it.
	return nil, errors.Errorf("no object available to marshal")
}

// devicesClient returns the devicesAPI to use when calling devices API calls
// and allows to test the requests and responses of those calls.
var devicesClient = func(environ *maasEnviron) devicesAPI {
	obj := environ.getMAASClient().GetSubObject("devices")
	return &devicesAPIShim{MAASObject: &obj}
}

// listDevices returns the parsed devices list, which match all of the
// specified, optional filters.
func (environ *maasEnviron) listDevices(withHostnames, withMACs, withParents, withIDs []string) ([]maasDevice, error) {
	devicesObj := devicesClient(environ)

	params := make(url.Values)
	for _, hostname := range withHostnames {
		params.Add("hostname", hostname)
	}

	for _, mac := range withMACs {
		params.Add("mac_address", mac)
	}

	for _, parent := range withParents {
		params.Add("parent", parent)
	}

	for _, id := range withIDs {
		params.Add("id", id)
	}

	result, err := devicesObj.CallGet("list", params)
	if err != nil {
		return nil, errors.Annotate(err, "fetching devices result")
	}

	return parseDevices(result)
}

// createDevice tries to create a new device using the MAAS API, with the
// specified parent, (primary) MAC address, and hostname. The macAddress is
// always required but both parentID and hostname can be empty.
func (environ *maasEnviron) createDevice(parentID, macAddress, hostname string) (*maasDevice, error) {
	if macAddress == "" {
		return nil, errors.New("cannot create a device with empty MAC address")
	}

	devicesObj := devicesClient(environ)

	params := make(url.Values)
	params.Add("mac_addresses", macAddress)

	if parentID != "" {
		params.Add("parent", parentID)
	}

	if hostname != "" {
		params.Add("hostname", hostname)
	}

	result, err := devicesObj.CallPost("new", params)
	if err != nil {
		return nil, errors.Annotatef(
			err,
			"cannot create device with parent %q, MAC address %q, hostname %q",
			parentID, macAddress, hostname,
		)
	}

	devices, err := parseDevices(result)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return &devices[0], nil
}

// updateDevice tries to update the hostname and/or parent of the device
// matching the given device.SystemID. No changes will be made if either
// Hostname or ParentID are empty.
func (environ *maasEnviron) updateDevice(device maasDevice) error {
	deviceObj := devicesClient(environ).GetSubObject(device.SystemID)

	params := make(url.Values)
	if device.Hostname != "" {
		params.Add("hostname", device.Hostname)
	}

	if device.ParentID != nil && *device.ParentID != "" {
		params.Add("parent", *device.ParentID)
	}

	_, err := deviceObj.Update(params)
	if code := getMAASErrorCode(err); code == 404 {
		return errors.NotFoundf("device %q", device.SystemID)
	} else if err != nil {
		return errors.Annotatef(err, "cannot update device %q", device.SystemID)
	}
	return nil
}

// deleteDevice tries to remove the device matching the given deviceID, if it
// exists. If no such device exists returns an error satisfying
// errors.IsNotFound().
func (environ *maasEnviron) deleteDevice(deviceID string) error {
	deviceObj := devicesClient(environ).GetSubObject(deviceID)

	err := deviceObj.Delete()
	if code := getMAASErrorCode(err); code == 404 {
		return errors.NotFoundf("device %q", deviceID)
	} else if err != nil {
		return errors.Annotatef(err, "cannot delete device %q", deviceID)
	}
	return nil
}

// getDevice returns the full information for a device with the given deviceID.
// When no such device exists an error satisfying errors.IsNotFound() is
// returned.
func (environ *maasEnviron) getDevice(deviceID string) (*maasDevice, error) {
	deviceObj := devicesClient(environ).GetSubObject(deviceID)

	result, err := deviceObj.Get()
	if code := getMAASErrorCode(err); code == 404 {
		return nil, errors.NotFoundf("device %q", deviceID)
	} else if err != nil {
		return nil, errors.Annotatef(err, "cannot get device %q", deviceID)
	}

	devices, err := parseDevices(result)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return &devices[0], nil
}

// setupDeviceInterfaces takes a slice of maasInterface entries and replaces the
// device's current interfaces config with it. Typically the interfaceConfig is
// taken from the device's parent's interfaces (or a subset of them).
func (environ *maasEnviron) setupDeviceInterfaces(deviceID string, interfaceConfig []maasInterface) ([]maasInterface, error) {
	return nil, errors.NotImplementedf("setupDeviceInterfaces")
}

// claimStickyIPForDevice reserves a sticky IP address for the given deviceID -
// either the given address, or (when empty) an available address given by MAAS.
//
// NOTE: This is only used when the address-allocation feature flag is enabled
// and devices API is supported by MAAS. Otherwise the default behavior is to
// use setupDeviceInterfaces() to mirror the parent's interfaces and claim
// addresses for each one explicitly.
func (environ *maasEnviron) claimStickyIPForDevice(deviceID string, address network.Address) (network.Address, error) {
	return network.Address{}, errors.NotImplementedf("claimStickyIPForDevice")
}
