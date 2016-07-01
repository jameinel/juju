// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package logforwarder

import (
	"github.com/juju/juju/api/base"
	"github.com/juju/juju/api/logstream"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/audit"
	"github.com/juju/juju/logfwd"
)

// LogStream streams log entries from a log source (e.g. the Juju
// controller).
type LogStream interface {
	// Next returns the next log record from the stream.
	Next() (logfwd.Record, error)
}

// TODO(katco): Need to think more about our error handling strategy
// here. I.e.:
//
// - Do we kill all streams if 1 has an error?
// - Should we report errors back at the risk of being torn down?
//
// This would probably be passed into the multiplexer.

func ManifoldMultiplexerAdaptor(_ base.APICaller, _ params.LogStreamConfig, controllerUUID string) LogStreamFn {
	return func(base.APICaller, params.LogStreamConfig, string) {
		return NewLogStreamMultiplexer(logStreams)
	}
}

func NewLogStreamMultiplexer(logStreams ...LogStream) *LogStreamMultiplexer {
	multiplexer := &LogStreamMultiplexer{
		logStreams: logStreams,
	}

	return multiplexer
}

type LogStreamMultiplexer struct {
	logStreams              []LogStream
	multiplexedRecordStream chan logfwd.Record
}

func (m *LogStreamMultiplexer) Next() (logfwd.Record, error) {
	return <-m.multiplexedRecordStream, nil
}

func (m *LogStreamMultiplexer) kickOff() {
	multiplexedPipe := make(chan logfwd.Record)
	for _, logStream := range m.logStreams {
		go pipeIn(multiplexedPipe, logStream)
	}
}

func pipeIn(send chan<- logfwd.Record, logStream LogStream) {
	for {
		record, err := logStream.Next()
		// TODO(katco): See above comment for what to do with error
		if err != nil {
			panic("NOT IMPLEMENTED:" + err.Error())
		}
		send <- record
	}
}

func OpenLogStream(
	caller base.APICaller,
	cfg params.LogStreamConfig,
	controllerUUID string,
) (*logstream.LogStream, error) {
	return logstream.Open(caller, cfg, controllerUUID)
}

func OpenAuditStream(
	controllerUUID string,
	auditEntryPipe <-chan audit.AuditEntry,
) (LogStream, error) {
	return logfwd.NewAuditLogRecordTailer(controllerUUID, auditEntryPipe), nil
}
