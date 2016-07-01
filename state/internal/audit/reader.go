// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package audit

import (
	"time"

	"launchpad.net/tomb"

	"github.com/juju/errors"
	"github.com/juju/juju/audit"
	"github.com/juju/loggo"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

func NewAuditTailer(
	logger loggo.Logger,
	auditCollIter *mgo.Iter,
	heartbeatInterval time.Duration,
) *AuditTailer {

	tailer := &AuditTailer{
		AuditEntryPipe:    make(chan audit.AuditEntry),
		auditCollIter:     auditCollIter,
		heartbeatInterval: heartbeatInterval,
	}
	go tailer.loop(logger, auditCollIter)

	return tailer
}

type AuditTailer struct {
	tomb.Tomb

	AuditEntryPipe chan audit.AuditEntry

	auditCollIter     *mgo.Iter
	heartbeatInterval time.Duration
}

func (t *AuditTailer) Kill(reason error) {
	// Because mgo doesn't expose a channel to select on, the only way
	// to break out of the loop method if it's blocking on calling
	// Next on the iterator is to close the iterator.
	t.auditCollIter.Close()
	t.Tomb.Kill(reason)
}

func (t *AuditTailer) loop(logger loggo.Logger, auditCollIter *mgo.Iter) {
	defer auditCollIter.Close()
	defer close(t.AuditEntryPipe)

	var doc auditEntryDoc
	for auditCollIter.Next(&doc) {

		auditEntry, err := auditEntryFromDoc(doc)
		if err != nil {
			// Audit systems cannot have holes in their stream of
			// entries. We have to assume that if we're forwarding
			// audit entries, that mongo is not the system of
			// truth. Under this assumption, a missing log record
			// should halt the line.
			t.Kill(errors.Annotate(err, "cannot convert audit doc"))
			break
		}

		for {
			select {
			case <-t.Dying():
				return
			case t.AuditEntryPipe <- auditEntry:
				break
			case <-time.After(t.heartbeatInterval):
				logger.Infof("AuditTailer heartbeat: waiting to send audit entry")
			}
		}
	}

	// Close is idempotent and will not interfere with the
	// deferred call to close.
	if err := auditCollIter.Close(); err != nil {
		logger.Criticalf("%v", errors.Annotate(err, "cannot unmarshal audit doc"))
	}
}

func AggregateAuditRecordsAfter(t time.Time) bson.M {
	return bson.M{"$match": bson.M{"Timestamp": bson.M{"$gt": t}}}
}

func auditEntryFromDoc(doc auditEntryDoc) (audit.AuditEntry, error) {
	var timestamp time.Time
	if err := timestamp.UnmarshalText([]byte(doc.Timestamp)); err != nil {
		return audit.AuditEntry{}, errors.Trace(err)
	}

	return audit.AuditEntry{
		JujuServerVersion: doc.JujuServerVersion,
		ModelUUID:         doc.ModelUUID,
		Timestamp:         timestamp,
		RemoteAddress:     doc.RemoteAddress,
		OriginType:        doc.OriginType,
		OriginName:        doc.OriginName,
		Operation:         doc.Operation,
		Data:              doc.Data,
	}, nil
}
