// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	"encoding/json"
	"net/url"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
)

const (
	exampleSingleDeviceNoParentJSON = `
	{
		"macaddress_set": [
			{
				"mac_address": "aa:bb:cc:dd:ee:b3"
			},
			{
				"mac_address": "aa:ff:cc:dd:ee:b3"
			}
		],
		"zone": {
			"resource_uri": "/MAAS/api/1.0/zones/default/",
			"name": "default",
			"description": ""
		},
		"parent": null,
		"ip_addresses": [],
		"hostname": "terrible-plane.maas-19",
		"system_id": "node-8643d12a-ba93-11e5-9c9b-00163e40c3b6",
		"owner": "juju",
		"tag_names": [],
		"resource_uri": "/MAAS/api/1.0/devices/node-8643d12a-ba93-11e5-9c9b-00163e40c3b6/"
	}`

	exampleSingleDeviceWithParentJSON = `
	{
		"macaddress_set": [
			{
				"mac_address": "aa:bb:cc:dd:ee:bc"
			}
		],
		"zone": {
			"resource_uri": "/MAAS/api/1.0/zones/default/",
			"name": "default",
			"description": ""
		},
		"parent": "node-d7b96c1c-9762-11e5-8e2e-00163e40c3b6",
		"ip_addresses": [
			"10.20.30.22",
			"10.20.30.23"
		],
		"hostname": "hideous-stem.maas-19",
		"system_id": "node-b6606800-ba93-11e5-ae39-00163e40c3b6",
		"owner": "juju",
		"tag_names": [],
		"resource_uri": "/MAAS/api/1.0/devices/node-b6606800-ba93-11e5-ae39-00163e40c3b6/"
	}`

	exampleDevicesListJSON = `
[
` + exampleSingleDeviceNoParentJSON + `,
` + exampleSingleDeviceWithParentJSON + `
]`
)

var (
	expectedDefaultZone = maasZone{
		Name:        "default",
		Description: "",
		ResourceURI: "/MAAS/api/1.0/zones/default/",
	}

	expectedSingleDeviceNoParent = maasDevice{
		SystemID:    "node-8643d12a-ba93-11e5-9c9b-00163e40c3b6",
		ParentID:    nil,
		Hostname:    "terrible-plane.maas-19",
		IPAddresses: nil,
		MACAddresses: []maasMACAddress{
			{Value: "aa:bb:cc:dd:ee:b3"},
			{Value: "aa:ff:cc:dd:ee:b3"},
		},
		Zone:        expectedDefaultZone,
		Owner:       "juju",
		ResourceURI: "/MAAS/api/1.0/devices/node-8643d12a-ba93-11e5-9c9b-00163e40c3b6/",
	}

	expectedParentID = "node-d7b96c1c-9762-11e5-8e2e-00163e40c3b6"

	expectedSingleDeviceWithParent = maasDevice{
		SystemID: "node-b6606800-ba93-11e5-ae39-00163e40c3b6",
		ParentID: &expectedParentID,
		Hostname: "hideous-stem.maas-19",
		IPAddresses: []string{
			"10.20.30.22",
			"10.20.30.23",
		},
		MACAddresses: []maasMACAddress{
			{Value: "aa:bb:cc:dd:ee:bc"},
		},
		Zone:        expectedDefaultZone,
		Owner:       "juju",
		ResourceURI: "/MAAS/api/1.0/devices/node-b6606800-ba93-11e5-ae39-00163e40c3b6/",
	}
)

type devicesSuite struct {
	providerSuite

	fakeAPI      *fakeDevicesAPI
	maas404Error error
}

var _ = gc.Suite(&devicesSuite{})

func (s *devicesSuite) SetUpTest(c *gc.C) {
	s.providerSuite.SetUpTest(c)

	fakeDevices := []maasDevice{
		expectedSingleDeviceNoParent,
		expectedSingleDeviceWithParent,
	}

	s.fakeAPI = &fakeDevicesAPI{
		Stub:         &testing.Stub{},
		allDevices:   fakeDevices,
		singleDevice: fakeDevices[0],
	}
	s.PatchValue(&devicesClient, func(environ *maasEnviron) devicesAPI {
		c.Assert(environ, gc.NotNil)
		return s.fakeAPI
	})

	// Since we can't set the embedded error interface inside
	// gomaasapi.ServerError as it's "unexported", we trick the test server to
	// do it instead. We need to test the behavior when 404 is received. Also
	// Due to the awkward multi-line error message formatting + parenthesis in
	// ServerError it's simpler to use HasPrefix instead of ErrorMatches.
	s.maas404Error = s.testMAASObject.Delete()
	c.Assert(s.maas404Error.Error(), jc.HasPrefix, "gomaasapi: got error back from server: 404 Not Found")
}

func (s *devicesSuite) TestParseDevicesNoJSON(c *gc.C) {
	result, err := parseDevices(nil)
	c.Check(err, gc.ErrorMatches, "nil JSON data")
	c.Check(result, gc.IsNil)
}

func (s *devicesSuite) TestParseDevicesBadJSON(c *gc.C) {

	data := json.RawMessage([]byte("$bad"))

	result, err := parseDevices(&data)
	c.Check(err, gc.ErrorMatches, `parsing devices: invalid character '\$' .*`)
	c.Check(result, gc.IsNil)

	data = json.RawMessage([]byte("[$bad]"))

	result, err = parseDevices(&data)
	c.Check(err, gc.ErrorMatches, `parsing devices: invalid character '\$' .*`)
	c.Check(result, gc.IsNil)
}

func (s *devicesSuite) TestParseDevicesExampleJSONSingleDevice(c *gc.C) {
	data := json.RawMessage([]byte(exampleSingleDeviceNoParentJSON))

	result, err := parseDevices(&data)
	c.Check(err, jc.ErrorIsNil)
	c.Check(result, jc.DeepEquals, []maasDevice{expectedSingleDeviceNoParent})

	data = json.RawMessage([]byte(exampleSingleDeviceWithParentJSON))

	result, err = parseDevices(&data)
	c.Check(err, jc.ErrorIsNil)
	c.Check(result, jc.DeepEquals, []maasDevice{expectedSingleDeviceWithParent})
}

func (s *devicesSuite) TestParseDevicesExampleJSONDeviceList(c *gc.C) {
	data := json.RawMessage([]byte(exampleDevicesListJSON))
	result, err := parseDevices(&data)
	c.Check(err, jc.ErrorIsNil)

	expectedDevices := []maasDevice{
		expectedSingleDeviceNoParent,
		expectedSingleDeviceWithParent,
	}
	c.Check(result, jc.DeepEquals, expectedDevices)
}

func (s *devicesSuite) TestListDevicesNoFilters(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil, // first call to CallGet succeeds
		nil, // subsequent call to MarshalJSON also succeeds
		errors.New("you require more vespene gas"), // second call to CallGet fails
		nil, // third call to CallGet succeds
		errors.New("we need more pylons"), // but the following MarshalJSON fails
	)
	env := s.makeEnviron()

	devices, err := env.listDevices(nil, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	devices, err = env.listDevices(nil, nil, nil)
	c.Assert(err, gc.ErrorMatches, "fetching devices result: you require more vespene gas")
	c.Check(devices, gc.IsNil)

	devices, err = env.listDevices(nil, nil, nil)
	c.Assert(err, gc.ErrorMatches, "cannot get JSON bytes: we need more pylons")
	c.Check(devices, gc.IsNil)

	expectedCall := testing.StubCall{
		FuncName: "CallGet",
		Args:     []interface{}{"list", url.Values{}},
	}
	s.fakeAPI.CheckCalls(c, []testing.StubCall{expectedCall, expectedCall, expectedCall})
}

func (s *devicesSuite) TestListDevicesWithSingleFilter(c *gc.C) {
	env := s.makeEnviron()

	hostFilters := []string{"host1", "host2", "host3"}
	devices, err := env.listDevices(hostFilters[0:1], nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	devices, err = env.listDevices(hostFilters, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "CallGet",
		Args:     []interface{}{"list", url.Values{"hostname": hostFilters[0:1]}},
	}, {
		FuncName: "CallGet",
		Args:     []interface{}{"list", url.Values{"hostname": hostFilters}},
	}})
}

func (s *devicesSuite) TestListDevicesWithAllFilters(c *gc.C) {
	env := s.makeEnviron()

	hostFilters := []string{"host1", "host2", "host3"}
	parentFilters := []string{"node1", "node2"}
	idFilters := []string{"id1", "id2", "id3"}
	devices, err := env.listDevices(hostFilters[0:1], parentFilters[0:1], idFilters[0:1])
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	devices, err = env.listDevices(hostFilters, parentFilters, idFilters)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "CallGet",
		Args: []interface{}{
			"list",
			url.Values{
				"hostname": hostFilters[0:1],
				"parent":   parentFilters[0:1],
				"id":       idFilters[0:1],
			}},
	}, {
		FuncName: "CallGet",
		Args: []interface{}{
			"list",
			url.Values{
				"hostname": hostFilters,
				"parent":   parentFilters,
				"id":       idFilters,
			}},
	}})
}

func (s *devicesSuite) TestCreateDeviceRequiresNonEmptyMACAddress(c *gc.C) {
	env := s.makeEnviron()

	device, err := env.createDevice("", "", "")
	c.Assert(err, gc.ErrorMatches, "cannot create a device with empty MAC address")
	c.Check(device, gc.IsNil)

	device, err = env.createDevice("parent", "", "host")
	c.Assert(err, gc.ErrorMatches, "cannot create a device with empty MAC address")
	c.Check(device, gc.IsNil)

	s.fakeAPI.CheckNoCalls(c)
}

func (s *devicesSuite) TestCreateDeviceWithOnlyOptionalParams(c *gc.C) {
	s.fakeAPI.SetErrors(
		// first call to CallPost fails
		errors.New("flawless victory"),
		nil, // second call to CallPost succeeds
		errors.New("fatality"), // but the next MarshalJSON call fails
		nil, // third CallPost succeeds
		nil, // second MarshalJSON succeeds
	)
	env := s.makeEnviron()

	device, err := env.createDevice("", "mac", "")
	c.Assert(err, gc.ErrorMatches,
		`cannot create device with parent "", MAC address "mac", hostname "": flawless victory`,
	)
	c.Check(device, gc.IsNil)

	device, err = env.createDevice("", "mac", "")
	c.Assert(err, gc.ErrorMatches, "cannot get JSON bytes: fatality")
	c.Check(device, gc.IsNil)

	device, err = env.createDevice("", "mac", "")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(*device, jc.DeepEquals, s.fakeAPI.singleDevice)

	expectedCall := testing.StubCall{
		FuncName: "CallPost",
		Args: []interface{}{
			"new",
			url.Values{"mac_addresses": []string{"mac"}},
		},
	}
	s.fakeAPI.CheckCalls(c, []testing.StubCall{expectedCall, expectedCall, expectedCall})
}

func (s *devicesSuite) TestCreateDeviceWithAllParams(c *gc.C) {
	env := s.makeEnviron()

	device, err := env.createDevice("parent", "mac", "host")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(*device, jc.DeepEquals, s.fakeAPI.singleDevice)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "CallPost",
		Args: []interface{}{
			"new",
			url.Values{
				"parent":        []string{"parent"},
				"mac_addresses": []string{"mac"},
				"hostname":      []string{"host"},
			}},
	}})
}

func (s *devicesSuite) TestUpdateDeviceWhenNotFound(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil,            // first call to GetSubObject succeeds (it can't fail anyway)
		s.maas404Error, // following call to Update fails with 404 error.
	)

	env := s.makeEnviron()

	dev := maasDevice{SystemID: "foo"}
	err := env.updateDevice(dev)
	c.Assert(err, gc.ErrorMatches, `device "foo" not found`)
	c.Check(err, jc.Satisfies, errors.IsNotFound)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "GetSubObject",
		Args:     []interface{}{"foo"},
	}, {
		FuncName: "Update",
		Args:     []interface{}{url.Values{}},
	}})
}

func (s *devicesSuite) TestUpdateDeviceSkipsEmptyParams(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil, // first call to GetSubObject succeeds (it can't fail anyway)
		nil, // following call to Update succeeds
		nil, // second call to GetSubObject succeeds
		errors.New("nothing works"), // second call to Update fails
	)
	env := s.makeEnviron()

	parent := "parent"
	dev := maasDevice{
		SystemID: "foo",
		Hostname: "bar",
		ParentID: &parent,
	}
	err := env.updateDevice(dev)
	c.Assert(err, jc.ErrorIsNil)

	dev.ParentID = nil
	err = env.updateDevice(dev)
	c.Assert(err, gc.ErrorMatches, `cannot update device "foo": nothing works`)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "GetSubObject",
		Args:     []interface{}{"foo"},
	}, {
		FuncName: "Update",
		Args: []interface{}{url.Values{
			"hostname": []string{"bar"},
			"parent":   []string{"parent"},
		}},
	}, {
		FuncName: "GetSubObject",
		Args:     []interface{}{"foo"},
	}, {
		FuncName: "Update",
		Args: []interface{}{url.Values{
			"hostname": []string{"bar"},
		}},
	}})
}

func (s *devicesSuite) TestDeleteDeviceWhenNotFound(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil,            // first call to GetSubObject succeeds (it can't fail anyway)
		s.maas404Error, // following call to Delete fails with 404 error.
		nil,            // second call to GetSubObject succeeds
		errors.New("does not compute"), // second call to Delete fails otherwise.
	)
	env := s.makeEnviron()

	err := env.deleteDevice("foo")
	c.Assert(err, gc.ErrorMatches, `device "foo" not found`)
	c.Check(err, jc.Satisfies, errors.IsNotFound)

	err = env.deleteDevice("bar")
	c.Assert(err, gc.ErrorMatches, `cannot delete device "bar": does not compute`)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "GetSubObject",
		Args:     []interface{}{"foo"},
	}, {
		FuncName: "Delete",
		Args:     nil,
	}, {
		FuncName: "GetSubObject",
		Args:     []interface{}{"bar"},
	}, {
		FuncName: "Delete",
		Args:     nil,
	}})
}

func (s *devicesSuite) TestDeleteDeviceSucceeds(c *gc.C) {
	env := s.makeEnviron()

	err := env.deleteDevice("valid")
	c.Assert(err, jc.ErrorIsNil)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "GetSubObject",
		Args:     []interface{}{"valid"},
	}, {
		FuncName: "Delete",
		Args:     nil,
	}})
}

func (s *devicesSuite) TestGetDeviceWhenNotFound(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil,               // first call to GetSubObject succeeds (it can't fail anyway)
		s.maas404Error,    // following call to Get fails with 404 error.
		nil,               // second call to GetSubObject succeeds
		errors.New("N/A"), // second call to Get fails otherwise.
	)
	env := s.makeEnviron()

	device, err := env.getDevice("widget")
	c.Assert(err, gc.ErrorMatches, `device "widget" not found`)
	c.Check(err, jc.Satisfies, errors.IsNotFound)
	c.Check(device, gc.IsNil)

	device, err = env.getDevice("X")
	c.Assert(err, gc.ErrorMatches, `cannot get device "X": N/A`)
	c.Check(device, gc.IsNil)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "GetSubObject",
		Args:     []interface{}{"widget"},
	}, {
		FuncName: "Get",
		Args:     nil,
	}, {
		FuncName: "GetSubObject",
		Args:     []interface{}{"X"},
	}, {
		FuncName: "Get",
		Args:     nil,
	}})
}

func (s *devicesSuite) TestGetDeviceSucceeds(c *gc.C) {
	env := s.makeEnviron()

	device, err := env.getDevice("boo")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(*device, jc.DeepEquals, s.fakeAPI.singleDevice)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "GetSubObject",
		Args:     []interface{}{"boo"},
	}, {
		FuncName: "Get",
		Args:     nil,
	}})
}

func (s *devicesSuite) TestSetupDeviceInterfacesEmptyConfig(c *gc.C) {

}

func (s *devicesSuite) TestSetupDeviceInterfacesMirroringHostInterfaces(c *gc.C) {

}

func (s *devicesSuite) TestClaimStickyIPForDeviceNotSupported(c *gc.C) {

}

func (s *devicesSuite) TestClaimStickyIPForDeviceWithoutExplicitAddress(c *gc.C) {

}

func (s *devicesSuite) TestClaimStickyIPForDeviceWithExplicitAddress(c *gc.C) {

}

type fakeDevicesAPI struct {
	*testing.Stub

	singleDevice maasDevice
	allDevices   []maasDevice
}

var (
	_ devicesAPI = (*devicesAPIShim)(nil)
	_ devicesAPI = (*fakeDevicesAPI)(nil)
)

func (f *fakeDevicesAPI) addCallAndGetResultOrError(callName string, args ...interface{}) (devicesAPI, error) {
	f.AddCall(callName, args...)
	if err := f.NextErr(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *fakeDevicesAPI) GetSubObject(uri string) devicesAPI {
	result, _ := f.addCallAndGetResultOrError("GetSubObject", uri)
	return result
}

func (f *fakeDevicesAPI) CallGet(op string, params url.Values) (devicesAPI, error) {
	return f.addCallAndGetResultOrError("CallGet", op, params)
}

func (f *fakeDevicesAPI) CallPost(op string, params url.Values) (devicesAPI, error) {
	return f.addCallAndGetResultOrError("CallPost", op, params)
}

func (f *fakeDevicesAPI) Delete() error {
	_, err := f.addCallAndGetResultOrError("Delete")
	return err
}

func (f *fakeDevicesAPI) Update(params url.Values) (devicesAPI, error) {
	return f.addCallAndGetResultOrError("Update", params)
}

func (f *fakeDevicesAPI) Get() (devicesAPI, error) {
	return f.addCallAndGetResultOrError("Get")
}

func (f *fakeDevicesAPI) MarshalJSON() ([]byte, error) {
	if err := f.NextErr(); err != nil {
		return nil, err
	}

	if calls := f.Calls(); len(calls) > 0 && calls[len(calls)-1].FuncName == "CallGet" {
		// Only CallGet can return more than one result, if it was the last call
		// made.
		return json.Marshal(f.allDevices)
	}
	return json.Marshal(f.singleDevice)
}
