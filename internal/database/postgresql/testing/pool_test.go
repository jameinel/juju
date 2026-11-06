// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

//go:build linux || darwin

package testing

import (
	"os"
	"path/filepath"
	stdtesting "testing"

	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type poolSuite struct {
	coretesting.BaseSuite
}

func TestPoolSuite(t *stdtesting.T) {
	tc.Run(t, &poolSuite{})
}

func (s *poolSuite) writePool(c *tc.C, content string) string {
	path := filepath.Join(c.MkDir(), "pool")
	err := os.WriteFile(path, []byte(content), 0o600)
	c.Assert(err, tc.ErrorIsNil)
	return path
}

func (s *poolSuite) lease(c *tc.C, path string) string {
	url, f, err := leasePoolURL(path)
	c.Assert(err, tc.ErrorIsNil)
	c.Cleanup(func() { _ = f.Close() })
	return url
}

func (s *poolSuite) TestLeaseTakesLowestFreeSlot(c *tc.C) {
	path := s.writePool(c, "# comment\nurl-a\n\n  url-b  \n")

	// Each open file description holds its own flock, so leases taken in
	// one process exclude each other as leases from separate processes do.
	c.Check(s.lease(c, path), tc.Equals, "url-a")
	c.Check(s.lease(c, path), tc.Equals, "url-b")
	c.Check(s.lease(c, path), tc.Equals, "url-a")
}

func (s *poolSuite) TestLeaseReusesReleasedSlot(c *tc.C) {
	path := s.writePool(c, "url-a\nurl-b\n")

	url, f, err := leasePoolURL(path)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(url, tc.Equals, "url-a")
	c.Check(s.lease(c, path), tc.Equals, "url-b")

	c.Assert(f.Close(), tc.ErrorIsNil)
	c.Check(s.lease(c, path), tc.Equals, "url-a")
}

func (s *poolSuite) TestLeaseEmptyPool(c *tc.C) {
	path := s.writePool(c, "# nothing\n\n")

	_, _, err := leasePoolURL(path)
	c.Check(err, tc.ErrorMatches, "pool file lists no URLs")
}

func (s *poolSuite) TestLeaseMissingPool(c *tc.C) {
	_, _, err := leasePoolURL(filepath.Join(c.MkDir(), "missing"))
	c.Check(err, tc.ErrorIs, os.ErrNotExist)
}
