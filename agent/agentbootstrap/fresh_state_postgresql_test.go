// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package agentbootstrap

import (
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/cloud"
	coredatabase "github.com/juju/juju/core/database"
	"github.com/juju/juju/internal/database"
	"github.com/juju/juju/internal/database/postgresql"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	loggertesting "github.com/juju/juju/internal/logger/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

// postgresBootstrapSuite runs a fresh controller's bootstrap end to end
// against PostgreSQL, so that every seed operation runs on the PostgreSQL
// schema. It runs only if JUJU_POSTGRESQL_TEST_URL is set.
type postgresBootstrapSuite struct {
	coretesting.BaseSuite
}

func TestPostgresBootstrapSuite(t *testing.T) {
	tc.Run(t, &postgresBootstrapSuite{})
}

func (s *postgresBootstrapSuite) TestFreshIAASState(c *tc.C) {
	s.assertFreshState(c, "lxd")
}

func (s *postgresBootstrapSuite) TestFreshK8sState(c *tc.C) {
	s.assertFreshState(c, cloud.CloudTypeKubernetes)
}

func (s *postgresBootstrapSuite) assertFreshState(c *tc.C, cloudType string) {
	admin := pgtesting.OpenAdmin(c)
	placement := postgresql.Placement{
		URL:          pgtesting.URL(),
		SchemaPrefix: pgtesting.NewSchemaPrefix(c),
	}
	pgtesting.DropSchemasOnCleanup(c, admin, placement.SchemaPrefix)

	b, cfg := newFreshBootstrap(c, cloudType,
		database.NewPostgresInitialiser(placement, loggertesting.WrapCheckLog(c)),
	)
	modelUUID := b.stateInitialisationParams.ControllerModelConfig.UUID()

	err := b.Initialise(c.Context())
	c.Assert(err, tc.ErrorIsNil)

	controllerDB, err := placement.OpenNamespace(coredatabase.ControllerNS)
	c.Assert(err, tc.ErrorIsNil)
	defer controllerDB.Close()
	info, ok := cfg.ControllerAgentInfo()
	c.Assert(ok, tc.IsTrue)
	var cert, systemIdentity string
	err = controllerDB.QueryRowContext(c.Context(),
		"SELECT cert, system_identity FROM controller",
	).Scan(&cert, &systemIdentity)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(cert, tc.Equals, info.Cert)
	c.Check(systemIdentity, tc.Equals, info.SystemIdentity)

	modelDB, err := placement.OpenNamespace(modelUUID)
	c.Assert(err, tc.ErrorIsNil)
	defer modelDB.Close()
	var gotModelUUID string
	var isController bool
	err = modelDB.QueryRowContext(c.Context(),
		"SELECT uuid, is_controller_model FROM model",
	).Scan(&gotModelUUID, &isController)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(gotModelUUID, tc.Equals, modelUUID)
	c.Check(isController, tc.IsTrue)

	apiInfo, ok := cfg.APIInfo()
	c.Assert(ok, tc.IsTrue)
	c.Check(apiInfo.Password, tc.Not(tc.Equals), "agent-before")
}
