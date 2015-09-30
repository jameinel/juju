// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package apiserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/juju/errors"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/apiserver"
	apiserverbackups "github.com/juju/juju/apiserver/backups"
	apihttp "github.com/juju/juju/apiserver/http"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/backups"
	backupstesting "github.com/juju/juju/state/backups/testing"
)

type baseBackupsSuite struct {
	authHttpSuite
	fake *backupstesting.FakeBackups
}

func (s *baseBackupsSuite) SetUpTest(c *gc.C) {
	s.authHttpSuite.SetUpTest(c)

	s.fake = &backupstesting.FakeBackups{}
	s.PatchValue(apiserver.NewBackups,
		func(st *state.State) (backups.Backups, io.Closer) {
			return s.fake, ioutil.NopCloser(nil)
		},
	)
}

func (s *baseBackupsSuite) backupURL(c *gc.C) string {
	environ, err := s.State.Environment()
	c.Assert(err, jc.ErrorIsNil)
	uri := s.baseURL(c)
	uri.Path = fmt.Sprintf("/environment/%s/backups", environ.UUID())
	return uri.String()
}

func (s *baseBackupsSuite) checkErrorResponse(c *gc.C, resp *http.Response, statusCode int, msg string) {
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, jc.ErrorIsNil)

	c.Check(resp.StatusCode, gc.Equals, statusCode, gc.Commentf("body: %s", body))
	c.Check(resp.Header.Get("Content-Type"), gc.Equals, apihttp.CTypeJSON)

	var failure params.Error
	err = json.Unmarshal(body, &failure)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(&failure, gc.ErrorMatches, msg, gc.Commentf("body: %s", body))
}

type backupsSuite struct {
	baseBackupsSuite
}

var _ = gc.Suite(&backupsSuite{})

func (s *backupsSuite) TestRequiresAuth(c *gc.C) {
	resp := s.sendRequest(c, httpRequestParams{method: "GET", url: s.backupURL(c)})
	s.checkErrorResponse(c, resp, http.StatusUnauthorized, "no authorization header found")
}

func (s *backupsSuite) checkInvalidMethod(c *gc.C, method, url string) {
	resp := s.authRequest(c, httpRequestParams{method: method, url: url})
	s.checkErrorResponse(c, resp, http.StatusMethodNotAllowed, `unsupported method: "`+method+`"`)
}

func (s *backupsSuite) TestInvalidHTTPMethods(c *gc.C) {
	url := s.backupURL(c)
	for _, method := range []string{"POST", "DELETE", "OPTIONS"} {
		c.Log("testing HTTP method: " + method)
		s.checkInvalidMethod(c, method, url)
	}
}

func (s *backupsSuite) TestAuthRequiresClientNotMachine(c *gc.C) {
	// Add a machine and try to login.
	machine, err := s.State.AddMachine("quantal", state.JobHostUnits)
	c.Assert(err, jc.ErrorIsNil)
	err = machine.SetProvisioned("foo", "fake_nonce", nil)
	c.Assert(err, jc.ErrorIsNil)
	password, err := utils.RandomPassword()
	c.Assert(err, jc.ErrorIsNil)
	err = machine.SetPassword(password)
	c.Assert(err, jc.ErrorIsNil)

	resp := s.sendRequest(c, httpRequestParams{
		tag:      machine.Tag().String(),
		password: password,
		method:   "GET",
		url:      s.backupURL(c),
		nonce:    "fake_nonce",
	})
	s.checkErrorResponse(c, resp, http.StatusUnauthorized, "invalid entity name or password")

	// Now try a user login.
	resp = s.authRequest(c, httpRequestParams{method: "POST", url: s.backupURL(c)})
	s.checkErrorResponse(c, resp, http.StatusMethodNotAllowed, `unsupported method: "POST"`)
}

type backupsDownloadSuite struct {
	baseBackupsSuite
	body []byte
}

var _ = gc.Suite(&backupsDownloadSuite{})

func (s *backupsDownloadSuite) newBody(c *gc.C, id string) *bytes.Buffer {
	args := params.BackupsDownloadArgs{
		ID: id,
	}
	body, err := json.Marshal(args)
	c.Assert(err, jc.ErrorIsNil)
	return bytes.NewBuffer(body)
}

func (s *backupsDownloadSuite) sendValid(c *gc.C) *http.Response {
	meta := backupstesting.NewMetadata()
	archive, err := backupstesting.NewArchiveBasic(meta)
	c.Assert(err, jc.ErrorIsNil)
	s.fake.Meta = meta
	s.fake.Archive = ioutil.NopCloser(archive)
	s.body = archive.Bytes()

	ctype := apihttp.CTypeJSON
	body := s.newBody(c, meta.ID())
	return s.authRequest(c, httpRequestParams{method: "GET", url: s.backupURL(c), contentType: ctype, body: body})
}

func (s *backupsDownloadSuite) TestCalls(c *gc.C) {
	resp := s.sendValid(c)
	defer resp.Body.Close()

	c.Check(s.fake.Calls, gc.DeepEquals, []string{"Get"})
	c.Check(s.fake.IDArg, gc.Equals, s.fake.Meta.ID())
}

func (s *backupsDownloadSuite) TestResponse(c *gc.C) {
	resp := s.sendValid(c)
	defer resp.Body.Close()
	meta := s.fake.Meta

	c.Check(resp.StatusCode, gc.Equals, http.StatusOK)
	c.Check(resp.Header.Get("Digest"), gc.Equals, string(apihttp.DigestSHA)+"="+meta.Checksum())
	c.Check(resp.Header.Get("Content-Type"), gc.Equals, apihttp.CTypeRaw)
}

func (s *backupsDownloadSuite) TestBody(c *gc.C) {
	resp := s.sendValid(c)
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(body, jc.DeepEquals, s.body)
}

func (s *backupsDownloadSuite) TestErrorWhenGetFails(c *gc.C) {
	s.fake.Error = errors.New("failed!")
	resp := s.sendValid(c)
	defer resp.Body.Close()

	s.checkErrorResponse(c, resp, http.StatusInternalServerError, "failed!")
}

type backupsUploadSuite struct {
	baseBackupsSuite
	meta *backups.Metadata
}

var _ = gc.Suite(&backupsUploadSuite{})

func (s *backupsUploadSuite) sendValid(c *gc.C, id string) *http.Response {
	s.fake.Meta = backups.NewMetadata()
	s.fake.Meta.SetID("<a new backup ID>")

	var parts bytes.Buffer
	writer := multipart.NewWriter(&parts)

	// Set the metadata part.
	s.meta = backups.NewMetadata()
	metaResult := apiserverbackups.ResultFromMetadata(s.meta)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="metadata"`)
	header.Set("Content-Type", apihttp.CTypeJSON)
	part, err := writer.CreatePart(header)
	c.Assert(err, jc.ErrorIsNil)
	err = json.NewEncoder(part).Encode(metaResult)
	c.Assert(err, jc.ErrorIsNil)

	// Set the attached part.
	archive := bytes.NewBufferString("<compressed data>")
	part, err = writer.CreateFormFile("attached", "juju-backup.tar.gz")
	c.Assert(err, jc.ErrorIsNil)
	_, err = io.Copy(part, archive)
	c.Assert(err, jc.ErrorIsNil)

	// Send the request.
	ctype := writer.FormDataContentType()
	return s.authRequest(c, httpRequestParams{method: "PUT", url: s.backupURL(c), contentType: ctype, body: &parts})
}

func (s *backupsUploadSuite) TestCalls(c *gc.C) {
	resp := s.sendValid(c, "<a new backup ID>")
	defer resp.Body.Close()

	c.Check(s.fake.Calls, gc.DeepEquals, []string{"Add"})
	c.Check(s.fake.ArchiveArg, gc.NotNil)
	c.Check(s.fake.MetaArg, jc.DeepEquals, s.meta)
}

func (s *backupsUploadSuite) TestResponse(c *gc.C) {
	resp := s.sendValid(c, "<a new backup ID>")
	defer resp.Body.Close()

	c.Check(resp.StatusCode, gc.Equals, http.StatusOK)
	c.Check(resp.Header.Get("Content-Type"), gc.Equals, apihttp.CTypeJSON)
}

func (s *backupsUploadSuite) TestBody(c *gc.C) {
	resp := s.sendValid(c, "<a new backup ID>")
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, jc.ErrorIsNil)
	var result params.BackupsUploadResult
	err = json.Unmarshal(body, &result)
	c.Assert(err, jc.ErrorIsNil)

	c.Check(result.ID, gc.Equals, "<a new backup ID>")
}

func (s *backupsUploadSuite) TestErrorWhenGetFails(c *gc.C) {
	s.fake.Error = errors.New("failed!")
	resp := s.sendValid(c, "<a new backup ID>")
	defer resp.Body.Close()

	s.checkErrorResponse(c, resp, http.StatusInternalServerError, "failed!")
}
