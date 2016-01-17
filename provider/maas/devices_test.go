// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"launchpad.net/gomaasapi"
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

// parseDevicesSuite tests parseDevices with different inputs.
type parseDevicesSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&parseDevicesSuite{})

type badJSONData struct{}

func (b *badJSONData) MarshalJSON() ([]byte, error) {
	return nil, errors.New("cannot marshal")
}

func (s *parseDevicesSuite) TestNilJSON(c *gc.C) {
	result, err := parseDevices(nil)
	c.Check(err, gc.ErrorMatches, "nil JSON data")
	c.Check(result, gc.IsNil)
}

func (s *parseDevicesSuite) TestMarshalJSONFails(c *gc.C) {
	data := &badJSONData{}

	result, err := parseDevices(data)
	c.Check(err, gc.ErrorMatches, "cannot get JSON bytes: cannot marshal")
	c.Check(result, gc.IsNil)
}

func (s *parseDevicesSuite) TestBadJSON(c *gc.C) {

	data := json.RawMessage([]byte("$bad"))

	result, err := parseDevices(&data)
	c.Check(err, gc.ErrorMatches, `parsing devices: invalid character '\$' .*`)
	c.Check(result, gc.IsNil)

	data = json.RawMessage([]byte("[true]"))

	result, err = parseDevices(&data)
	c.Check(err, gc.ErrorMatches, `parsing device: json: cannot unmarshal array into Go value .*`)
	c.Check(result, gc.IsNil)
}

func (s *parseDevicesSuite) TestSuccessExampleJSONSingleDevice(c *gc.C) {
	data := json.RawMessage([]byte(exampleSingleDeviceNoParentJSON))

	result, err := parseDevices(&data)
	c.Check(err, jc.ErrorIsNil)
	c.Check(result, jc.DeepEquals, []maasDevice{expectedSingleDeviceNoParent})

	data = json.RawMessage([]byte(exampleSingleDeviceWithParentJSON))

	result, err = parseDevices(&data)
	c.Check(err, jc.ErrorIsNil)
	c.Check(result, jc.DeepEquals, []maasDevice{expectedSingleDeviceWithParent})
}

func (s *parseDevicesSuite) TestSuccessExampleJSONDeviceList(c *gc.C) {
	data := json.RawMessage([]byte(exampleDevicesListJSON))
	result, err := parseDevices(&data)
	c.Check(err, jc.ErrorIsNil)

	expectedDevices := []maasDevice{
		expectedSingleDeviceNoParent,
		expectedSingleDeviceWithParent,
	}
	c.Check(result, jc.DeepEquals, expectedDevices)
}

// devicesFakeAPISuite tests the internal details of how devicesAPI calls are
// implemented, using stubs to verify call order and arguments.
type devicesFakeAPISuite struct {
	providerSuite

	fakeAPI      *fakeDevicesAPI
	maas404Error error
}

var _ = gc.Suite(&devicesFakeAPISuite{})

func (s *devicesFakeAPISuite) SetUpTest(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestListDevicesNoFilters(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil, // first call to CallGet succeeds
		nil, // subsequent call to MarshalJSON also succeeds
		errors.New("you require more vespene gas"), // second call to CallGet fails
		nil, // third call to CallGet succeds
		errors.New("we need more pylons"), // but the following MarshalJSON fails
	)
	env := s.makeEnviron()

	devices, err := env.listDevices(nil, nil, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	devices, err = env.listDevices(nil, nil, nil, nil)
	c.Assert(err, gc.ErrorMatches, "fetching devices result: you require more vespene gas")
	c.Check(devices, gc.IsNil)

	devices, err = env.listDevices(nil, nil, nil, nil)
	c.Assert(err, gc.ErrorMatches, "cannot get JSON bytes: we need more pylons")
	c.Check(devices, gc.IsNil)

	expectedCall := testing.StubCall{
		FuncName: "CallGet",
		Args:     []interface{}{"list", url.Values{}},
	}
	s.fakeAPI.CheckCalls(c, []testing.StubCall{expectedCall, expectedCall, expectedCall})
}

func (s *devicesFakeAPISuite) TestListDevicesWithSingleFilter(c *gc.C) {
	env := s.makeEnviron()

	hostFilters := []string{"host1", "host2", "host3"}
	devices, err := env.listDevices(hostFilters[0:1], nil, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	devices, err = env.listDevices(hostFilters, nil, nil, nil)
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

func (s *devicesFakeAPISuite) TestListDevicesWithAllFilters(c *gc.C) {
	env := s.makeEnviron()

	hostFilters := []string{"host1", "host2", "host3"}
	macFilters := []string{"mac1", "mac2", "mac3"}
	parentFilters := []string{"node1", "node2"}
	idFilters := []string{"id1", "id2", "id3"}
	devices, err := env.listDevices(hostFilters[0:1], macFilters[0:1], parentFilters[0:1], idFilters[0:1])
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	devices, err = env.listDevices(hostFilters, macFilters, parentFilters, idFilters)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, s.fakeAPI.allDevices)

	s.fakeAPI.CheckCalls(c, []testing.StubCall{{
		FuncName: "CallGet",
		Args: []interface{}{
			"list",
			url.Values{
				"hostname":    hostFilters[0:1],
				"mac_address": macFilters[0:1],
				"parent":      parentFilters[0:1],
				"id":          idFilters[0:1],
			}},
	}, {
		FuncName: "CallGet",
		Args: []interface{}{
			"list",
			url.Values{
				"hostname":    hostFilters,
				"mac_address": macFilters,
				"parent":      parentFilters,
				"id":          idFilters,
			}},
	}})
}

func (s *devicesFakeAPISuite) TestCreateDeviceRequiresNonEmptyMACAddress(c *gc.C) {
	env := s.makeEnviron()

	device, err := env.createDevice("", "", "")
	c.Assert(err, gc.ErrorMatches, "cannot create a device with empty MAC address")
	c.Check(device, gc.IsNil)

	device, err = env.createDevice("parent", "", "host")
	c.Assert(err, gc.ErrorMatches, "cannot create a device with empty MAC address")
	c.Check(device, gc.IsNil)

	s.fakeAPI.CheckNoCalls(c)
}

func (s *devicesFakeAPISuite) TestCreateDeviceWithOnlyOptionalParams(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestCreateDeviceWithAllParams(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestUpdateDeviceWhenNotFound(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestUpdateDeviceSkipsEmptyParams(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestDeleteDeviceWhenNotFound(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestDeleteDeviceSucceeds(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestGetDeviceErrors(c *gc.C) {
	s.fakeAPI.SetErrors(
		nil,               // first call to GetSubObject succeeds (it can't fail anyway)
		s.maas404Error,    // following call to Get fails with 404 error.
		nil,               // second call to GetSubObject succeeds
		errors.New("N/A"), // second call to Get fails with some other error.
	)
	env := s.makeEnviron()

	device, err := env.getDevice("widget")
	c.Assert(err, gc.ErrorMatches, `device "widget" not found`)
	c.Check(err, jc.Satisfies, errors.IsNotFound)
	c.Check(device, gc.IsNil)

	device, err = env.getDevice("X")
	c.Assert(err, gc.ErrorMatches, `cannot get device "X": N/A`)
	c.Check(device, gc.IsNil)

	// force the next Get() result to return no error and a result with cannot
	// be marshaled into JSON, so that the following parseDevices call inside
	// getDevice will fail.
	s.fakeAPI.singleDevice = make(chan int)
	device, err = env.getDevice("Y")
	c.Assert(err, gc.ErrorMatches, "cannot get JSON bytes: json: unsupported type: chan int")
	c.Check(device, gc.IsNil)

	// We expect 6 calls - 2 for each getDevice call, the only difference
	// between them is the ID.
	expectedCalls := make([]testing.StubCall, 0, 6)
	for _, id := range []string{"widget", "X", "Y"} {
		expectedCalls = append(expectedCalls, testing.StubCall{
			FuncName: "GetSubObject",
			Args:     []interface{}{id},
		}, testing.StubCall{
			FuncName: "Get",
			Args:     nil,
		})
	}

	s.fakeAPI.CheckCalls(c, expectedCalls)
}

func (s *devicesFakeAPISuite) TestGetDeviceSucceeds(c *gc.C) {
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

func (s *devicesFakeAPISuite) TestSetupDeviceInterfacesEmptyConfig(c *gc.C) {

}

func (s *devicesFakeAPISuite) TestSetupDeviceInterfacesMirroringHostInterfaces(c *gc.C) {

}

func (s *devicesFakeAPISuite) TestClaimStickyIPForDeviceNotSupported(c *gc.C) {

}

func (s *devicesFakeAPISuite) TestClaimStickyIPForDeviceWithoutExplicitAddress(c *gc.C) {

}

func (s *devicesFakeAPISuite) TestClaimStickyIPForDeviceWithExplicitAddress(c *gc.C) {

}

// fakeDevicesAPI implements a stub devicesAPI for use in devicesFakeAPISuite.
type fakeDevicesAPI struct {
	*testing.Stub

	singleDevice interface{}
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
		// Only CallGet can return more than one result, if that was the last
		// call made.
		return json.Marshal(f.allDevices)
	}
	return json.Marshal(f.singleDevice)
}

// devicesAPIShimSuite tests the internals of devicesAPIShim, verifying we
// correctly pass requests to gomaasapi and we don't mask any error responses.
type devicesAPIShimSuite struct {
	providerSuite

	env           *maasEnviron
	devicesClient gomaasapi.MAASObject
	createdShim   devicesAPI
}

var _ = gc.Suite(&devicesAPIShimSuite{})

func (s *devicesAPIShimSuite) SetUpTest(c *gc.C) {
	s.providerSuite.SetUpTest(c)
	s.env = s.makeEnviron()
	client := s.env.getMAASClient()
	s.devicesClient = client.GetSubObject("devices")
	s.createdShim = devicesClient(s.env)
	s.assertShim(c, s.createdShim, &devicesAPIShim{MAASObject: &s.devicesClient})
}

func (s *devicesAPIShimSuite) assertShim(c *gc.C, gotShim interface{}, expectedShim *devicesAPIShim) {
	c.Assert(gotShim, gc.NotNil)
	c.Assert(gotShim, gc.FitsTypeOf, &devicesAPIShim{})
	c.Assert(gotShim.(*devicesAPIShim), jc.DeepEquals, expectedShim)
}

func (s *devicesAPIShimSuite) TestGetSubObject(c *gc.C) {
	clientResult := s.devicesClient.GetSubObject("foo")
	shimResult := s.createdShim.GetSubObject("foo")
	s.assertShim(c, shimResult, &devicesAPIShim{MAASObject: &clientResult})
}

func (s *devicesAPIShimSuite) TestCallGet(c *gc.C) {
	// Verify we don't obscure the error cause.
	shimResult, err := s.createdShim.CallGet("bad", nil)
	c.Assert(shimResult, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `gomaasapi: got error back from server: 400 Bad Request .*`)
	c.Assert(err, gc.FitsTypeOf, gomaasapi.ServerError{})
	c.Assert(getMAASErrorCode(err), gc.Equals, 400)

	// Otherwise verify we get the same result through the shim and the test
	// client.
	clientResult, err := s.devicesClient.CallGet("list", nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(clientResult.IsNil(), jc.IsFalse)
	shimResult, err = s.createdShim.CallGet("list", nil)
	c.Assert(err, jc.ErrorIsNil)
	s.assertShim(c, shimResult, &devicesAPIShim{JSONObject: &clientResult})
}

func (s *devicesAPIShimSuite) TestCallPost(c *gc.C) {
	// Verify we don't obscure the error cause.
	shimBadResult, err := s.createdShim.CallPost("bad", nil)
	c.Assert(shimBadResult, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `gomaasapi: got error back from server: 400 Bad Request .*`)
	c.Assert(err, gc.FitsTypeOf, gomaasapi.ServerError{})
	c.Assert(getMAASErrorCode(err), gc.Equals, 400)

	// Otherwise verify creating a device through the shim and following get
	// through the client (filtered by MAC address) produces same result.
	params := make(url.Values)
	params.Add("hostname", "host")
	params.Add("parent", "parent")
	params.Add("mac_addresses", "mac")
	shimPostResult, err := s.createdShim.CallPost("new", params)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(shimPostResult, gc.NotNil)
	c.Assert(shimPostResult, gc.FitsTypeOf, &devicesAPIShim{})
	shimResult, err := parseDevices(shimPostResult.(*devicesAPIShim))
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(shimResult, gc.HasLen, 1)

	clientGetResult, err := s.devicesClient.GetSubObject(shimResult[0].SystemID).Get()
	c.Assert(err, jc.ErrorIsNil)
	clientResult, err := parseDevices(clientGetResult)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(clientResult, gc.HasLen, 1)
	c.Assert(shimResult, jc.DeepEquals, clientResult)
}

func (s *devicesAPIShimSuite) TestUpdate(c *gc.C) {
	// Just verify we don't obscure the error cause and get the same error for
	// Update() through the shim and the test client.
	clientResult, err := s.devicesClient.GetSubObject("foo").Update(nil)
	c.Assert(err.Error(), jc.HasPrefix, "gomaasapi: got error back from server: 404 Not Found")
	c.Check(getMAASErrorCode(err), gc.Equals, 404)
	c.Check(clientResult, jc.DeepEquals, gomaasapi.MAASObject{})

	shimResult, err := s.createdShim.GetSubObject("foo").Update(nil)
	c.Assert(err.Error(), jc.HasPrefix, "gomaasapi: got error back from server: 404 Not Found")
	c.Check(getMAASErrorCode(err), gc.Equals, 404)
	c.Check(shimResult, gc.IsNil)
}

func (s *devicesAPIShimSuite) TestGet(c *gc.C) {
	// Just verify we don't obscure the error cause and get the same error for
	// Get() through the shim and the test client.
	clientResult, err := s.devicesClient.GetSubObject("foo").Get()
	c.Assert(err.Error(), jc.HasPrefix, "gomaasapi: got error back from server: 404 Not Found")
	c.Check(getMAASErrorCode(err), gc.Equals, 404)
	c.Check(clientResult, jc.DeepEquals, gomaasapi.MAASObject{})

	shimResult, err := s.createdShim.GetSubObject("foo").Get()
	c.Assert(err.Error(), jc.HasPrefix, "gomaasapi: got error back from server: 404 Not Found")
	c.Check(getMAASErrorCode(err), gc.Equals, 404)
	c.Check(shimResult, gc.IsNil)
}

func (s *devicesAPIShimSuite) TestMarshalJSON(c *gc.C) {
	shim := &devicesAPIShim{MAASObject: nil, JSONObject: nil}
	result, err := shim.MarshalJSON()
	c.Assert(err, gc.ErrorMatches, "no object available to marshal")
	c.Check(result, gc.IsNil)

	// Verify with the embedded MAASObject set we get the same result as
	// serializiing a maasDevice with the same SystemID.
	maasObj := s.testMAASObject.TestServer.NewNode(`{"system_id":"foo"}`)
	shim = &devicesAPIShim{MAASObject: &maasObj, JSONObject: nil}
	maasObjResult, err := shim.MarshalJSON()
	c.Assert(err, jc.ErrorIsNil)
	c.Check(maasObjResult, gc.NotNil)

	var maasObjDevice maasDevice
	err = json.Unmarshal(maasObjResult, &maasObjDevice)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(maasObjDevice.SystemID, gc.Equals, "foo")

	// Now try the same with a populated JSONObject.
	// The hard part is getting the client the test server uses in order to be
	// able to create a JSONObject.
	client, err := gomaasapi.NewAnonymousClient(s.testMAASObject.TestServer.URL, "1.0")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(client, gc.NotNil)
	jsonObj, err := gomaasapi.JSONObjectFromStruct(*client, maasDevice{SystemID: "bar"})
	c.Assert(err, jc.ErrorIsNil)

	// When JSONObject is set we call its MarshalJSON and ignore MAASObject.
	shim = &devicesAPIShim{MAASObject: nil, JSONObject: &jsonObj}
	jsonObjResult, err := shim.MarshalJSON()
	c.Assert(err, jc.ErrorIsNil)
	c.Check(jsonObjResult, gc.NotNil)

	var jsonObjDevice maasDevice
	err = json.Unmarshal(jsonObjResult, &jsonObjDevice)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(jsonObjDevice.SystemID, gc.Equals, "bar")

	// Verify with both objects set we still get the result of
	// JSONObject.MarshalJSON.
	shim = &devicesAPIShim{MAASObject: &maasObj, JSONObject: &jsonObj}
	result, err = shim.MarshalJSON()
	c.Assert(err, jc.ErrorIsNil)
	c.Check(result, gc.NotNil)

	var device maasDevice
	err = json.Unmarshal(result, &device)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(device, jc.DeepEquals, jsonObjDevice)
}

// devicesIntegrationSuite tests as much as possible of the end-to-end devices
// API handling using the gomaasapi test server.
type devicesIntegrationSuite struct {
	providerSuite

	env *maasEnviron
}

var _ = gc.Suite(&devicesIntegrationSuite{})

func (s *devicesIntegrationSuite) SetUpTest(c *gc.C) {
	s.providerSuite.SetUpTest(c)
	s.env = s.makeEnviron()
}

func (s *devicesIntegrationSuite) addDevice(c *gc.C) maasDevice {
	// Create a device first, then get it back and compare.
	device, err := s.env.createDevice("parent", "mac", "host")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(device, gc.NotNil)
	return *device
}

func (s *devicesIntegrationSuite) TestCreateDevice(c *gc.C) {
	// The test server does not provide a NewDevice call, so we need to create a
	// couple of devices using createDevice first. However, the gomaasapi
	// newDeviceHandler requires parent, mac address and hostname to be all set.
	device, err := s.env.createDevice("parent", "mac", "host")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(device, gc.NotNil)
	c.Check(device.ParentID, gc.NotNil)
	c.Check(*device.ParentID, gc.Equals, "parent")
	c.Check(device.MACAddresses, gc.HasLen, 1)
	c.Check(device.MACAddresses[0].Value, gc.Equals, "mac")
	c.Check(device.Hostname, gc.Equals, "host")
}

func (s *devicesIntegrationSuite) TestGetDevice(c *gc.C) {
	// Create a device first, then get it back and compare.
	device := s.addDevice(c)

	gotDevice, err := s.env.getDevice(device.SystemID)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(*gotDevice, jc.DeepEquals, device)

	// Check we get 404 for unknown IDs.
	missingDevice, err := s.env.getDevice("foo")
	c.Assert(err, jc.Satisfies, errors.IsNotFound)
	c.Check(err, gc.ErrorMatches, `device "foo" not found`)
	c.Check(missingDevice, gc.IsNil)
}

func (s *devicesIntegrationSuite) TestUpdateDevice(c *gc.C) {
	device := s.addDevice(c)

	// We can't test updateDevice successfully as the test server does not
	// support PUT for devices, but we can test at least the request goes
	// through and we get 404 (as with any not implemented handler) for both
	// known and unknown IDs.
	checkNotFound := func(systemID string) {
		err := s.env.updateDevice(maasDevice{SystemID: systemID})
		c.Assert(err, jc.Satisfies, errors.IsNotFound)
		c.Check(err, gc.ErrorMatches, fmt.Sprintf("device %q not found", systemID))
	}

	checkNotFound(device.SystemID)
	checkNotFound("foo")
}

func (s *devicesIntegrationSuite) TestDeleteDevice(c *gc.C) {
	device := s.addDevice(c)

	err := s.env.deleteDevice(device.SystemID)
	c.Assert(err, jc.ErrorIsNil)

	// Check we get 404 for both the just deleted device and for unknown IDs.
	checkNotFound := func(systemID string) {
		err := s.env.deleteDevice(systemID)
		c.Assert(err, jc.Satisfies, errors.IsNotFound)
		c.Check(err, gc.ErrorMatches, fmt.Sprintf("device %q not found", systemID))
	}

	checkNotFound(device.SystemID)
	checkNotFound("foo")
}

func (s *devicesIntegrationSuite) TestListDevices(c *gc.C) {
	// Create a couple of devices first.
	device1 := s.addDevice(c)
	device2, err := s.env.createDevice("parent2", "mac2", "host2")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(device2, gc.NotNil)

	// Now list first all devices, then with a mac address filter matching
	// device2. The test server does not support other filters yet.
	devices, err := s.env.listDevices(nil, nil, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	// We're getting the results out of a map, so the order might differ.
	c.Check(devices, gc.HasLen, 2)
	expected := []maasDevice{device1, *device2}
	if devices[0].SystemID == device2.SystemID {
		expected[0], expected[1] = expected[1], expected[0]
	}
	c.Check(devices, jc.DeepEquals, expected)

	devices, err = s.env.listDevices(nil, []string{"mac2"}, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(devices, jc.DeepEquals, []maasDevice{*device2})
}
