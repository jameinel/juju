// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package common

import (
	"github.com/juju/juju/worker/agent"
	"github.com/juju/juju/worker/apiaddressupdater"
	"github.com/juju/juju/worker/apiconn"
	"github.com/juju/juju/worker/dependency"
	"github.com/juju/juju/worker/logger"
	"github.com/juju/juju/worker/machinelock"
	"github.com/juju/juju/worker/proxyupdater"
	"github.com/juju/juju/worker/rsyslog"
	"github.com/juju/juju/worker/upgrader"
)

// We only expect one of each of these per process; apart from a little bit of
// handwaving around the identity used for the api connection, these elements
// should work just fine in a consolidated agent.
var (
	AgentName             = "agent"
	ApiAddressUpdaterName = "api-address-updater"
	ApiCallerName         = "api-caller"
	BinaryUpgraderName    = "binary-upgrader"
	LoggerUpdaterName     = "logger-updater"
	MachineLockName       = "machine-lock"
	ProxyUpdaterName      = "proxy-updater"
	RsyslogUpdaterName    = "rsyslog-updater"
)

// AgentManifolds returns the manifolds representing workers that must be run
// in every jujud agent. It creates a manifold representing the agent itself;
// one for an API connection on behalf of that agent; and various others as
// defined in this file (generally responsible for upgrading one sort of
// configuration or other).
func AgentManifolds(a agent.Agent) dependency.Manifolds {
	return dependency.Manifolds{

		AgentName: agent.Manifold(a),

		ApiAddressUpdaterName: apiaddressupdater.Manifold(apiaddressupdater.ManifoldConfig{
			AgentName:     AgentName,
			ApiCallerName: ApiCallerName,
		}),

		ApiCallerName: apiconn.Manifold(apiconn.ManifoldConfig{
			AgentName: AgentName,
		}),

		BinaryUpgraderName: upgrader.Manifold(upgrader.ManifoldConfig{
			AgentName:     AgentName,
			ApiCallerName: ApiCallerName,
		}),

		LoggerUpdaterName: logger.Manifold(logger.ManifoldConfig{
			AgentName:     AgentName,
			ApiCallerName: ApiCallerName,
		}),

		MachineLockName: machinelock.Manifold(machinelock.ManifoldConfig{
			AgentName: AgentName,
		}),

		ProxyUpdaterName: proxyupdater.Manifold(proxyupdater.ManifoldConfig{
			ApiCallerName: ApiCallerName,
		}),

		RsyslogUpdaterName: rsyslog.Manifold(rsyslog.ManifoldConfig{
			AgentName:     AgentName,
			ApiCallerName: ApiCallerName,
		}),
	}
}
