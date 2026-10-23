// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package connsource

import (
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/controller"
	coretesting "github.com/juju/juju/internal/testing"
)

type connsourceSuite struct {
	coretesting.BaseSuite
}

func TestConnsourceSuite(t *testing.T) {
	tc.Run(t, &connsourceSuite{})
}

type fakeConfig struct {
	backend, url, caCert string
}

func (f fakeConfig) DBBackend() string { return f.backend }
func (f fakeConfig) DBURL() string     { return f.url }
func (f fakeConfig) DBCACert() string  { return f.caCert }

func (s *connsourceSuite) TestUnsetIsDqlite(c *tc.C) {
	d, err := NewAgentConfigSource(fakeConfig{}).Details(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(d, tc.DeepEquals, Details{Backend: controller.DBBackendDqlite})
}

func (s *connsourceSuite) TestPostgreSQL(c *tc.C) {
	d, err := NewAgentConfigSource(fakeConfig{
		backend: controller.DBBackendPostgreSQL,
		url:     "postgres://u:p@h/juju",
		caCert:  "CA",
	}).Details(c.Context())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(d, tc.DeepEquals, Details{
		Backend: controller.DBBackendPostgreSQL,
		URL:     "postgres://u:p@h/juju",
		CACert:  "CA",
	})
}

func (s *connsourceSuite) TestPostgreSQLRequiresURL(c *tc.C) {
	_, err := NewAgentConfigSource(fakeConfig{
		backend: controller.DBBackendPostgreSQL,
	}).Details(c.Context())
	c.Assert(err, tc.ErrorMatches, `.*requires a URL`)
}

func (s *connsourceSuite) TestUnknownBackend(c *tc.C) {
	_, err := NewAgentConfigSource(fakeConfig{backend: "mysql"}).Details(c.Context())
	c.Assert(err, tc.ErrorMatches, `unknown database backend "mysql"`)
}
