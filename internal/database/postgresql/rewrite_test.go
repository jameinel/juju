// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"database/sql/driver"
	"testing"

	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type rewriteSuite struct {
	coretesting.BaseSuite
}

func TestRewriteSuite(t *testing.T) {
	tc.Run(t, &rewriteSuite{})
}

func (s *rewriteSuite) TestRewrite(c *tc.C) {
	tests := []struct {
		query string
		want  string
		args  []param
	}{
		{query: "SELECT 1", want: "SELECT 1"},
		{query: "SELECT $1::int, $2::text", want: "SELECT $1::int, $2::text"},
		{query: "SELECT ?, ?", want: "SELECT $1, $2", args: []param{{position: 1}, {position: 2}}},
		{query: "SELECT @sqlair_12, @sqlair_2, @sqlair_12", want: "SELECT $1, $2, $1",
			args: []param{{name: "sqlair_12"}, {name: "sqlair_2"}}},
		{query: "SELECT ?, @sqlair_0, ?, @sqlair_0", want: "SELECT $1, $2, $3, $2",
			args: []param{{position: 1}, {name: "sqlair_0"}, {position: 2}}},
		{query: `SELECT '?', 'it''s ?', E'it\'s ?', "a""?", ?`,
			want: `SELECT '?', 'it''s ?', E'it\'s ?', "a""?", $1`, args: []param{{position: 1}}},
		{query: "SELECT /* a /* ? */ @sqlair_0 */ ? -- ?\n, ?",
			want: "SELECT /* a /* ? */ @sqlair_0 */ $1 -- ?\n, $2", args: []param{{position: 1}, {position: 2}}},
		{query: "SELECT $$ ? @sqlair_0 $$, $tag$ ? $$ $tag$, ?",
			want: "SELECT $$ ? @sqlair_0 $$, $tag$ ? $$ $tag$, $1", args: []param{{position: 1}}},
		{query: "SELECT a$b, ? FROM t$x$", want: "SELECT a$b, $1 FROM t$x$", args: []param{{position: 1}}},
		{query: "SELECT @foo, @sqlair_, @sqlair_1x, a@sqlair_0",
			want: "SELECT @foo, @sqlair_, @sqlair_1x, a@sqlair_0"},
		{query: "SELECT a @> b, a @@ b, ?", want: "SELECT a @> b, a @@ b, $1", args: []param{{position: 1}}},
		{query: "SELECT 1 WHERE 1 IN ()", want: "SELECT 1 WHERE 1 IN ()"},
		{query: "SELECT ? WHERE 1 NOT IN ()", want: "SELECT $1 WHERE 1 NOT IN ()", args: []param{{position: 1}}},
		{query: "SELECT 'unterminated ?", want: "SELECT 'unterminated ?"},
		{query: "SELECT $$ unterminated ?", want: "SELECT $$ unterminated ?"},
		{query: "SELECT /* unterminated ?", want: "SELECT /* unterminated ?"},
		{query: "SELECT 1 -- ?", want: "SELECT 1 -- ?"},
	}
	for _, test := range tests {
		query, args, err := rewrite(test.query)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(query, tc.Equals, test.want)
		c.Check(args, tc.DeepEquals, test.args)
	}
	for _, query := range []string{"SELECT $1, ?", "SELECT @sqlair_0, $2"} {
		_, _, err := rewrite(query)
		c.Check(err, tc.ErrorMatches, "cannot mix PostgreSQL numbered parameters.*")
	}
}

func (s *rewriteSuite) TestBind(c *tc.C) {
	params := []param{{name: "sqlair_1"}, {position: 1}, {name: "sqlair_0"}}
	got, err := bind(params, []driver.NamedValue{
		{Name: "sqlair_0", Value: "zero"},
		{Value: nil},
		{Name: "sqlair_1", Value: "one"},
	})
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.DeepEquals, []driver.NamedValue{
		{Ordinal: 1, Value: "one"}, {Ordinal: 2, Value: nil}, {Ordinal: 3, Value: "zero"},
	})
	for _, args := range [][]driver.NamedValue{
		{{Name: "sqlair_0"}},
		{{Name: "sqlair_0"}, {Name: "sqlair_1"}, {Name: "extra"}},
		{{Name: "sqlair_0"}, {}, {Name: "wrong"}},
		{{Name: "sqlair_0"}, {Name: "sqlair_0"}, {}},
	} {
		_, err := bind(params, args)
		c.Check(err, tc.NotNil)
	}
	args := []driver.NamedValue{{Ordinal: 1, Value: int64(42)}}
	got, err = bind(nil, args)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.DeepEquals, args)
}

func (s *rewriteSuite) TestConfiguration(c *tc.C) {
	for _, connection := range []string{
		"postgres://localhost/example?sslmode=disable",
		"host=localhost dbname=example sslmode=disable",
	} {
		connector, err := NewConnector(connection, WithSearchPath(`a"b`, "schema-with-hyphens"))
		c.Assert(err, tc.ErrorIsNil)
		c.Check(connector, tc.NotNil)
	}
	for _, option := range []Option{WithSearchPath(), WithSearchPath(""), WithSearchPath("a\x00b")} {
		_, err := NewConnector("host=localhost", option)
		c.Check(err, tc.ErrorMatches, "configuring PostgreSQL connection: .*")
	}
	for _, connection := range []string{
		"postgres://user:credential-marker@%invalid",
		"host=localhost password='credential-marker",
	} {
		_, err := NewConnector(connection)
		c.Assert(err, tc.NotNil)
		c.Check(err.Error(), tc.Not(tc.Contains), "credential-marker")
		c.Check(err.Error(), tc.Contains, "invalid PostgreSQL connection configuration")
	}
}
