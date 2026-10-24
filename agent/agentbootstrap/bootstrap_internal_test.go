// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package agentbootstrap

import (
	"context"
	"testing"

	"github.com/juju/names/v6"
	"github.com/juju/tc"

	"github.com/juju/juju/agent"
	coremodel "github.com/juju/juju/core/model"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/internal/database"
	loggertesting "github.com/juju/juju/internal/logger/testing"
)

type bootstrapInternalSuite struct{}

func TestBootstrapInternalSuite(t *testing.T) {
	tc.Run(t, &bootstrapInternalSuite{})
}

func (*bootstrapInternalSuite) TestNewAgentBootstrapRequiresDatabaseInitialiser(c *tc.C) {
	_, err := NewAgentBootstrap(AgentBootstrapArgs{
		AdminUser:        names.NewLocalUserTag("admin"),
		AgentConfig:      stubAgentConfig{},
		BootstrapEnviron: stubBootstrapEnviron{},
		Logger:           loggertesting.WrapCheckLog(c),
	})
	c.Check(err, tc.ErrorMatches, "database initialiser not valid")
}

type stubAgentConfig struct {
	agent.ConfigSetter
}

type stubBootstrapEnviron struct {
	environs.BootstrapEnviron
}

type databaseInitialiserFunc func(context.Context, coremodel.UUID, ...database.BootstrapOpt) error

func (f databaseInitialiserFunc) Initialise(
	ctx context.Context, controllerModelUUID coremodel.UUID, opts ...database.BootstrapOpt,
) error {
	return f(ctx, controllerModelUUID, opts...)
}
