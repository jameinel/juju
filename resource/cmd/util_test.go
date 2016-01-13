// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package cmd

import (
	"bytes"
	"strings"

	jujucmd "github.com/juju/cmd"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	charmresource "gopkg.in/juju/charm.v6-unstable/resource"

	coretesting "github.com/juju/juju/testing"
)

func charmRes(c *gc.C, name, suffix, comment, fingerprint string) charmresource.Resource {
	var fp charmresource.Fingerprint
	if fingerprint == "" {
		built, err := charmresource.GenerateFingerprint(strings.NewReader(name))
		c.Assert(err, jc.ErrorIsNil)
		fp = built
	} else {
		wrapped, err := charmresource.NewFingerprint([]byte(fingerprint))
		c.Assert(err, jc.ErrorIsNil)
		fp = wrapped
	}

	res := charmresource.Resource{
		Meta: charmresource.Meta{
			Name:    name,
			Type:    charmresource.TypeFile,
			Path:    name + suffix,
			Comment: comment,
		},
		Origin:      charmresource.OriginUpload,
		Revision:    0,
		Fingerprint: fp,
	}
	err := res.Validate()
	c.Assert(err, jc.ErrorIsNil)
	return res
}

func newCharmResources(c *gc.C, names ...string) []charmresource.Resource {
	var resources []charmresource.Resource
	for _, name := range names {
		var comment string
		parts := strings.SplitN(name, ":", 2)
		if len(parts) == 2 {
			name = parts[0]
			comment = parts[1]
		}

		res := charmRes(c, name, ".tgz", comment, "")
		resources = append(resources, res)
	}
	return resources
}

func runCmd(c *gc.C, command jujucmd.Command, args ...string) (code int, stdout string, stderr string) {
	ctx := coretesting.Context(c)
	code = jujucmd.Main(command, ctx, args)
	stdout = string(ctx.Stdout.(*bytes.Buffer).Bytes())
	stderr = string(ctx.Stderr.(*bytes.Buffer).Bytes())
	return code, stdout, stderr
}
