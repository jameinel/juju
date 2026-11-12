// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testconfig_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/internal/database/dqlite"
	"github.com/juju/juju/internal/database/testconfig"
	"github.com/juju/juju/internal/testhelpers"
	coretesting "github.com/juju/juju/internal/testing"
)

type configSuite struct {
	coretesting.BaseSuite
}

func TestConfigSuite(t *testing.T) {
	tc.Run(t, &configSuite{})
}

func (s *configSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)
	s.PatchEnvironment(testconfig.BackendEnvVar, "")
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, "")
}

func (s *configSuite) TestUnsetSelector(c *tc.C) {
	c.Assert(os.Unsetenv(testconfig.BackendEnvVar), tc.ErrorIsNil)
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{Backend: compiledBackend()})
}

func (s *configSuite) TestEmptySelector(c *tc.C) {
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{Backend: compiledBackend()})
}

func (s *configSuite) TestExplicitCompiledBackend(c *tc.C) {
	s.PatchEnvironment(testconfig.BackendEnvVar, string(compiledBackend()))
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{Backend: compiledBackend()})
}

func (s *configSuite) TestUnavailableBackend(c *tc.C) {
	unavailable := testconfig.Dqlite
	if dqlite.Enabled {
		unavailable = testconfig.SQLite
	}
	s.PatchEnvironment(testconfig.BackendEnvVar, string(unavailable))
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorMatches, fmt.Sprintf(
		"JUJU_TEST_DATABASE=%s requires a %s test build; this build uses %s",
		unavailable, unavailable, compiledBackend(),
	))
	c.Check(config, tc.DeepEquals, testconfig.Config{})
}

func (s *configSuite) TestUnknownBackend(c *tc.C) {
	for _, backend := range []string{"unknown", "postgres", "SQLite", " postgresql", "postgresql "} {
		s.PatchEnvironment(testconfig.BackendEnvVar, backend)
		config, err := testconfig.Load()
		c.Assert(err, tc.ErrorMatches, fmt.Sprintf(
			`unknown JUJU_TEST_DATABASE value %q: expected sqlite, dqlite or postgresql`, backend,
		))
		c.Check(config, tc.DeepEquals, testconfig.Config{})
	}
}

func (s *configSuite) TestPostgreSQL(c *tc.C) {
	const url = "postgresql://localhost/juju-test?sslmode=disable"
	s.PatchEnvironment(testconfig.BackendEnvVar, "postgresql")
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, url)
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{
		Backend:       testconfig.PostgreSQL,
		PostgreSQLURL: url,
	})
}

func (s *configSuite) TestPostgreSQLKeywordConnectionString(c *tc.C) {
	const url = "host=localhost dbname=juju-test sslmode=disable"
	s.PatchEnvironment(testconfig.BackendEnvVar, "postgresql")
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, url)
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config.PostgreSQLURL, tc.Equals, url)
}

func (s *configSuite) TestPostgreSQLRequiresURL(c *tc.C) {
	s.PatchEnvironment(testconfig.BackendEnvVar, "postgresql")
	for _, url := range []string{"", " ", "\t\n"} {
		s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, url)
		config, err := testconfig.Load()
		c.Assert(err, tc.ErrorMatches,
			"JUJU_TEST_POSTGRESQL_URL is required when JUJU_TEST_DATABASE=postgresql")
		c.Check(config, tc.DeepEquals, testconfig.Config{})
	}
}

func (s *configSuite) TestURLDoesNotSelectPostgreSQL(c *tc.C) {
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, "not a connection string")
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{Backend: compiledBackend()})
}

func (s *configSuite) TestURLIgnoredForOtherBackend(c *tc.C) {
	s.PatchEnvironment(testconfig.BackendEnvVar, string(compiledBackend()))
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, "not a connection string")
	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{Backend: compiledBackend()})
}

func (s *configSuite) TestSwitchBackendWithConfiguredURL(c *tc.C) {
	const url = "postgresql://localhost/juju-test"
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, url)
	for _, backend := range []testconfig.Backend{testconfig.PostgreSQL, compiledBackend(), testconfig.PostgreSQL} {
		s.PatchEnvironment(testconfig.BackendEnvVar, string(backend))
		config, err := testconfig.Load()
		c.Assert(err, tc.ErrorIsNil)
		c.Check(config.Backend, tc.Equals, backend)
		c.Check(os.Getenv(testconfig.PostgreSQLURLEnvVar), tc.Equals, url)
	}
}

func (s *configSuite) TestConfigurationSurvivesIsolation(c *tc.C) {
	const url = "postgresql://localhost/juju-test"
	s.PatchEnvironment(testconfig.BackendEnvVar, "postgresql")
	s.PatchEnvironment(testconfig.PostgreSQLURLEnvVar, url)

	var isolation testhelpers.OsEnvSuite
	isolation.SetUpSuite(c)
	defer isolation.TearDownSuite(c)
	isolation.SetUpTest(c)
	defer isolation.TearDownTest(c)

	config, err := testconfig.Load()
	c.Assert(err, tc.ErrorIsNil)
	c.Check(config, tc.DeepEquals, testconfig.Config{
		Backend:       testconfig.PostgreSQL,
		PostgreSQLURL: url,
	})
}

func compiledBackend() testconfig.Backend {
	if dqlite.Enabled {
		return testconfig.Dqlite
	}
	return testconfig.SQLite
}
