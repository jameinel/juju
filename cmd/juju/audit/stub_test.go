// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.
package audit

import (
	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/testing"
)

type stubAuditAPI struct {
	stub *testing.Stub

	// Stub AuditAPI attributes follow
	ReturnAuditRecords []auditRecord
}

// Connect TBD.
func (s *stubAuditAPI) Connect(ctx *cmd.Context) (LogLister, error) {
	s.stub.AddCall("Connect", ctx)
	if err := s.stub.NextErr(); err != nil {
		return nil, errors.Trace(err)
	}
	return s, nil
}

func (s *stubAuditAPI) QueryResults(filters []LogFilter) ([]auditRecord, error) {
	s.stub.AddCall("QueryResults", filters)
	if err := s.stub.NextErr(); err != nil {
		return nil, errors.Trace(err)
	}
	return s.ReturnAuditRecords, nil
}

func (s *stubAuditAPI) Close() error {
	return nil
}
