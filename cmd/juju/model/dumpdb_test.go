// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for info.

package model_test

import (
	"encoding/json"

	"github.com/juju/cmd/v3/cmdtesting"
	"github.com/juju/names/v5"
	gitjujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/juju/model"
	coremodel "github.com/juju/juju/core/model"
	"github.com/juju/juju/jujuclient"
	"github.com/juju/juju/testing"
)

type DumpDBCommandSuite struct {
	testing.FakeJujuXDGDataHomeSuite
	fake  fakeDumpDBClient
	store *jujuclient.MemStore
}

var _ = gc.Suite(&DumpDBCommandSuite{})

func (s *DumpDBCommandSuite) SetUpTest(c *gc.C) {
	s.FakeJujuXDGDataHomeSuite.SetUpTest(c)
	s.fake.ResetCalls()
	s.fake.result = nil
	s.store = jujuclient.NewMemStore()
	s.store.CurrentControllerName = "testing"
	s.store.Controllers["testing"] = jujuclient.ControllerDetails{}
	s.store.Accounts["testing"] = jujuclient.AccountDetails{
		User: "admin",
	}
	err := s.store.UpdateModel("testing", "admin/mymodel", jujuclient.ModelDetails{
		ModelUUID: testing.ModelTag.Id(),
		ModelType: coremodel.IAAS,
	})
	c.Assert(err, jc.ErrorIsNil)
	s.store.Models["testing"].CurrentModel = "admin/mymodel"
}

func (s *DumpDBCommandSuite) TestDumpDB(c *gc.C) {
	ctx, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store))
	c.Assert(err, jc.ErrorIsNil)
	s.fake.CheckCalls(c, []gitjujutesting.StubCall{
		{"DumpModelDB", []interface{}{testing.ModelTag}},
		{"Close", nil},
	})

	out := cmdtesting.Stdout(ctx)
	c.Assert(out, gc.Equals, `all-others: heaps of data
models:
  name: testing
  uuid: fake-uuid
`)
}

func (s *DumpDBCommandSuite) TestDumpDBSanitize(c *gc.C) {
	// Keep this list independent of the implementation so changes to the
	// fields in scripts/sanitise-db.py require updating the command as well.
	fields := map[string][]string{
		"users":              {"secretkey", "passwordhash", "passwordsalt"},
		"units":              {"passwordhash"},
		"machines":           {"passwordhash"},
		"applications":       {"metric-credentials", "passwordhash"},
		"models":             {"passwordhash", "sla"},
		"controllerNodes":    {"password-hash"},
		"settings":           {"settings"},
		"controllers":        {"settings", "cert", "privatekey", "caprivatekey", "sharedsecret", "systemidentity", "key", "local-users-key", "local-users-thirdparty-key", "external-users-thirdparty-key", "offers-thirdparty-key"},
		"actions":            {"parameters", "message", "results", "messages"},
		"cloudCredentials":   {"attributes"},
		"dockerResources":    {"password"},
		"sshrequests":        {"password"},
		"virtualhostkeys":    {"hostkey"},
		"autocertCache":      {"data"},
		"bakeryStorageItems": {"rootkey", "item"},
		"remoteEntities":     {"token", "macaroon"},
		"remoteApplications": {"macaroon"},
		"migrations":         {"target-password", "target-macaroons", "target-token"},
		"secretRevisions":    {"data"},
		"secretBackends":     {"config"},
		"statuses":           {"statusinfo", "statusdata"},
		"statuseshistory":    {"statusinfo", "statusdata"},
	}
	s.fake.result = map[string]interface{}{"other": []interface{}{map[string]interface{}{"name": "unchanged"}}}
	for collection, attributes := range fields {
		doc := map[string]interface{}{"_id": "unchanged"}
		for _, name := range attributes {
			doc[name] = map[string]interface{}{"nested": "secret"}
		}
		if collection == "models" {
			s.fake.result[collection] = doc
		} else {
			s.fake.result[collection] = []interface{}{doc, map[string]interface{}{"_id": "second"}}
		}
	}
	ctx, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store), "--sanitize", "--format=json")
	c.Assert(err, jc.ErrorIsNil)
	var output map[string]interface{}
	c.Assert(json.Unmarshal([]byte(cmdtesting.Stdout(ctx)), &output), jc.ErrorIsNil)
	for collection, attributes := range fields {
		var doc map[string]interface{}
		if collection == "models" {
			doc = output[collection].(map[string]interface{})
		} else {
			docs := output[collection].([]interface{})
			c.Check(docs[1].(map[string]interface{})["_id"], gc.Equals, "second")
			doc = docs[0].(map[string]interface{})
		}
		c.Check(doc["_id"], gc.Equals, "unchanged")
		for _, name := range attributes {
			c.Check(doc[name], gc.Equals, "REDACTED", gc.Commentf("%s.%s", collection, name))
		}
	}
	c.Check(output["other"].([]interface{})[0].(map[string]interface{})["name"], gc.Equals, "unchanged")
}

func (s *DumpDBCommandSuite) TestDumpDBKeepLowSensitivityData(c *gc.C) {
	s.fake.result = map[string]interface{}{
		"actions": []interface{}{map[string]interface{}{
			"parameters": "secret", "messages": "action log", "message": "secret",
		}},
		"statuses": []interface{}{map[string]interface{}{"statusinfo": "status", "statusdata": "data"}},
	}
	ctx, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store),
		"--sanitize", "--keep-low-sensitivity-data", "--format=json")
	c.Assert(err, jc.ErrorIsNil)
	var output map[string]interface{}
	c.Assert(json.Unmarshal([]byte(cmdtesting.Stdout(ctx)), &output), jc.ErrorIsNil)
	action := output["actions"].([]interface{})[0].(map[string]interface{})
	c.Check(action["parameters"], gc.Equals, "REDACTED")
	c.Check(action["message"], gc.Equals, "REDACTED")
	c.Check(action["messages"], gc.Equals, "action log")
	status := output["statuses"].([]interface{})[0].(map[string]interface{})
	c.Check(status["statusinfo"], gc.Equals, "status")
	c.Check(status["statusdata"], gc.Equals, "data")
}

func (s *DumpDBCommandSuite) TestDumpDBSanitizeYAML(c *gc.C) {
	s.fake.result = map[string]interface{}{
		"models": map[string]interface{}{"name": "testing", "passwordhash": "secret"},
		"units":  []map[string]interface{}{{"_id": "unit-0", "passwordhash": "secret"}, {"_id": "unit-1"}},
	}
	ctx, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store), "--sanitize")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(cmdtesting.Stdout(ctx), gc.Equals, `models:
  name: testing
  passwordhash: REDACTED
units:
- _id: unit-0
  passwordhash: REDACTED
- _id: unit-1
`)
}

func (s *DumpDBCommandSuite) TestDumpDBWithoutSanitize(c *gc.C) {
	s.fake.result = map[string]interface{}{"models": map[string]interface{}{"passwordhash": "secret"}}
	ctx, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store), "--format=json")
	c.Assert(err, jc.ErrorIsNil)
	var output map[string]interface{}
	c.Assert(json.Unmarshal([]byte(cmdtesting.Stdout(ctx)), &output), jc.ErrorIsNil)
	c.Check(output["models"].(map[string]interface{})["passwordhash"], gc.Equals, "secret")
}

func (s *DumpDBCommandSuite) TestDumpDBSanitizeUnexpectedDocument(c *gc.C) {
	s.fake.result = map[string]interface{}{"units": []interface{}{"unexpected"}}
	ctx, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store), "--sanitize")
	c.Assert(err, gc.ErrorMatches, `unexpected document in "units" collection: string`)
	c.Check(cmdtesting.Stdout(ctx), gc.Equals, "")
}

func (s *DumpDBCommandSuite) TestDumpDBKeepLowSensitivityDataRequiresSanitize(c *gc.C) {
	_, err := cmdtesting.RunCommand(c, model.NewDumpDBCommandForTest(&s.fake, s.store), "--keep-low-sensitivity-data")
	c.Assert(err, gc.ErrorMatches, `.*--keep-low-sensitivity-data requires --sanitize.*`)
}

type fakeDumpDBClient struct {
	gitjujutesting.Stub
	result map[string]interface{}
}

func (f *fakeDumpDBClient) Close() error {
	f.MethodCall(f, "Close")
	return f.NextErr()
}

func (f *fakeDumpDBClient) DumpModelDB(model names.ModelTag) (map[string]interface{}, error) {
	f.MethodCall(f, "DumpModelDB", model)
	err := f.NextErr()
	if err != nil {
		return nil, err
	}
	if f.result != nil {
		return f.result, nil
	}
	return map[string]interface{}{
		"models": map[string]interface{}{
			"name": "testing",
			"uuid": "fake-uuid",
		},
		"all-others": "heaps of data",
	}, nil
}
