// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package agent

import (
	"context"
	stdtesting "testing"

	"github.com/juju/tc"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/controller"
	"github.com/juju/juju/core/network"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/internal/database"
	loggertesting "github.com/juju/juju/internal/logger/testing"
)

type BootstrapSuite struct {
}

func TestBootstrapSuite(t *stdtesting.T) {
	tc.Run(t, &BootstrapSuite{})
}

func (s *BootstrapSuite) TestStub(c *tc.C) {
	c.Skipf(`This suite is missing tests for the following scenarios:

  - Test that the initial model and all of the machines are created.
  - Test JWKS reachable check.
  - Test that the initial model is created with the correct UUID.
  - Test initial password.
  - Test set constraints for bootstrap machine and model.
  - Test system identity is written to the data directory.
`)
}

func (s *BootstrapSuite) TestBootstrapControllerAddressesUsesProviderInterface(c *tc.C) {
	want := network.NewMachineAddresses([]string{"10.0.0.1"}).AsProviderAddresses()
	env := &stubBootstrapAddressEnviron{addresses: want}

	got, err := bootstrapControllerAddresses(c.Context(), env, "bootstrap-instance")
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.DeepEquals, want)
	c.Check(env.calls, tc.Equals, 1)
}

type stubBootstrapAddressEnviron struct {
	environs.BootstrapEnviron
	addresses network.ProviderAddresses
	calls     int
}

func (e *stubBootstrapAddressEnviron) BootstrapControllerAddresses(
	_ context.Context,
) (network.ProviderAddresses, error) {
	e.calls++
	return e.addresses, nil
}

func (s *BootstrapSuite) TestNewDatabaseInitialiserDefaultsToDqlite(c *tc.C) {
	initialiser, err := newDatabaseInitialiser(
		c.Context(), stubDBAgentConfig{dataDir: c.MkDir()}, nil, loggertesting.WrapCheckLog(c),
	)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(initialiser, tc.FitsTypeOf, &database.DqliteInitialiser{})
}

func (s *BootstrapSuite) TestNewDatabaseInitialiserDqlite(c *tc.C) {
	initialiser, err := newDatabaseInitialiser(c.Context(), stubDBAgentConfig{
		dataDir: c.MkDir(),
		backend: controller.DBBackendDqlite,
	}, nil, loggertesting.WrapCheckLog(c))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(initialiser, tc.FitsTypeOf, &database.DqliteInitialiser{})
}

func (s *BootstrapSuite) TestNewDatabaseInitialiserPostgreSQL(c *tc.C) {
	initialiser, err := newDatabaseInitialiser(c.Context(), stubDBAgentConfig{
		backend: controller.DBBackendPostgreSQL,
		url:     "postgres://u:p@db.example.com/juju?sslmode=verify-full",
	}, nil, loggertesting.WrapCheckLog(c))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(initialiser, tc.FitsTypeOf, &database.PostgresInitialiser{})
}

func (s *BootstrapSuite) TestNewDatabaseInitialiserPostgreSQLRequiresURL(c *tc.C) {
	_, err := newDatabaseInitialiser(c.Context(), stubDBAgentConfig{
		backend: controller.DBBackendPostgreSQL,
	}, nil, loggertesting.WrapCheckLog(c))
	c.Check(err, tc.ErrorMatches, `reading database connection details: .*requires a URL`)
}

func (s *BootstrapSuite) TestNewDatabaseInitialiserUnknownBackend(c *tc.C) {
	_, err := newDatabaseInitialiser(c.Context(), stubDBAgentConfig{
		backend: "mysql",
	}, nil, loggertesting.WrapCheckLog(c))
	c.Check(err, tc.ErrorMatches, `reading database connection details: unknown database backend "mysql"`)
}

type stubDBAgentConfig struct {
	agent.Config
	dataDir string
	backend string
	url     string
}

func (s stubDBAgentConfig) DataDir() string   { return s.dataDir }
func (s stubDBAgentConfig) CACert() string    { return "ca-cert" }
func (s stubDBAgentConfig) DBBackend() string { return s.backend }
func (s stubDBAgentConfig) DBURL() string     { return s.url }
func (s stubDBAgentConfig) DBCACert() string  { return "" }

func (s stubDBAgentConfig) ControllerAgentInfo() (controller.ControllerAgentInfo, bool) {
	return controller.ControllerAgentInfo{Cert: "cert", PrivateKey: "key"}, true
}
