// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package logfwd

import (
	"github.com/juju/errors"
	"github.com/juju/juju/audit"
)

func NewAuditLogRecordTailer(
	controllerUUID string,
	auditEntryPipe <-chan audit.AuditEntry,
) *AuditLogRecordTailer {
	return &AuditLogRecordTailer{
		controllerUUID: controllerUUID,
		auditEntryPipe: auditEntryPipe,
	}
}

type AuditLogRecordTailer struct {
	controllerUUID string
	auditEntryPipe <-chan audit.AuditEntry
}

func (t *AuditLogRecordTailer) Next() (Record, error) {
	select {
	case auditEntry := <-t.auditEntryPipe:
		return recordFromAuditEntry(t.controllerUUID, auditEntry)
	}
}

func recordFromAuditEntry(controllerUUID string, auditEntry audit.AuditEntry) (Record, error) {
	originType, err := ParseOriginType(auditEntry.OriginType)
	if err != nil {
		return Record{}, errors.Trace(err)
	}

	return Record{
		Origin: Origin{
			ControllerUUID: controllerUUID,
			ModelUUID:      auditEntry.ModelUUID,
			Hostname:       auditEntry.RemoteAddress,
			Type:           originType,
			Name:           auditEntry.OriginName,
			// TODO(katco): Software?
		},
		Timestamp: auditEntry.Timestamp,
		// TODO(katco): Fill in more audit-specific info once rebased onto Eric's PR.
	}, nil
}
