// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"testing"
	"time"

	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type databaseTimeSuite struct {
	coretesting.BaseSuite
}

func TestDatabaseTimeSuite(t *testing.T) {
	tc.Run(t, &databaseTimeSuite{})
}

func (s *databaseTimeSuite) TestScanDatabaseTimestamp(c *tc.C) {
	expected := time.Date(2026, time.November, 9, 12, 30, 45, 0, time.UTC)
	for _, value := range []any{
		"2026-11-09 12:30:45",
		[]byte("2026-11-09 12:30:45"),
		expected.In(time.FixedZone("test", 2*60*60)),
	} {
		var got dbTimestamp
		err := got.Scan(value)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(got.Time, tc.Equals, expected)
	}
}

func (s *databaseTimeSuite) TestScanDatabaseTimestampRejectsInvalidValues(c *tc.C) {
	for _, value := range []any{nil, int64(123), "not-a-timestamp", []byte("2026-99-01")} {
		var got dbTimestamp
		err := got.Scan(value)
		c.Check(err, tc.ErrorMatches, ".*database timestamp.*")
	}
}
