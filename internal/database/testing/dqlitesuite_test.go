// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing_test

import (
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/dqlite"
	"github.com/juju/juju/internal/database/testconfig"
	databasetesting "github.com/juju/juju/internal/database/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type databaseSuite struct {
	coretesting.BaseSuite
	databasetesting.DqliteSuite
}

func TestDatabaseSuite(t *testing.T) {
	tc.Run(t, &databaseSuite{})
}

func (s *databaseSuite) SetUpSuite(c *tc.C) {
	s.BaseSuite.SetUpSuite(c)
	s.DqliteSuite.SetUpSuite(c)
}

func (s *databaseSuite) TearDownSuite(c *tc.C) {
	s.DqliteSuite.TearDownSuite(c)
	s.BaseSuite.TearDownSuite(c)
}

func (s *databaseSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)
	s.DqliteSuite.SetUpTest(c)
}

func (s *databaseSuite) TearDownTest(c *tc.C) {
	s.DqliteSuite.TearDownTest(c)
	s.BaseSuite.TearDownTest(c)
}

func (s *databaseSuite) TestSelectedBackend(c *tc.C) {
	expected := testconfig.SQLite
	if dqlite.Enabled {
		expected = testconfig.Dqlite
	}
	c.Check(s.Backend(), tc.Equals, expected)

	var value int
	err := s.DB().QueryRowContext(c.Context(), "SELECT 42").Scan(&value)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(value, tc.Equals, 42)
}
