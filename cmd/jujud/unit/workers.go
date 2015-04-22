// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package unit

import (
	"time"

	"github.com/juju/juju/cmd/jujud/common"
	"github.com/juju/juju/worker/dependency"
	"github.com/juju/juju/worker/leadership"
	"github.com/juju/juju/worker/uniter"
	"github.com/juju/juju/worker/uniter/filter"
)

// These define the names of the dependency.Manifolds we use in a unit agent.
// We expect one of each of these per running unit; when we try to run N units
// inside each agent process, we'll need to disambiguate the names (and probably
// add/remove the following as a group).
var (
	LeadershipTrackerName = "leadership-tracker"
	EventFilterName       = "event-filter"
	UniterName            = "uniter"
)

// Manifolds returns the manifolds representing workers unique to a single unit.
// It assumes the existence of manifolds defined in cmd/jujud/common (in
// particular: for AgentName, ApiCallerName, and MachineLockName).
func Manifolds() dependency.Manifolds {
	return dependency.Manifolds{

		EventFilterName: filter.Manifold(filter.ManifoldConfig{
			AgentName:     common.AgentName,
			ApiCallerName: common.ApiCallerName,
		}),

		LeadershipTrackerName: leadership.Manifold(leadership.ManifoldConfig{
			AgentName:           common.AgentName,
			ApiCallerName:       common.ApiCallerName,
			LeadershipGuarantee: 30 * time.Second,
		}),

		UniterName: uniter.Manifold(uniter.ManifoldConfig{
			AgentName:             common.AgentName,
			ApiCallerName:         common.ApiCallerName,
			EventFilterName:       EventFilterName,
			LeadershipTrackerName: LeadershipTrackerName,
			MachineLockName:       common.MachineLockName,
		}),
	}
}
