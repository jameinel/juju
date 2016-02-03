// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
)

type bindingsMapSuite struct {
	testing.IsolationSuite

	escapedMapWithBadKeys   map[string]string
	unescapedMapWithBadKeys bindingsMap
}

var _ = gc.Suite(&bindingsMapSuite{})

func (s *bindingsMapSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)

	s.escapedMapWithBadKeys = map[string]string{
		"bad" + fullWidthDollar + "key": "must be unescaped",
		"also " + fullWidthDot + " bad": "same thing",
		fullWidthDot:                    "even by itself",
		fullWidthDollar + fullWidthDot:  "or in combination",
	}

	s.unescapedMapWithBadKeys = bindingsMap{
		"bad$key":    "must be unescaped",
		"also . bad": "same thing",
		".":          "even by itself",
		"$.":         "or in combination",
	}
}

func (s *bindingsMapSuite) TestSetBSONReportsUnmarshalErrors(c *gc.C) {
	emptyMap := make(bindingsMap)
	emptyRaw := bson.Raw{}

	err := emptyMap.SetBSON(emptyRaw)
	c.Assert(err, gc.NotNil)
	c.Assert(err, gc.ErrorMatches, "Unknown element kind .*")
}

func (s *bindingsMapSuite) TestSetBSONUnescapesSpecialCharactersInKeys(c *gc.C) {
	bsonBytes, err := bson.Marshal(s.escapedMapWithBadKeys)
	c.Assert(err, jc.ErrorIsNil)

	rawBSONWithBadKeys := bson.Raw{
		Kind: 0x03, // Document
		Data: bsonBytes,
	}

	inputMap := make(bindingsMap)
	err = inputMap.SetBSON(rawBSONWithBadKeys)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(inputMap, jc.DeepEquals, s.unescapedMapWithBadKeys)
}

func (s *bindingsMapSuite) TestGetBSONReturnsNonNilMapWithEmptyOrNilInput(c *gc.C) {
	var nilMap bindingsMap
	emptyMap := make(bindingsMap)

	for _, inputMap := range []bindingsMap{nilMap, emptyMap} {
		result, err := inputMap.GetBSON()
		c.Check(err, jc.ErrorIsNil)
		c.Check(result, gc.NotNil)
		c.Check(result, gc.FitsTypeOf, bindingsMap{})
	}
}

func (s *bindingsMapSuite) TestGetBSONEscapesSpecialCharactersInKeys(c *gc.C) {
	result, err := s.unescapedMapWithBadKeys.GetBSON()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(result, gc.NotNil)
	c.Assert(result, gc.FitsTypeOf, map[string]string{})

	escapedMap := result.(map[string]string)
	c.Assert(escapedMap, jc.DeepEquals, s.escapedMapWithBadKeys)
}
