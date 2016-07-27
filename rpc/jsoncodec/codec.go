// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// The jsoncodec package provides a JSON codec for the rpc package.
package jsoncodec

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/juju/errors"
	"github.com/juju/gojsonschema"
	"github.com/juju/loggo"
	"github.com/juju/utils"
	"gopkg.in/yaml.v2"

	"github.com/juju/juju/rpc"
)

var logger = loggo.GetLogger("juju.rpc.jsoncodec")

// JSONConn sends and receives messages to an underlying connection
// in JSON format.
type JSONConn interface {
	// Send sends a message.
	Send(msg interface{}) error
	// Receive receives a message into msg.
	Receive(msg interface{}) error
	Close() error
}

// Codec implements rpc.Codec for a connection.
type Codec struct {
	// msg holds the message that's just been read by ReadHeader, so
	// that the body can be read by ReadBody.
	msg         inMsgV1
	conn        JSONConn
	logMessages int32
	mu          sync.Mutex
	closing     bool
}

// New returns an rpc codec that uses conn to send and receive
// messages.
func New(conn JSONConn) *Codec {
	return &Codec{
		conn: conn,
	}
}

// inMsg holds an incoming message.  We don't know the type of the
// parameters or response yet, so we delay parsing by storing them
// in a RawMessage.
type inMsgV0 struct {
	RequestId uint64
	Type      string
	Version   int
	Id        string
	Request   string
	Params    json.RawMessage
	Error     string
	ErrorCode string
	Response  json.RawMessage
}

type inMsgV1 struct {
	RequestId uint64          `json:"request-id"`
	Type      string          `json:"type"`
	Version   int             `json:"version"`
	Id        string          `json:"id"`
	Request   string          `json:"request"`
	Params    json.RawMessage `json:"params"`
	Error     string          `json:"error"`
	ErrorCode string          `json:"error-code"`
	Response  json.RawMessage `json:"response"`
}

const msgV1JSONSchema = `
{
	"$schema": "http://json-schema.org/draft-04/schema#",
	"title": "Message Schema v1",
	"type": "object",
	"description": "Format of valid Request and Response messages",
	"properties": {
		"request-id": {
			"type": "integer"
		},
		"type": {
			"type": "string"
		},
		"version": {
			"type": "integer"
		},
		"id": {
			"type": "string"
		},
		"request": {
			"type": "string"
		},
		"params": {
			"type": "object"
		},
		"error": {
			"type": "string"
		},
		"error-code": {
			"type": "string"
		},
		"response": {
			"type": "object"
		}
	},
	"required": ["request-id", "type", "version", "id", "error", "error-code"],
	"additionalProperties": false
}
`
const msgV0YAMLSchema = `
$schema: "http://json-schema.org/draft-04/schema#"
title: "Juju RPC Message Schema v0"
description: |
    Format of valid Request and Response messages for Version 0 of the JSON RPC.
    This is the format that was used for Juju 1.X, but for valid Juju 2.x communication
    the Juju RPC Message Schema v1 should be used. 
type: object
properties:
  RequestId:
    type: integer
  Type:
    type: string
  Version:
    type: integer
  Id:
    type: string
  Request:
    type: string
  Params:
    type: object
  Error:
    type: string
  ErrorCode:
    type: string
  Response:
    type: object
required:
  - RequestId
`

var msgV0Schema = MustParseYAMLSchema(msgV0YAMLSchema)

const msgV1YAMLSchema = `
$schema: "http://json-schema.org/draft-04/schema#"
title: "Juju Message Schema v1"
description: |
    Format of valid Request and Response messages
type: object
properties:
  request-id:
    type: integer
    description: |
      Unique identifier for this request. The response will be tagged with the
      same value as the request id. Request identifiers should not be reused
      within the lifetime of a connection.
      Request-id is mandatory and must be a valid positive integer.
    minimum: 1
  type:
    type: string
    description: |
      Type gives the name of the Facade that we will be interacting with. A
      Facade collects a set of methods, grouped together for a focused purpose.
  version:
    type: integer
    description: |
      The Version of the Facade that we are interacting with. Clients should
      know what versions of Facades they support. Servers can expose multiple
      versions of a Facade to allow compatibility with older clients.
    minimum: 0
  request:
    type: string
    description: |
      The method on Facade that is being called. Request is only relevant for
      the side initiating the request. Responses will not have a request field.
  params:
    type: object
    description: |
      Arguments can be seen as being passed to the Facade.Request(params). See
      individual Methods for descriptions of what parameters need to be supplied.
  error:
    type: string
    description: |
      If there is something invalid about the request (malformed request, etc),
      or if a client is accessing a facade that it does not have access to, an
      error will be generated and returned. Error is inteded to be a human
      readable string. Note that if you are making a bulk api call (that takes
      a list of objects), errors are likely to be part of the Response. Since
      if it is valid for you to make the request, but you ask about an object
      you do not have access rights.
  error-code:
    type: string
    description: |
      Short, machine-readable string indicating there was a problem in the
      request.
  id:
    type: string
    description: |
      Some Facades use an id as a distinguisher for what object you are
      operating on (eg Watcher/NotifyWatcher). Most Facades do not use this
      value.
  response:
    type: object
    description: |
      The result of making a request. Response should be omitted for requests.
      See individual methods to determine what the response layout is.
required:
  - request-id
additionalProperties: false
`

func MustParseYAMLSchema(schema string) *gojsonschema.Schema {
	var yamlDoc map[interface{}]interface{}
	var schemaDoc map[string]interface{}
	if err := yaml.Unmarshal([]byte(schema), &yamlDoc); err != nil {
		panic(err)
	} else {
		// Convert the map[interface{}]interface{} back into
		// map[string]interface{} which is what we need for JSON like
		// things.
		if asInterface, err := utils.ConformYAML(yamlDoc); err != nil {
			panic(err)
		} else {
			schemaDoc = asInterface.(map[string]interface{})
		}
	}
	schemaObj, err := gojsonschema.NewSchema(gojsonschema.NewGoLoader(schemaDoc))
	if err != nil {
		panic(err)
	}
	return schemaObj
}

var msgV1Schema = MustParseYAMLSchema(msgV1YAMLSchema)

// outMsg holds an outgoing message.
type outMsgV0 struct {
	RequestId uint64
	Type      string      `json:",omitempty"`
	Version   int         `json:",omitempty"`
	Id        string      `json:",omitempty"`
	Request   string      `json:",omitempty"`
	Params    interface{} `json:",omitempty"`
	Error     string      `json:",omitempty"`
	ErrorCode string      `json:",omitempty"`
	Response  interface{} `json:",omitempty"`
}

type outMsgV1 struct {
	RequestId uint64      `json:"request-id"`
	Type      string      `json:"type,omitempty"`
	Version   int         `json:"version,omitempty"`
	Id        string      `json:"id,omitempty"`
	Request   string      `json:"request,omitempty"`
	Params    interface{} `json:"params,omitempty"`
	Error     string      `json:"error,omitempty"`
	ErrorCode string      `json:"error-code,omitempty"`
	Response  interface{} `json:"response,omitempty"`
}

func (c *Codec) Close() error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	return c.conn.Close()
}

func (c *Codec) isClosing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closing
}

func (c *Codec) ReadHeader(hdr *rpc.Header) error {
	var m json.RawMessage
	var version int
	err := c.conn.Receive(&m)
	if err == nil {
		logger.Tracef("<- %s", m)
		c.msg, version, err = c.readMessage(m)
	} else {
		logger.Tracef("<- error: %v (closing %v)", err, c.isClosing())
	}
	if err != nil {
		// If we've closed the connection, we may get a spurious error,
		// so ignore it.
		if c.isClosing() || err == io.EOF {
			return io.EOF
		}
		return errors.Annotate(err, "error receiving message")
	}
	hdr.RequestId = c.msg.RequestId
	hdr.Request = rpc.Request{
		Type:    c.msg.Type,
		Version: c.msg.Version,
		Id:      c.msg.Id,
		Action:  c.msg.Request,
	}
	hdr.Error = c.msg.Error
	hdr.ErrorCode = c.msg.ErrorCode
	hdr.Version = version
	return nil
}

func (c *Codec) readMessage(m json.RawMessage) (inMsgV1, int, error) {
	var msg inMsgV1
	if result, err := msgV1Schema.Validate(gojsonschema.NewStringLoader(string(m))); err != nil {
		// err is only not-nil if the content.loadJSON() fails (eg, its not JSON at all)
		return msg, -1, errors.Trace(err)
	} else if !result.Valid() {
		var allErrors []string
		if resultV0, err := msgV0Schema.Validate(gojsonschema.NewStringLoader(string(m))); err != nil {
			// should never get here, as invalid JSON should be caught in the V1 check.
			return msg, -1, errors.Trace(err)
		} else if resultV0.Valid() {
			// This is valid according to the old schema, but not
			// the new schema, so treat it as an old request.
			return c.readV0Message(m)
		}
		// Not valid as V0 request either, so reject it.
		for _, errDescr := range result.Errors() {
			allErrors = append(allErrors, errDescr.String())
		}
		return msg, -1, errors.Errorf("message had schema errors:\n%s\n", strings.Join(allErrors, "\n"))
	}
	if err := json.Unmarshal(m, &msg); err != nil {
		return msg, -1, errors.Trace(err)
	}
	return msg, 1, nil
}

func (c *Codec) readV0Message(m json.RawMessage) (inMsgV1, int, error) {
	var msg inMsgV0
	if err := json.Unmarshal(m, &msg); err != nil {
		return inMsgV1{}, -1, errors.Trace(err)
	}
	return inMsgV1{
		RequestId: msg.RequestId,
		Type:      msg.Type,
		Version:   msg.Version,
		Id:        msg.Id,
		Request:   msg.Request,
		Params:    msg.Params,
		Error:     msg.Error,
		ErrorCode: msg.ErrorCode,
		Response:  msg.Response,
	}, 0, nil
}

func (c *Codec) ReadBody(body interface{}, isRequest bool) error {
	if body == nil {
		return nil
	}
	var rawBody json.RawMessage
	if isRequest {
		rawBody = c.msg.Params
	} else {
		rawBody = c.msg.Response
	}
	if len(rawBody) == 0 {
		// If the response or params are omitted, it's
		// equivalent to an empty object.
		return nil
	}
	return json.Unmarshal(rawBody, body)
}

// DumpRequest returns JSON-formatted data representing
// the RPC message with the given header and body,
// as it would be written by Codec.WriteMessage.
// If the body cannot be marshalled as JSON, the data
// will hold a JSON string describing the error.
func DumpRequest(hdr *rpc.Header, body interface{}) []byte {
	msg, err := response(hdr, body)
	if err != nil {
		return []byte(fmt.Sprintf("%q", err.Error()))
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return []byte(fmt.Sprintf("%q", "marshal error: "+err.Error()))
	}
	return data
}

func (c *Codec) WriteMessage(hdr *rpc.Header, body interface{}) error {
	msg, err := response(hdr, body)
	if err != nil {
		return errors.Trace(err)
	}
	if logger.IsTraceEnabled() {
		data, err := json.Marshal(msg)
		if err != nil {
			logger.Tracef("-> marshal error: %v", err)
			return err
		}
		logger.Tracef("-> %s", data)
	}
	return c.conn.Send(msg)
}

func response(hdr *rpc.Header, body interface{}) (interface{}, error) {
	switch hdr.Version {
	case 0:
		return newOutMsgV0(hdr, body), nil
	case 1:
		return newOutMsgV1(hdr, body), nil
	default:
		return nil, errors.Errorf("unsupported version %d", hdr.Version)
	}
}

// newOutMsgV0 fills out a outMsgV0 with information from the given
// header and body.
func newOutMsgV0(hdr *rpc.Header, body interface{}) outMsgV0 {
	result := outMsgV0{
		RequestId: hdr.RequestId,
		Type:      hdr.Request.Type,
		Version:   hdr.Request.Version,
		Id:        hdr.Request.Id,
		Request:   hdr.Request.Action,
		Error:     hdr.Error,
		ErrorCode: hdr.ErrorCode,
	}
	if hdr.IsRequest() {
		result.Params = body
	} else {
		result.Response = body
	}
	return result
}

// newOutMsgV1 fills out a outMsgV1 with information from the given header and
// body. This might look a lot like the v0 method, and that is because it is.
// However, since Go determins structs to be sufficiently different if the
// tags are different, we can't use the same code. Theoretically we could use
// reflect, but no.
func newOutMsgV1(hdr *rpc.Header, body interface{}) outMsgV1 {
	result := outMsgV1{
		RequestId: hdr.RequestId,
		Type:      hdr.Request.Type,
		Version:   hdr.Request.Version,
		Id:        hdr.Request.Id,
		Request:   hdr.Request.Action,
		Error:     hdr.Error,
		ErrorCode: hdr.ErrorCode,
	}
	if hdr.IsRequest() {
		result.Params = body
	} else {
		result.Response = body
	}
	return result
}
