// Copyright 2014-2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package bakerystorage

import (
	"errors"
	"time"

	gitjujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/mgo.v2"

	"github.com/juju/juju/mongo"
	"github.com/juju/juju/testing"
)

type StorageSuite struct {
	testing.BaseSuite
	gitjujutesting.Stub
	collection      mockCollection
	closeCollection func()
	clock           *testing.Clock
	config          Config
	store           bakery.Storage
}

var _ = gc.Suite(&StorageSuite{})

func (s *StorageSuite) SetUpTest(c *gc.C) {
	s.BaseSuite.SetUpTest(c)
	s.Stub.ResetCalls()
	s.collection = mockCollection{Stub: &s.Stub}
	s.closeCollection = func() {
		s.AddCall("Close")
		s.PopNoErr()
	}
	s.clock = testing.NewClock(time.Time{})
	s.config = Config{
		GetCollection: func(name string) (mongo.Collection, func()) {
			s.AddCall("GetCollection", name)
			s.PopNoErr()
			return &s.collection, s.closeCollection
		},
		Collection:  "bakery-storage",
		Clock:       s.clock,
		ExpireAfter: time.Minute * 42,
	}
}

func (s *StorageSuite) TestValidateConfigGetCollection(c *gc.C) {
	s.config.GetCollection = nil
	_, err := New(s.config)
	c.Assert(err, gc.ErrorMatches, "validating config: nil GetCollection not valid")
}

func (s *StorageSuite) TestValidateConfigCollection(c *gc.C) {
	s.config.Collection = ""
	_, err := New(s.config)
	c.Assert(err, gc.ErrorMatches, "validating config: empty Collection not valid")
}

func (s *StorageSuite) TestValidateConfigClock(c *gc.C) {
	s.config.Clock = nil
	_, err := New(s.config)
	c.Assert(err, gc.ErrorMatches, "validating config: nil Clock not valid")
}

func (s *StorageSuite) TestValidateConfigExpireAfter(c *gc.C) {
	s.config.ExpireAfter = 0
	_, err := New(s.config)
	c.Assert(err, gc.ErrorMatches, "validating config: unspecified ExpireAfter not valid")
}

func (s *StorageSuite) TestPut(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)

	err = store.Put("foo", "bar")
	c.Assert(err, jc.ErrorIsNil)
	s.CheckCalls(c, []gitjujutesting.StubCall{
		{"GetCollection", []interface{}{s.config.Collection}},
		{"Writeable", nil},
		{"UpsertId", []interface{}{"foo", storageDoc{
			Location: "foo",
			Item:     "bar",
			ExpireAt: s.clock.Now().Add(s.config.ExpireAfter - time.Second),
		}}},
		{"Close", nil},
	})
}

func (s *StorageSuite) TestPutError(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)
	s.SetErrors(nil, nil, errors.New("failed to upsert"))
	err = store.Put("foo", "bar")
	c.Assert(err, gc.ErrorMatches, `cannot store item for location "foo": failed to upsert`)
}

func (s *StorageSuite) TestGet(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)
	item, err := store.Get("foo")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(item, gc.Equals, "item-value")
	s.CheckCalls(c, []gitjujutesting.StubCall{
		{"GetCollection", []interface{}{s.config.Collection}},
		{"FindId", []interface{}{"foo"}},
		{"One", []interface{}{&storageDoc{
			// Set by mock, not in input. Unimportant anyway.
			Location: "foo",
			Item:     "item-value",
		}}},
		{"Close", nil},
	})
}

func (s *StorageSuite) TestGetNotFound(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)
	s.SetErrors(nil, nil, mgo.ErrNotFound)
	_, err = store.Get("foo")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
}

func (s *StorageSuite) TestGetError(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)
	s.SetErrors(nil, nil, errors.New("failed to read"))
	_, err = store.Get("foo")
	c.Assert(err, gc.ErrorMatches, `cannot get item for location "foo": failed to read`)
}

func (s *StorageSuite) TestDel(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)

	err = store.Del("foo")
	c.Assert(err, jc.ErrorIsNil)
	s.CheckCalls(c, []gitjujutesting.StubCall{
		{"GetCollection", []interface{}{s.config.Collection}},
		{"Writeable", nil},
		{"RemoveId", []interface{}{"foo"}},
		{"Close", nil},
	})
}

func (s *StorageSuite) TestDelNotFound(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)
	s.SetErrors(nil, nil, mgo.ErrNotFound)
	err = store.Del("foo")
	c.Assert(err, jc.ErrorIsNil)
}

func (s *StorageSuite) TestDelError(c *gc.C) {
	store, err := New(s.config)
	c.Assert(err, jc.ErrorIsNil)
	s.SetErrors(nil, nil, errors.New("failed to remove"))
	err = store.Del("foo")
	c.Assert(err, gc.ErrorMatches, `cannot remove item for location "foo": failed to remove`)
}

type mockCollection struct {
	mongo.WriteCollection
	*gitjujutesting.Stub
}

func (c *mockCollection) FindId(id interface{}) mongo.Query {
	c.MethodCall(c, "FindId", id)
	c.PopNoErr()
	return &mockQuery{Stub: c.Stub, id: id}
}

func (c *mockCollection) UpsertId(id, update interface{}) (*mgo.ChangeInfo, error) {
	c.MethodCall(c, "UpsertId", id, update)
	return &mgo.ChangeInfo{}, c.NextErr()
}

func (c *mockCollection) RemoveId(id interface{}) error {
	c.MethodCall(c, "RemoveId", id)
	return c.NextErr()
}

func (c *mockCollection) Writeable() mongo.WriteCollection {
	c.MethodCall(c, "Writeable")
	c.PopNoErr()
	return c
}

type mockQuery struct {
	mongo.Query
	*gitjujutesting.Stub
	id interface{}
}

func (q *mockQuery) One(result interface{}) error {
	q.MethodCall(q, "One", result)
	*result.(*storageDoc) = storageDoc{
		Location: q.id.(string),
		Item:     "item-value",
	}
	return q.NextErr()
}
