// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package unit_test

import (
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/jujud/unit"
)

type ManifoldsSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&ManifoldsSuite{})

func (s *ManifoldsSuite) TestInputs(c *gc.C) {
	manifolds := unit.Manifolds()
	inputs := map[string][]string{}
	for name, manifold := range manifolds {
		inputs[name] = manifold.Inputs
	}
	c.Assert(inputs, jc.DeepEquals, map[string][]string{
		"event-filter":       {"agent", "api-caller"},
		"leadership-tracker": {"agent", "api-caller"},
		"uniter":             {"agent", "api-caller", "event-filter", "leadership-tracker", "machine-lock"},
	})
}

func (s *ManifoldsSuite) TestOutput(c *gc.C) {
	manifolds := unit.Manifolds()
	outputs := map[string]bool{}
	for name, manifold := range manifolds {
		outputs[name] = manifold.Output != nil
	}
	c.Assert(outputs, jc.DeepEquals, map[string]bool{
		"event-filter":       true,
		"leadership-tracker": true,
		"uniter":             false,
	})
}
