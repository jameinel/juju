package watcher_test

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/state/watcher"
	"github.com/juju/juju/testing"
)

var _ = gc.Suite(&lastRevnoTrackerSuite{})

type lastRevnoTrackerSuite struct {
	testing.BaseSuite
}

type Finder interface {
	Find(string, interface{}) (int64, bool)
}

func checkFind(c *gc.C, tracker Finder, coll string, id interface{}, expectRevno int64, shouldFind bool) {
	revno, found := tracker.Find(coll, id)
	c.Check(revno, gc.Equals, expectRevno)
	c.Check(found, gc.Equals, shouldFind)
}

func (s *lastRevnoTrackerSuite) checkFindAndUpdate(c *gc.C, id interface{}) {
	tracker := watcher.NewRevnoTracker()
	checkFind(c, tracker, "coll", id, -1, false)
	c.Check(tracker.Update("coll", id, 4), jc.IsTrue)
	checkFind(c, tracker, "coll", id, 4, true)
	c.Check(tracker.Update("coll", id, 3), jc.IsTrue)
	checkFind(c, tracker, "coll", id, 3, true)
	c.Check(tracker.Update("coll", id, 3), jc.IsFalse)
	checkFind(c, tracker, "coll", id, 3, true)
	c.Check(tracker.Update("coll", id, -1), jc.IsTrue)
	checkFind(c, tracker, "coll", id, -1, true)
}

type compoundId struct {
	Str string
	Int int
}

func (s *lastRevnoTrackerSuite) TestObject(c *gc.C) {
	s.checkFindAndUpdate(c, compoundId{Str: "foo", Int: 1})
}

func (s *lastRevnoTrackerSuite) TestSimpleString(c *gc.C) {
	s.checkFindAndUpdate(c, "foo")
}

func (s *lastRevnoTrackerSuite) TestUUIDString(c *gc.C) {
	s.checkFindAndUpdate(c, "dead-beef:1")
}

func (s *lastRevnoTrackerSuite) TestMixedTypes(c *gc.C) {
	coll := "collection"
	id1 := compoundId{Str: "foo", Int: 1}
	id2 := compoundId{Str: "foo", Int: 2}
	id3 := "foo"
	id4 := "foo:1"
	id5 := ":1"
	id6 := "foo:"
	tracker := watcher.NewRevnoTracker()
	checkFind(c, tracker, coll, id1, -1, false)
	checkFind(c, tracker, coll, id2, -1, false)
	checkFind(c, tracker, coll, id3, -1, false)
	checkFind(c, tracker, coll, id4, -1, false)
	checkFind(c, tracker, coll, id5, -1, false)
	checkFind(c, tracker, coll, id6, -1, false)
	c.Check(tracker.Update(coll, id1, 4), jc.IsTrue)
	c.Check(tracker.Update(coll, id2, 3), jc.IsTrue)
	c.Check(tracker.Update(coll, id3, 5), jc.IsTrue)
	c.Check(tracker.Update(coll, id4, 2), jc.IsTrue)
	c.Check(tracker.Update(coll, id5, 10), jc.IsTrue)
	c.Check(tracker.Update(coll, id6, 7), jc.IsTrue)
	checkFind(c, tracker, coll, id1, 4, true)
	checkFind(c, tracker, coll, id2, 3, true)
	checkFind(c, tracker, coll, id3, 5, true)
	checkFind(c, tracker, coll, id4, 2, true)
	checkFind(c, tracker, coll, id5, 10, true)
	checkFind(c, tracker, coll, id6, 7, true)
	// ensure that things are stored in the expected locations
	c.Check(watcher.TrackerFindOpaque(tracker, coll, id1), gc.Equals, int64(4))
	c.Check(watcher.TrackerFindOpaque(tracker, coll, id2), gc.Equals, int64(3))
	c.Check(watcher.TrackerFindOpaque(tracker, coll, id3), gc.Equals, int64(5))
	c.Check(watcher.TrackerFindModelId(tracker, coll, "foo", ""), gc.Equals, int64(-1))
	c.Check(watcher.TrackerFindModelId(tracker, coll, "", "foo"), gc.Equals, int64(-1))
	c.Check(watcher.TrackerFindOpaque(tracker, coll, id4), gc.Equals, int64(-1))
	c.Check(watcher.TrackerFindModelId(tracker, coll, "foo", "1"), gc.Equals, int64(2))
	c.Check(watcher.TrackerFindOpaque(tracker, coll, id5), gc.Equals, int64(10))
	c.Check(watcher.TrackerFindModelId(tracker, coll, "", "1"), gc.Equals, int64(-1))
	c.Check(watcher.TrackerFindOpaque(tracker, coll, id6), gc.Equals, int64(7))
	c.Check(watcher.TrackerFindModelId(tracker, coll, "foo", ""), gc.Equals, int64(-1))
}

func (s *lastRevnoTrackerSuite) TestMixedCollections(c *gc.C) {
	tracker := watcher.NewRevnoTracker()
	checkFind(c, tracker, "coll1", "foo:1", -1, false)
	checkFind(c, tracker, "coll1", "foo:2", -1, false)
	checkFind(c, tracker, "coll2", "foo:1", -1, false)
	checkFind(c, tracker, "coll2", "foo:2", -1, false)
	c.Check(tracker.Update("coll1", "foo:1", 3), jc.IsTrue)
	c.Check(tracker.Update("coll1", "foo:2", 5), jc.IsTrue)
	c.Check(tracker.Update("coll2", "foo:1", 2), jc.IsTrue)
	c.Check(tracker.Update("coll2", "foo:2", 6), jc.IsTrue)
	checkFind(c, tracker, "coll1", "foo:1", 3, true)
	checkFind(c, tracker, "coll1", "foo:2", 5, true)
	checkFind(c, tracker, "coll2", "foo:1", 2, true)
	checkFind(c, tracker, "coll2", "foo:2", 6, true)
	c.Check(watcher.TrackerFindModelId(tracker, "coll1", "foo", "1"), gc.Equals, int64(3))
	c.Check(watcher.TrackerFindModelId(tracker, "coll1", "foo", "2"), gc.Equals, int64(5))
	c.Check(watcher.TrackerFindModelId(tracker, "coll2", "foo", "1"), gc.Equals, int64(2))
	c.Check(watcher.TrackerFindModelId(tracker, "coll2", "foo", "2"), gc.Equals, int64(6))
}
