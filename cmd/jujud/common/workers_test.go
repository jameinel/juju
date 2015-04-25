// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package common_test

import (
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/jujud/common"
	"github.com/juju/juju/worker/agent"
)

type AgentManifoldsSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&AgentManifoldsSuite{})

func (s *AgentManifoldsSuite) TestInputs(c *gc.C) {
	manifolds := common.AgentManifolds(&dummyAgent{})
	inputs := map[string][]string{}
	for name, manifold := range manifolds {
		inputs[name] = manifold.Inputs
	}
	c.Assert(inputs, jc.DeepEquals, map[string][]string{
		"agent":                    nil,
		"api-address-updater":      {"agent", "api-caller"},
		"api-caller":               {"agent"},
		"jujud-binary-upgrader":    {"agent", "api-caller"},
		"logger-settings-updater":  {"agent", "api-caller"},
		"machine-lock":             {"agent"},
		"proxy-settings-updater":   {"api-caller"},
		"rsyslog-settings-updater": {"agent", "api-caller"},
	})
}

func (s *AgentManifoldsSuite) TestOutput(c *gc.C) {
	manifolds := common.AgentManifolds(&dummyAgent{})
	outputs := map[string]bool{}
	for name, manifold := range manifolds {
		outputs[name] = manifold.Output != nil
	}
	c.Assert(outputs, jc.DeepEquals, map[string]bool{
		"agent":                    true,
		"api-address-updater":      false,
		"api-caller":               true,
		"jujud-binary-upgrader":    false,
		"logger-settings-updater":  false,
		"machine-lock":             true,
		"proxy-settings-updater":   false,
		"rsyslog-settings-updater": false,
	})
}

// You shouldn't need to change this test.
func (s *AgentManifoldsSuite) TestSanity(c *gc.C) {
	manifolds := common.AgentManifolds(&dummyAgent{})
	for name, manifold := range manifolds {
		if manifold.Start == nil {
			c.Errorf("manifold %q lacks a Start func", name)
		}
		for _, input := range manifold.Inputs {
			if inputManifold, ok := manifolds[input]; !ok {
				c.Errorf("manifold %q requires unknown manifold %q", name, input)
			} else if inputManifold.Output == nil {
				c.Errorf("manifold %q requires non-output manifold %q", name, input)
			}
		}
	}
}

type dummyAgent struct {
	agent.Agent
}
