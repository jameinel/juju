// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing_test

import (
	stdtesting "testing"
	"time"

	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type timeSuite struct {
	coretesting.BaseSuite
}

func TestTimeSuite(t *stdtesting.T) {
	tc.Run(t, &timeSuite{})
}

func (s *timeSuite) TestDatabaseNow(c *tc.C) {
	before := time.Now().UTC().Truncate(time.Microsecond)
	now := coretesting.DatabaseNow()
	after := time.Now().UTC().Truncate(time.Microsecond)

	c.Check(now.Location(), tc.Equals, time.UTC)
	c.Check(now.Nanosecond()%int(time.Microsecond), tc.Equals, 0)
	c.Check(now.Before(before), tc.IsFalse)
	c.Check(now.After(after), tc.IsFalse)
}
