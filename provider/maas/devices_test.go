// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package maas

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"

	"launchpad.net/gomaasapi"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
)

type devicesSuite struct {
	providerSuite
}

var _ = gc.Suite(&devicesSuite{})

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
	env := s.makeEnviron()

	testProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, client")
	}))
	defer testProxy.Close()

	proxyURL, err := url.Parse(testProxy.URL)
	c.Assert(err, jc.ErrorIsNil)

	s.PatchValue(&s.testMAASObject.TestServer.Server.Config.Handler, httputil.NewSingleHostReverseProxy(proxyURL))

	devices, err := env.listDevices(nil, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(devices, jc.DeepEquals, nil)
}

func (s *devicesSuite) TestListDevicesWithSingleFilter(c *gc.C) {

}

func (s *devicesSuite) TestListDevicesWithMultipleFilters(c *gc.C) {

}

func (s *devicesSuite) TestCreateDeviceRequiresNonEmptyMACAddress(c *gc.C) {

}

func (s *devicesSuite) TestCreateDeviceWithOnlyOptionalParams(c *gc.C) {

}

func (s *devicesSuite) TestCreateDeviceWithAllParams(c *gc.C) {

}

func (s *devicesSuite) TestUpdateDeviceWhenNotFound(c *gc.C) {

}

func (s *devicesSuite) TestUpdateDeviceSkipsEmptyParams(c *gc.C) {

}

func (s *devicesSuite) TestDeleteDeviceWhenNotFound(c *gc.C) {

}

func (s *devicesSuite) TestDeleteDeviceSucceeds(c *gc.C) {

}

func (s *devicesSuite) TestGetDeviceWhenNotFound(c *gc.C) {

}

func (s *devicesSuite) TestGetDeviceSucceeds(c *gc.C) {

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

	callGetResult  interface{}
	callPostResult interface{}
	updateResult   interface{}
	getResult      interface{}
}

var (
	_ devicesAPI = (*gomaasapi.MAASObject)(nil)
	_ devicesAPI = (*fakeDevicesAPI)(nil)
)

func (f *fakeDevicesAPI) GetSubObject(uri string) devicesAPI {
	f.Stub.AddCall("GetSubObject", uri)
	f.Stub.PopNoErr()
	return f
}

func (f *fakeDevicesAPI) CallGet(op string, params url.Values) (gomaasapi.JSONObject, error) {
	f.Stub.AddCall("CallGet", op, params)
	return gomaasapi.JSONObjectFromStruct()
	if err := f.Stub.NextErr(); err != nil {
		return gomaasapi.JSONObject{}, err
	}
	return f
}
