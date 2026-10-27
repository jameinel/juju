// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"context"
	"fmt"
	"os"
	stdtesting "testing"

	"github.com/jackc/pgx/v5"
	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type schemaSuite struct {
	coretesting.BaseSuite
}

func TestSchemaSuite(t *stdtesting.T) {
	tc.Run(t, &schemaSuite{})
}

func (s *schemaSuite) TestSchemaPID(c *tc.C) {
	tests := []struct {
		name string
		pid  int
		ok   bool
	}{
		{name: "juju-test-123-4", pid: 123, ok: true},
		{name: "juju-test-123-4-controller", pid: 123, ok: true},
		{name: "juju-test-123-4-", pid: 123, ok: true},
		{name: "juju-test-123", ok: false},
		{name: "juju-test-123-", ok: false},
		{name: "juju-test-abc-4", ok: false},
		{name: "juju-test-123-4x", ok: false},
		{name: "juju_test_123_4", ok: false},
		{name: "juju_controller", ok: false},
		{name: "juju-test-99999999999999999999-1", ok: false},
	}
	for _, test := range tests {
		pid, ok := schemaPID(test.name)
		c.Check(ok, tc.Equals, test.ok, tc.Commentf("%q", test.name))
		c.Check(pid, tc.Equals, test.pid, tc.Commentf("%q", test.name))
	}
}

func (s *schemaSuite) TestQuoteLiteral(c *tc.C) {
	c.Check(quoteLiteral(`plain`), tc.Equals, `'plain'`)
	c.Check(quoteLiteral(`it's`), tc.Equals, `'it''s'`)
	c.Check(quoteLiteral(`back\slash`), tc.Equals, `'back\slash'`)
}

func (s *schemaSuite) TestWithSessionTimeouts(c *tc.C) {
	const all = "lock_timeout=30s&statement_timeout=60s&" +
		"idle_in_transaction_session_timeout=60s"
	tests := []struct {
		conn string
		want string
	}{
		{conn: "", want: ""},
		{
			conn: "postgres://u:p%3Fw@h:5432/db",
			want: "postgres://u:p%3Fw@h:5432/db?" + all,
		},
		{
			conn: "postgresql://h/db?sslmode=disable",
			want: "postgresql://h/db?sslmode=disable&" + all,
		},
		{conn: "postgres://h/db?", want: "postgres://h/db?" + all},
		{
			conn: "postgres://h/db?statement_timeout=5s",
			want: "postgres://h/db?statement_timeout=5s&lock_timeout=30s&" +
				"idle_in_transaction_session_timeout=60s",
		},
		{conn: "postgres://h/db?bad=%zz", want: "postgres://h/db?bad=%zz"},
		{
			conn: "host=h dbname=db",
			want: "host=h dbname=db lock_timeout=30s statement_timeout=60s " +
				"idle_in_transaction_session_timeout=60s",
		},
		{
			conn: "host=h lock_timeout = 1s",
			want: "host=h lock_timeout = 1s statement_timeout=60s " +
				"idle_in_transaction_session_timeout=60s",
		},
	}
	for _, test := range tests {
		c.Check(withSessionTimeouts(test.conn), tc.Equals, test.want,
			tc.Commentf("%q", test.conn))
	}
}

func (s *schemaSuite) TestURLSetsSessionTimeouts(c *tc.C) {
	admin := OpenAdmin(c)

	for name, want := range map[string]string{
		"lock_timeout":                        "30s",
		"statement_timeout":                   "1min",
		"idle_in_transaction_session_timeout": "1min",
	} {
		var got string
		err := admin.QueryRowContext(c.Context(), "SHOW "+name).Scan(&got)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(got, tc.Equals, want, tc.Commentf("%s", name))
	}
}

func (s *schemaSuite) TestNewSchemaName(c *tc.C) {
	_ = OpenAdmin(c)

	first := NewSchemaName(c, "")
	second := NewSchemaName(c, "controller")
	prefix := NewSchemaPrefix(c)

	c.Check(first, tc.Matches, fmt.Sprintf(`juju-test-%d-[0-9]+`, os.Getpid()))
	c.Check(second, tc.Matches, fmt.Sprintf(`juju-test-%d-[0-9]+-controller`, os.Getpid()))
	c.Check(prefix, tc.Matches, fmt.Sprintf(`juju-test-%d-[0-9]+-`, os.Getpid()))
	c.Check(second, tc.Not(tc.Equals), first+"-controller")

	pid, ok := schemaPID(prefix + "model")
	c.Check(ok, tc.IsTrue)
	c.Check(pid, tc.Equals, os.Getpid())
}

func (s *schemaSuite) TestCreateSchema(c *tc.C) {
	admin := OpenAdmin(c)
	name := NewSchemaName(c, "it's")

	CreateSchema(c, admin, name)

	var comment string
	err := admin.QueryRowContext(c.Context(), `
SELECT obj_description(oid, 'pg_namespace') FROM pg_namespace WHERE nspname = $1`,
		name).Scan(&comment)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(comment, tc.Equals, fmt.Sprintf("pid=%d started=%d test=%s",
		os.Getpid(), processStarted.Unix(), c.TestName()))
}

func (s *schemaSuite) TestSweepStaleSchemas(c *tc.C) {
	admin := OpenAdmin(c)

	// PIDs above the Linux maximum are never alive, so concurrent sweeps in
	// other processes agree with this one about the dead and self schemas.
	const (
		deadPID = 4194305
		selfPID = 4194306
	)
	live := fmt.Sprintf("juju-test-1-%d-live", os.Getpid())
	dead := fmt.Sprintf("juju-test-%d-1-dead", deadPID)
	self := fmt.Sprintf("juju-test-%d-1-self", selfPID)
	other := fmt.Sprintf("other-test-%d", os.Getpid())
	for _, name := range []string{live, dead, self, other} {
		quoted := pgx.Identifier{name}.Sanitize()
		_, err := admin.ExecContext(c.Context(), "CREATE SCHEMA "+quoted)
		c.Assert(err, tc.ErrorIsNil)
		c.Cleanup(func() {
			_, err := admin.ExecContext(context.Background(),
				"DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
			c.Check(err, tc.ErrorIsNil)
		})
	}

	sweepStaleSchemas(c, admin, selfPID, func(pid int) bool {
		return pid != deadPID && processAlive(pid)
	})

	for name, want := range map[string]bool{
		live:  true,
		dead:  false,
		self:  false,
		other: true,
	} {
		var exists bool
		err := admin.QueryRowContext(c.Context(),
			`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
			name).Scan(&exists)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(exists, tc.Equals, want, tc.Commentf("%q", name))
	}
}
