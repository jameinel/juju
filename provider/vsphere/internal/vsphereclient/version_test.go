// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package vsphereclient_test

import (
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/provider/vsphere/internal/vsphereclient"
)

type versionSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&versionSuite{})

func V(i ...int) vsphereclient.Version {
	return vsphereclient.Version(i)
}

var parseVersionTests = []struct {
	Description string
	Version     string
	Parsed      vsphereclient.Version
	Error       string
}{{
	Description: "major",
	Version:     "6",
	Parsed:      V(6),
}, {
	Description: "major.minor",
	Version:     "6.5",
	Parsed:      V(6, 5),
}, {
	Description: "major.minor.micro",
	Version:     "6.5.0",
	Parsed:      V(6, 5, 0),
}, {
	Description: "major.minor.micro.extra",
	Version:     "1.2.3.4",
	Parsed:      V(1, 2, 3, 4),
}, {
	Description: "non-integer",
	Version:     "1.2beta",
	Error:       `"1.2beta" is not a valid version: invalid integer: "2beta"`,
}, {
	Description: "negative integer",
	Version:     "1.-1",
	Error:       `"1.-1" is not a valid version: negative sections are not allowed`,
}}

func (s *versionSuite) TestParse(c *gc.C) {
	for i, test := range parseVersionTests {
		c.Logf("step %d: %s", i, test.Description)
		parsed, err := vsphereclient.ParseVersion(test.Version)
		if test.Error == "" {
			c.Assert(err, jc.ErrorIsNil)
			c.Check(parsed, gc.DeepEquals, test.Parsed)
		} else {
			c.Assert(err, gc.ErrorMatches, test.Error)
		}
	}
}

var compareVersionTests = []struct {
	Description string
	VersionA    string
	VersionB    string
	Compared    int
}{{
	Description: "same major",
	VersionA:    "6",
	VersionB:    "6",
	Compared:    0,
}, {
	Description: "same major.minor",
	VersionA:    "6.5",
	VersionB:    "6.5",
	Compared:    0,
}, {
	Description: "same major.minor.micro",
	VersionA:    "6.5.4",
	VersionB:    "6.5.4",
	Compared:    0,
}, {
	Description: "same major.minor.micro.extra",
	VersionA:    "6.5.4.3",
	VersionB:    "6.5.4.3",
	Compared:    0,
}, {
	Description: "diff major",
	VersionA:    "7.5.4.3",
	VersionB:    "6.5.4.3",
	Compared:    1,
}, {
	Description: "diff minor",
	VersionA:    "6.7.4.3",
	VersionB:    "6.5.4.3",
	Compared:    1,
}, {
	Description: "diff micro",
	VersionA:    "6.5.5.3",
	VersionB:    "6.5.4.3",
	Compared:    1,
}, {
	Description: "different length",
	VersionA:    "6.5.4",
	VersionB:    "6.5",
	Compared:    1,
}, {
	Description: "different length matches zero",
	VersionA:    "6.5.0",
	VersionB:    "6.5",
	Compared:    0,
}}

func checkCompare(c *gc.C, A, B vsphereclient.Version, cmp int) {
	result := A.Compare(B)
	if result != cmp {
		c.Errorf("%v.Compare(%v) expected %d not %d", A, B, cmp, result)
	}
}
func (s *versionSuite) TestCompare(c *gc.C) {
	for i, test := range compareVersionTests {
		c.Logf("step %d: %s", i, test.Description)
		parsedA, err := vsphereclient.ParseVersion(test.VersionA)
		c.Assert(err, jc.ErrorIsNil)
		parsedB, err := vsphereclient.ParseVersion(test.VersionB)
		c.Assert(err, jc.ErrorIsNil)
		checkCompare(c, parsedA, parsedB, test.Compared)
		// reversing them should invert the comparison
		checkCompare(c, parsedB, parsedA, -test.Compared)
	}
}
