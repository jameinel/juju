package audit

import (
	"net"
	"time"
)

type FakeAuditAPIClient struct{}

func (s *FakeAuditAPIClient) Connect() error { return nil }
func (s *FakeAuditAPIClient) Close() error   { return nil }

func (s *FakeAuditAPIClient) AuditEntries([]LogFilter) ([]auditRecord, error) {
	return []auditRecord{
		{
			net.IPv4(8, 8, 8, 8),
			"user",
			"status",
			time.Now(),
			"bob",
		},
		{
			net.IPv4(23, 196, 119, 211),
			"action",
			"cleanup",
			time.Now(),
			"ubuntu/0",
		},
	}, nil
}
