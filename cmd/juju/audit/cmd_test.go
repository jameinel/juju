// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.
package audit

import (
	"bytes"
	"encoding/json"
	"net"
	"time"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	coretesting "github.com/juju/juju/testing"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
)

var _ = gc.Suite(&AuditCommandTestSuite{})

type AuditCommandTestSuite struct {
	testing.IsolationSuite

	stub   *testing.Stub
	client *stubAuditAPI
}

func (s *AuditCommandTestSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)

	s.stub = &testing.Stub{}
	s.client = &stubAuditAPI{stub: s.stub}
}

func (s *AuditCommandTestSuite) newAPIClient(c *auditCommand) (LogLister, error) {
	s.stub.AddCall("newAPIClient", c)
	if err := s.stub.NextErr(); err != nil {
		return nil, errors.Trace(err)
	}
	return s.client, nil
}

func (s *AuditCommandTestSuite) TestInfo(c *gc.C) {
	var command auditCommand
	info := command.Info()

	c.Check(info, jc.DeepEquals, &cmd.Info{
		Args:    "[options] <model name>",
		Name:    "audit",
		Purpose: usageSummary,
		Doc:     usageDetails,
	})
}

func (s *AuditCommandTestSuite) TestNoArgs(c *gc.C) {
	command := NewAuditCommand(s.client)
	args := []string{}
	code, stdout, stderr := runCmd(c, command, args...)
	c.Check(code, gc.Equals, 2)
	c.Check(stdout, gc.Equals, "")
	c.Check(stderr, gc.Equals, "error: missing model name\n")
}

func (s *AuditCommandTestSuite) TestMissingArgs(c *gc.C) {
	command := NewAuditCommand(s.client)
	args := []string{"a model", "--timestamp"}
	code, stdout, stderr := runCmd(c, command, args...)
	c.Check(code, gc.Equals, 2)
	c.Check(stdout, gc.Equals, "")
	c.Check(stderr, gc.Equals, "error: flag needs an argument: --timestamp\n")
}

func (s *AuditCommandTestSuite) TestExtraneousArgs(c *gc.C) {
	command := NewAuditCommand(s.client)
	args := []string{"a model", "foo", "bar"}
	code, stdout, stderr := runCmd(c, command, args...)
	c.Check(code, gc.Equals, 2)
	c.Check(stdout, gc.Equals, "")
	c.Check(stderr, gc.Equals, "error: unrecognized args: [\"foo\" \"bar\"]\n")
}

func (s *AuditCommandTestSuite) TestOuputFormat(c *gc.C) {
	records := []auditRecord{
		auditRecord{net.ParseIP("10.0.0.1"), "a model", "a name", "an operation", time.Date(2016, 03, 21, 16, 0, 0, 0, time.UTC), "a type"},
		auditRecord{net.ParseIP("10.0.0.2"), "another model", "another name", "another operation", time.Date(2016, 03, 21, 16, 30, 0, 0, time.UTC), "another type"},
	}
	s.client.ReturnAuditRecords = records

	wantJSON, err := jsonFormatted()
	if err != nil {
		c.Fatalf("the wanted json format couldn't be compacted: %s", err)
	}
	formats := map[string]string{
		"tabular": `
DATE	IP	SOURCE	OPERATION
2016-03-21 16:00:00 +0000 UTC	10.0.0.1	a type:a name	an operation
2016-03-21 16:30:00 +0000 UTC	10.0.0.2	another type:another name	another operation

`[1:],
		// We're really getting back stdout here, so there is a newline added
		// to the JSON.
		"json": wantJSON + "\n",
		"yaml": `
- ipaddress: 10.0.0.1
  modelid: a model
  name: a name
  operation: an operation
  timestamp: 2016-03-21T16:00:00Z
  type: a type
- ipaddress: 10.0.0.2
  modelid: another model
  name: another name
  operation: another operation
  timestamp: 2016-03-21T16:30:00Z
  type: another type
`[1:]}

	for format, expected := range formats {
		command := NewAuditCommand(s.client)
		args := []string{"--format", format, "a model"}
		code, stdout, stderr := runCmd(c, command, args...)
		c.Check(code, gc.Equals, 0)
		c.Check(stdout, gc.Equals, expected)
		c.Check(stderr, gc.Equals, "")
	}
}

// Helpers

func runCmd(c *gc.C, command cmd.Command, args ...string) (code int, stdout string, stderr string) {
	ctx := coretesting.Context(c)
	code = cmd.Main(command, ctx, args)
	stdout = string(ctx.Stdout.(*bytes.Buffer).Bytes())
	stderr = string(ctx.Stderr.(*bytes.Buffer).Bytes())
	return code, stdout, stderr
}

// Return the legible 'JSON' expectation without insignificant whitespace.
func jsonFormatted() (string, error) {
	var buf bytes.Buffer
	readable := []byte("" +
		"[" +
		"  {" +
		`    "ipAddress":"10.0.0.1",` +
		`    "modelID":"a model",` +
		`    "name":"a name",` +
		`    "operation":"an operation",` +
		`    "timestamp":"2016-03-21T16:00:00Z",` +
		`    "type":"a type"` +
		"  },{" +
		`    "ipAddress":"10.0.0.2",` +
		`    "modelID":"another model",` +
		`    "name":"another name",` +
		`    "operation":"another operation",` +
		`    "timestamp":"2016-03-21T16:30:00Z",` +
		`    "type":"another type"` +
		"  }" +
		" ]")
	if err := json.Compact(&buf, readable); err != nil {
		return "", errors.Trace(err)
	}
	return buf.String(), nil
}
