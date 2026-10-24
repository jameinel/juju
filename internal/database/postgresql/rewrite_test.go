// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
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

func (s *rewriteSuite) TestTimeZoneOption(c *tc.C) {
	cfg, err := parseConfig("postgres://user:password@localhost/database", WithTimeZoneUTC())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(cfg.RuntimeParams["timezone"], tc.Equals, "UTC")
}

func (s *rewriteSuite) TestRewrite(c *tc.C) {
	named := func(n string) param { return param{name: n} }
	pos := func(i int) param { return param{position: i} }

	tests := []struct {
		name   string
		query  string
		expect string
		params []param
	}{{
		name:   "no placeholders",
		query:  "SELECT 1",
		expect: "SELECT 1",
	}, {
		name:   "empty",
		query:  "",
		expect: "",
	}, {
		name:   "single named",
		query:  "SELECT * FROM t WHERE a = @sqlair_0",
		expect: "SELECT * FROM t WHERE a = $1",
		params: []param{named("sqlair_0")},
	}, {
		name:   "multiple named",
		query:  "INSERT INTO t (a, b) VALUES (@sqlair_0, @sqlair_1)",
		expect: "INSERT INTO t (a, b) VALUES ($1, $2)",
		params: []param{named("sqlair_0"), named("sqlair_1")},
	}, {
		name:   "repeated named maps to one parameter",
		query:  "SELECT @sqlair_0, @sqlair_1, @sqlair_0",
		expect: "SELECT $1, $2, $1",
		params: []param{named("sqlair_0"), named("sqlair_1")},
	}, {
		name:   "multi digit named",
		query:  "SELECT @sqlair_12, @sqlair_2",
		expect: "SELECT $1, $2",
		params: []param{named("sqlair_12"), named("sqlair_2")},
	}, {
		name:   "positional",
		query:  "SELECT * FROM t WHERE a = ? AND b = ?",
		expect: "SELECT * FROM t WHERE a = $1 AND b = $2",
		params: []param{pos(1), pos(2)},
	}, {
		name:   "repeated positional are distinct",
		query:  "SELECT ?, ?",
		expect: "SELECT $1, $2",
		params: []param{pos(1), pos(2)},
	}, {
		name:   "mixed named and positional",
		query:  "SELECT ?, @sqlair_0, ?, @sqlair_0",
		expect: "SELECT $1, $2, $3, $2",
		params: []param{pos(1), named("sqlair_0"), pos(2)},
	}, {
		name:   "string literal",
		query:  "SELECT '?', '@sqlair_0', ?",
		expect: "SELECT '?', '@sqlair_0', $1",
		params: []param{pos(1)},
	}, {
		name:   "doubled quote in string literal",
		query:  "SELECT 'it''s ?', ?",
		expect: "SELECT 'it''s ?', $1",
		params: []param{pos(1)},
	}, {
		name:   "escape string with backslash quote",
		query:  `SELECT E'it\'s ?', ?`,
		expect: `SELECT E'it\'s ?', $1`,
		params: []param{pos(1)},
	}, {
		name:   "plain string backslash is literal",
		query:  `SELECT 'a\', ?`,
		expect: `SELECT 'a\', $1`,
		params: []param{pos(1)},
	}, {
		name:   "quoted identifier",
		query:  `SELECT "what?", "@sqlair_0" FROM t WHERE a = ?`,
		expect: `SELECT "what?", "@sqlair_0" FROM t WHERE a = $1`,
		params: []param{pos(1)},
	}, {
		name:   "doubled quote in identifier",
		query:  `SELECT "a""?", ?`,
		expect: `SELECT "a""?", $1`,
		params: []param{pos(1)},
	}, {
		name:   "line comment",
		query:  "SELECT ? -- is ? or @sqlair_0\n, ?",
		expect: "SELECT $1 -- is ? or @sqlair_0\n, $2",
		params: []param{pos(1), pos(2)},
	}, {
		name:   "line comment to end of input",
		query:  "SELECT ? -- ?",
		expect: "SELECT $1 -- ?",
		params: []param{pos(1)},
	}, {
		name:   "block comment",
		query:  "SELECT /* ? */ ?",
		expect: "SELECT /* ? */ $1",
		params: []param{pos(1)},
	}, {
		name:   "nested block comment",
		query:  "SELECT /* a /* ? */ ? */ ?",
		expect: "SELECT /* a /* ? */ ? */ $1",
		params: []param{pos(1)},
	}, {
		name:   "unterminated block comment",
		query:  "SELECT ? /* ?",
		expect: "SELECT $1 /* ?",
		params: []param{pos(1)},
	}, {
		name:   "dollar quoted empty tag",
		query:  "SELECT $$ ? @sqlair_0 $$, ?",
		expect: "SELECT $$ ? @sqlair_0 $$, $1",
		params: []param{pos(1)},
	}, {
		name:   "dollar quoted with tag",
		query:  "SELECT $fn$ a $$ ? $$ b $fn$, ?",
		expect: "SELECT $fn$ a $$ ? $$ b $fn$, $1",
		params: []param{pos(1)},
	}, {
		name:   "unterminated dollar quote",
		query:  "SELECT $$ ?",
		expect: "SELECT $$ ?",
	}, {
		name:   "existing numbered parameter is untouched",
		query:  "SELECT $1, $2",
		expect: "SELECT $1, $2",
	}, {
		name:   "dollar in identifier is not a quote",
		query:  "SELECT a$b, ? FROM t$x$ WHERE c = ?",
		expect: "SELECT a$b, $1 FROM t$x$ WHERE c = $2",
		params: []param{pos(1), pos(2)},
	}, {
		name:   "jsonb and text search operators",
		query:  "SELECT a @> b, a @@ b, ? FROM t",
		expect: "SELECT a @> b, a @@ b, $1 FROM t",
		params: []param{pos(1)},
	}, {
		name:   "at sign without sqlair prefix",
		query:  "SELECT @foo, @sqlair_, @sqlair_1x, ?",
		expect: "SELECT @foo, @sqlair_, @sqlair_1x, $1",
		params: []param{pos(1)},
	}, {
		name:   "at sign in identifier",
		query:  "SELECT a@sqlair_0",
		expect: "SELECT a@sqlair_0",
	}, {
		name:   "cast",
		query:  "SELECT ?::int, @sqlair_0::text",
		expect: "SELECT $1::int, $2::text",
		params: []param{pos(1), named("sqlair_0")},
	}, {
		name:   "multibyte text",
		query:  "SELECT 'é?', ?",
		expect: "SELECT 'é?', $1",
		params: []param{pos(1)},
	}, {
		name:   "unterminated string",
		query:  "SELECT 'abc ?",
		expect: "SELECT 'abc ?",
	}, {
		name:   "empty in list",
		query:  "SELECT a FROM t WHERE a IN ()",
		expect: "SELECT a FROM t WHERE a = ANY ('{}')",
	}, {
		name:   "empty not in list with parameters",
		query:  "DELETE FROM t WHERE a = @sqlair_0 AND b NOT IN ()",
		expect: "DELETE FROM t WHERE a = $1 AND b <> ALL ('{}')",
		params: []param{named("sqlair_0")},
	}, {
		name:   "empty in list lower case without space",
		query:  "SELECT a FROM t WHERE a not\n  in( \n) AND b in()",
		expect: "SELECT a FROM t WHERE a <> ALL ('{}') AND b = ANY ('{}')",
	}, {
		name:   "non empty in list",
		query:  "SELECT a FROM t WHERE a NOT IN (@sqlair_0, @sqlair_1)",
		expect: "SELECT a FROM t WHERE a NOT IN ($1, $2)",
		params: []param{named("sqlair_0"), named("sqlair_1")},
	}, {
		name:   "empty parentheses that are not an in list",
		query:  "SELECT now(), count( ), login(), a_in (), \"in\" () FROM t",
		expect: "SELECT now(), count( ), login(), a_in (), \"in\" () FROM t",
	}, {
		name:   "quoted identifier before in",
		query:  `SELECT a FROM t WHERE "not" IN ()`,
		expect: `SELECT a FROM t WHERE "not" = ANY ('{}')`,
	}, {
		name:   "empty in list inside literals and comments",
		query:  "SELECT 'a IN ()', \"IN ()\" -- NOT IN ()\n, ?",
		expect: "SELECT 'a IN ()', \"IN ()\" -- NOT IN ()\n, $1",
		params: []param{pos(1)},
	}}

	for _, test := range tests {
		c.Logf("test: %s", test.name)
		got, params := rewrite(test.query)
		c.Check(got, tc.Equals, test.expect)
		c.Check(params, tc.DeepEquals, test.params)
	}
}
