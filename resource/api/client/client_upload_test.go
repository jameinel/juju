// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package client_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	charmresource "gopkg.in/juju/charm.v6-unstable/resource"

	"github.com/juju/juju/resource/api/client"
)

var _ = gc.Suite(&UploadSuite{})

type UploadSuite struct {
	BaseSuite
}

func (s *UploadSuite) TestOkay(c *gc.C) {
	s.response = "success"
	data := "<data>"
	fp, err := charmresource.GenerateFingerprint(strings.NewReader(data))
	c.Assert(err, jc.ErrorIsNil)
	req, err := http.NewRequest("PUT", "/services/a-service/resources/spam", nil)
	c.Assert(err, jc.ErrorIsNil)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-SHA384", fp.String())
	req.Header.Set("Content-Length", fmt.Sprint(len(data)))
	req.ContentLength = int64(len(data))
	reader := &stubFile{stub: s.stub}
	reader.returnRead = strings.NewReader(data)
	cl := client.NewClient(s.facade, s, s.facade)

	err = cl.Upload("a-service", "spam", reader)
	c.Assert(err, jc.ErrorIsNil)

	s.stub.CheckCallNames(c, "Read", "Read", "Seek", "Do")
	resp := "success"
	s.stub.CheckCall(c, 3, "Do", req, reader, &resp)
}

func (s *UploadSuite) TestBadService(c *gc.C) {
	cl := client.NewClient(s.facade, s, s.facade)

	err := cl.Upload("???", "spam", nil)

	c.Check(err, gc.ErrorMatches, `.*invalid service.*`)
	s.stub.CheckNoCalls(c)
}

func (s *UploadSuite) TestBadRequest(c *gc.C) {
	reader := &stubFile{stub: s.stub}
	cl := client.NewClient(s.facade, s, s.facade)
	failure := errors.New("<failure>")
	s.stub.SetErrors(failure)

	err := cl.Upload("a-service", "spam", reader)

	c.Check(errors.Cause(err), gc.Equals, failure)
	s.stub.CheckCallNames(c, "Read")
}

func (s *UploadSuite) TestRequestFailed(c *gc.C) {
	reader := &stubFile{stub: s.stub}
	reader.returnRead = strings.NewReader("<data>")
	cl := client.NewClient(s.facade, s, s.facade)
	failure := errors.New("<failure>")
	s.stub.SetErrors(nil, nil, nil, failure)

	err := cl.Upload("a-service", "spam", reader)

	c.Check(errors.Cause(err), gc.Equals, failure)
	s.stub.CheckCallNames(c, "Read", "Read", "Seek", "Do")
}

type stubFile struct {
	stub *testing.Stub

	returnRead io.Reader
	returnSeek int64
}

func (s *stubFile) Read(buf []byte) (int, error) {
	s.stub.AddCall("Read", buf)
	if err := s.stub.NextErr(); err != nil {
		return 0, errors.Trace(err)
	}

	return s.returnRead.Read(buf)
}

func (s *stubFile) Seek(offset int64, whence int) (int64, error) {
	s.stub.AddCall("Seek", offset, whence)
	if err := s.stub.NextErr(); err != nil {
		return 0, errors.Trace(err)
	}

	return s.returnSeek, nil
}

func (s *stubFile) Close() error {
	s.stub.AddCall("Close")
	if err := s.stub.NextErr(); err != nil {
		return errors.Trace(err)
	}

	return nil
}
