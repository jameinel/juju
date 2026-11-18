// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/juju/tc"

	coreschema "github.com/juju/juju/core/database/schema"
	"github.com/juju/juju/core/version"
	"github.com/juju/juju/domain/schema"
	coretesting "github.com/juju/juju/internal/testing"
)

type generatorSuite struct {
	coretesting.BaseSuite
}

func TestGeneratorSuite(t *testing.T) {
	tc.Run(t, &generatorSuite{})
}

func (s *generatorSuite) TestLexAndSplitPreserveSource(c *tc.C) {
	for _, test := range []struct {
		src    string
		sqlite bool
		count  int
	}{
		{"-- ; user INT\nCREATE TABLE t (s TEXT DEFAULT 'a;''b', \"quoted;name\" TEXT);\n-- tail", true, 1},
		{"/* nested /* ; */ comment */ SELECT `a``b`, 1.25e-2;", true, 1},
		{"CREATE TRIGGER t AFTER INSERT ON x BEGIN\nSELECT CASE WHEN NEW.n > 0 THEN ';' ELSE 'END' END;\nUPDATE x SET n = 1; END; SELECT 2;", true, 2},
		{"CREATE TEMP TRIGGER t AFTER DELETE ON x BEGIN SELECT 1; END;", true, 1},
		{"CREATE FUNCTION f() RETURNS trigger LANGUAGE plpgsql AS $body$BEGIN PERFORM 1; RETURN NEW; END;$body$; SELECT 2;", false, 2},
	} {
		toks, err := lex(test.src)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(render(toks), tc.Equals, test.src)
		stmts, err := splitStatements(toks, test.sqlite)
		c.Assert(err, tc.ErrorIsNil)
		var text strings.Builder
		count := 0
		for _, stmt := range stmts {
			text.WriteString(stmt.text())
			if len(stmt.body) > 0 {
				count++
			}
		}
		c.Check(text.String(), tc.Equals, test.src)
		c.Check(count, tc.Equals, test.count)
	}
}

func (s *generatorSuite) TestMalformedSQL(c *tc.C) {
	for _, src := range []string{
		"'unfinished", "/* unterminated", "/* /* */", "$body$unfinished",
		"$1", "$bad-tag$", "SELECT \x00;", "SELECT \xff;", "SELECT ?;",
		"SELECT @param;", "SELECT 12xyz;", "SELECT 1.2.3;", "SELECT 1e;",
		"CREATE TABLE t (n INT); SELECT 'unfinished",
	} {
		_, err := lex(src)
		c.Check(err, tc.NotNil, tc.Commentf("%q", src))
	}
	for _, src := range []string{
		"SELECT 1", "SELECT (1;", "SELECT 1);", "SELECT CASE WHEN TRUE THEN 1;",
		"CREATE TRIGGER t AFTER INSERT ON x BEGIN SELECT 1;",
		"CREATE TRIGGER t AFTER INSERT ON x BEGIN SELECT 1; END extra;",
		"SELECT END;",
	} {
		toks, err := lex(src)
		c.Assert(err, tc.ErrorIsNil)
		_, err = splitStatements(toks, true)
		c.Check(err, tc.NotNil, tc.Commentf("%q", src))
	}
	_, err := matchParen(nil, 0)
	c.Check(err, tc.ErrorMatches, "expected opening parenthesis")
	for _, src := range []string{"a,,b", "(a", "a)", ""} {
		toks, err := lex(src)
		c.Assert(err, tc.ErrorIsNil)
		_, err = splitTopLevel(toks)
		c.Check(err, tc.NotNil)
	}
}

func (s *generatorSuite) TestTypedTranslation(c *tc.C) {
	src := `-- user DATETIME('now') must remain a comment
CREATE TABLE user (
 id INT PRIMARY KEY,
 value TEXT,
 b BOOLEAN DEFAULT (0),
 data BLOB,
 time DATETIME DEFAULT (STRFTIME('%Y-%m-%d %H:%M:%f', 'NOW', 'utc'))
) STRICT;
INSERT INTO user (value, b, id, data) VALUES
('user; DATETIME(''now'')', 1, -1, X'00FF'), ('line
next', NULL, 2, X'');
ALTER TABLE user ADD COLUMN enabled BOOLEAN DEFAULT 1;
INSERT INTO user (enabled, value) VALUES (0, 'later');
CREATE UNIQUE INDEX idx_user_value ON user (value);`
	text, err := newTranslator().translatePatch(src)
	c.Assert(err, tc.ErrorIsNil)
	for _, want := range []string{
		"-- user DATETIME('now') must remain a comment",
		`CREATE TABLE "user"`,
		`"id" BIGINT PRIMARY KEY`,
		`"b" BOOLEAN DEFAULT FALSE`,
		`"data" BYTEA`,
		`"time" TIMESTAMPTZ DEFAULT date_trunc('milliseconds', statement_timestamp())`,
		`('user; DATETIME(''now'')', TRUE, -1, decode('00ff', 'hex'))`,
		"('line\nnext', NULL, 2, decode('', 'hex'))",
		`ALTER TABLE "user" ADD COLUMN "enabled" BOOLEAN DEFAULT TRUE;`,
		`INSERT INTO "user" ("enabled", "value") VALUES (FALSE, 'later');`,
		`CREATE UNIQUE INDEX "idx_user_value" ON "user" ("value");`,
	} {
		c.Check(strings.Contains(text, want), tc.IsTrue, tc.Commentf("want %q in:\n%s", want, text))
	}
	tr := newTranslator()
	_, err = tr.translatePatch("CREATE TABLE t (b BOOLEAN, n INT);")
	c.Assert(err, tc.ErrorIsNil)
	got, err := tr.translatePatch("INSERT INTO t VALUES (0, 1), ((1), 0);")
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.Equals, "INSERT INTO \"t\" VALUES (FALSE, 1), (TRUE, 0);\n")
}

func (s *generatorSuite) TestQuotedIdentifiersAndKeys(c *tc.C) {
	got, err := newTranslator().translatePatch(`CREATE TABLE "a""b" (
		"i""d" INT NULL, "value" TEXT NOT NULL, b BOOLEAN DEFAULT TRUE,
		CONSTRAINT pair UNIQUE ("i""d", "value"));
		CREATE INDEX "plain" ON "a""b" ("value");
		INSERT INTO "a""b" ("i""d", "value", b) VALUES (+1, 'it''s literal', FALSE);`)
	c.Assert(err, tc.ErrorIsNil)
	for _, want := range []string{
		`CREATE TABLE "a""b"`, `"i""d" BIGINT NULL`, `"value" TEXT NOT NULL`,
		`CONSTRAINT "pair" UNIQUE ("i""d", "value")`,
		`CREATE INDEX "plain" ON "a""b" ("value")`,
		`VALUES (+1, 'it''s literal', FALSE)`,
	} {
		c.Check(strings.Contains(got, want), tc.IsTrue, tc.Commentf("%s", got))
	}
	got, err = newTranslator().translatePatch("/* keep */ CREATE TABLE `quoted``name` (/* inside */ n INT); -- tail")
	c.Assert(err, tc.ErrorIsNil)
	c.Check(strings.Contains(got, `CREATE TABLE "quoted`+"`"+`name"`), tc.IsTrue)
	c.Check(strings.Contains(got, "/* keep */"), tc.IsTrue)
	c.Check(strings.Contains(got, "/* inside */"), tc.IsTrue)
	c.Check(strings.Contains(got, "-- tail"), tc.IsTrue)
}

func (s *generatorSuite) TestForeignKeyTranslation(c *tc.C) {
	tr := newTranslator()
	_, err := tr.translatePatch(`CREATE TABLE parent (a INT, b TEXT, PRIMARY KEY (a, b));
CREATE TABLE other (id INT);
CREATE UNIQUE INDEX other_key ON other (id);`)
	c.Assert(err, tc.ErrorIsNil)
	got, err := tr.translatePatch(`CREATE TABLE child (
		id INT PRIMARY KEY, a INT, b TEXT, other_id INT,
		CONSTRAINT parent_ref FOREIGN KEY (a, b) REFERENCES parent (a, b) ON DELETE CASCADE ON UPDATE NO ACTION,
		FOREIGN KEY (other_id) REFERENCES other (id) ON UPDATE SET NULL);
CREATE TABLE node (id INT PRIMARY KEY, parent_id INT,
	FOREIGN KEY (parent_id) REFERENCES node (id));`)
	c.Assert(err, tc.ErrorIsNil)
	for _, want := range []string{
		`CONSTRAINT "parent_ref" FOREIGN KEY ("a", "b") REFERENCES "parent" ("a", "b") ON DELETE CASCADE ON UPDATE NO ACTION`,
		`CONSTRAINT "child_other_id_fkey" FOREIGN KEY ("other_id") REFERENCES "other" ("id") ON UPDATE SET NULL`,
		`CONSTRAINT "node_parent_id_fkey" FOREIGN KEY ("parent_id") REFERENCES "node" ("id")`,
	} {
		c.Check(strings.Contains(got, want), tc.IsTrue, tc.Commentf("%s", got))
	}
	got, err = tr.translatePatch(`CREATE TABLE child2 (a INT, b TEXT,
		CONSTRAINT parent_ref FOREIGN KEY (a, b) REFERENCES parent (a, b));`)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(strings.Contains(got, `CONSTRAINT "parent_ref" FOREIGN KEY`), tc.IsTrue)
}

func (s *generatorSuite) TestRejectUnsupportedForeignKeys(c *tc.C) {
	for _, src := range []string{
		`CREATE TABLE t (id INT, FOREIGN KEY (id) REFERENCES future (id));`,
		`CREATE TABLE t (id INT, FOREIGN KEY (missing) REFERENCES t (id));`,
		`CREATE TABLE t (id INT, FOREIGN KEY (id) REFERENCES t (missing));`,
		`CREATE TABLE t (id INT, FOREIGN KEY (id) REFERENCES t (id));`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id, id) REFERENCES t (id, id));`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id) REFERENCES t (id, id));`,
		`CREATE TABLE t (id INT PRIMARY KEY, value TEXT, FOREIGN KEY (value) REFERENCES t (id));`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id) REFERENCES t);`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id) REFERENCES t (id) DEFERRABLE);`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id) REFERENCES t (id) MATCH FULL);`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id) REFERENCES t (id) ON DELETE CASCADE ON DELETE RESTRICT);`,
		`CREATE TABLE t (id INT PRIMARY KEY, FOREIGN KEY (id) REFERENCES t (id) ON UPDATE SET SOMETHING);`,
		`CREATE TABLE t (id INT PRIMARY KEY, CONSTRAINT t_pkey FOREIGN KEY (id) REFERENCES t (id));`,
		`CREATE TABLE t (id INT UNIQUE, CONSTRAINT t_id_key FOREIGN KEY (id) REFERENCES t (id));`,
		`CREATE TABLE t (id INT, CONSTRAINT named UNIQUE (id), CONSTRAINT named FOREIGN KEY (id) REFERENCES t (id));`,
		`CREATE TABLE t (id INT PRIMARY KEY, CONSTRAINT same FOREIGN KEY (id) REFERENCES t (id), CONSTRAINT same FOREIGN KEY (id) REFERENCES t (id));`,
		`CREATE TABLE t (id INT PRIMARY KEY, CONSTRAINT wrong_pkey FOREIGN KEY (id) REFERENCES t (id));`,
		`CREATE TABLE p (id INT); CREATE INDEX i ON p (id); CREATE TABLE t (id INT, FOREIGN KEY (id) REFERENCES p (id));`,
	} {
		_, err := newTranslator().translatePatch(src)
		c.Check(err, tc.NotNil, tc.Commentf("%s", src))
	}
}

func (s *generatorSuite) TestStorageCompatibilityMap(c *tc.C) {
	for _, test := range []struct{ table, col string }{
		{"user_password", "password_salt"}, {"macaroon_root_key", "id"},
		{"charm", "lxd_profile"}, {"charm_metadata", "assumes"},
		{"charm_action", "params"}, {"machine_status", "data"},
		{"machine_cloud_instance_status", "data"}, {"operator_status", "data"},
		{"application_status", "data"}, {"unit_agent_status", "data"},
		{"unit_workload_status", "data"}, {"k8s_pod_status", "data"},
		{"application_remote_offerer", "macaroon"}, {"application_remote_offerer_status", "data"},
		{"application_remote_offerer_relation_macaroon", "macaroon"},
	} {
		src := fmt.Sprintf("CREATE TABLE %s (%s TEXT, ordinary TEXT);", test.table, test.col)
		got, err := newTranslator().translatePatch(src)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(strings.Contains(got, quoteIdent(test.col)+" BYTEA"), tc.IsTrue)
		c.Check(strings.Contains(got, `"ordinary" TEXT`), tc.IsTrue)
	}
}

func (s *generatorSuite) TestTimestampExpressions(c *tc.C) {
	for _, test := range []struct{ src, want string }{
		{"CURRENT_TIMESTAMP", "second"},
		{"DATETIME('now')", "second"},
		{"DATETIME('NoW', 'UTC')", "second"},
		{"STRFTIME('%Y-%m-%d %H:%M:%f', 'NOW', 'utc')", "milliseconds"},
	} {
		toks, err := lex(test.src)
		c.Assert(err, tc.ErrorIsNil)
		got, err := scalar(significant(toks), "TIMESTAMPTZ")
		c.Assert(err, tc.ErrorIsNil)
		c.Check(got, tc.Equals, "date_trunc('"+test.want+"', statement_timestamp())")
	}
	for _, src := range []string{
		"DATETIME('now', '+1 day')", "DATETIME()", "STRFTIME('%Y-%M-%D %H:%M:%F', 'now', 'utc')",
		"STRFTIME('%Y-%m-%d %H:%M:%f', 'now')", "now()", "clock_timestamp()",
	} {
		toks, err := lex(src)
		c.Assert(err, tc.ErrorIsNil)
		_, err = scalar(significant(toks), "TIMESTAMPTZ")
		c.Check(err, tc.NotNil, tc.Commentf("%s", src))
	}
}

func (s *generatorSuite) TestRejectUnsupportedForms(c *tc.C) {
	for _, src := range []string{
		"PRAGMA foreign_keys = ON;", "DROP TABLE t;",
		"CREATE VIEW v AS SELECT 1;",
		"CREATE TRIGGER t AFTER INSERT ON x BEGIN SELECT 1; END;",
		"CREATE TEMP TABLE t (id INT);", "CREATE TABLE IF NOT EXISTS t (id INT);",
		`CREATE TABLE "Mixed" (id INT);`, `CREATE TABLE t ("Id" INT);`,
		"CREATE TABLE t AS SELECT 1;", "CREATE TABLE t (n UNKNOWN);",
		"CREATE TABLE macaroon_root_key (id UNKNOWN);",
		"CREATE TABLE t (n INT) WITHOUT ROWID;", "CREATE TABLE t (n INT) STRICT, WITHOUT ROWID;",
		"CREATE TABLE t (n INT) STRICT EXTRA;", "CREATE TABLE t (n INT, n INT);",
		"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT);",
		"CREATE TABLE t (id INTEGER, PRIMARY KEY (id));",
		"CREATE TABLE t (id INTEGER, CONSTRAINT pk PRIMARY KEY (id));",
		"CREATE TABLE t (id INT REFERENCES other (id));",
		"CREATE TABLE t (id INT, FOREIGN KEY (id) REFERENCES other (id));",
		"CREATE TABLE t (id INT CHECK (id > 0));", "CREATE TABLE t (id INT DEFAULT FOO());",
		"CREATE TABLE t (id INT DEFAULT);", "CREATE TABLE t (id INT DEFAULT -);",
		"CREATE TABLE t (id INT NULL NOT NULL);", "CREATE TABLE t (id INT PRIMARY);",
		"CREATE TABLE t (id INT PRIMARY KEY, PRIMARY KEY (id));",
		"CREATE TABLE t (id INT, UNIQUE (missing));", "CREATE TABLE t (id INT, UNIQUE (id, id));",
		"CREATE TABLE t (id INT, CONSTRAINT special_pkey UNIQUE (id));",
		"CREATE INDEX idx ON missing (id);",
		"CREATE TABLE t (id INT); CREATE INDEX idx ON t (missing);",
		"CREATE TABLE t (id INT); CREATE INDEX idx ON t (id) WHERE id > 0;",
		"CREATE TABLE t (id INT); CREATE INDEX idx ON t (LOWER(id));",
		"CREATE TABLE t (id INT); CREATE INDEX idx_pkey ON t (id);",
		"INSERT INTO missing VALUES (1);",
		"CREATE TABLE t (id INT); INSERT OR IGNORE INTO t VALUES (1);",
		"CREATE TABLE t (id INT); INSERT INTO t SELECT 1;",
		"CREATE TABLE t (id INT); INSERT INTO t VALUES (1), (2, 3);",
		"CREATE TABLE t (id INT); INSERT INTO t (missing) VALUES (1);",
		"CREATE TABLE t (id INT); INSERT INTO t (id,id) VALUES (1,2);",
		"CREATE TABLE t (b BOOLEAN); INSERT INTO t VALUES (2);",
		"CREATE TABLE t (b BOOLEAN); INSERT INTO t VALUES (1+0);",
		"CREATE TABLE t (b BOOLEAN); INSERT INTO t VALUES ('true');",
		"CREATE TABLE t (b BLOB); INSERT INTO t VALUES (X'0');",
		"CREATE TABLE t (b TEXT); INSERT INTO t VALUES ('back\\slash');",
		"CREATE TABLE t (n INT); INSERT INTO t VALUES (1.5);",
		"CREATE TABLE t (n INT); INSERT INTO t VALUES (9223372036854775808);",
		"CREATE TABLE t (n INT); ALTER TABLE t ADD COLUMN n TEXT;",
		"CREATE TABLE t (n INT); ALTER TABLE t ADD COLUMN b INT UNIQUE;",
		"CREATE TABLE t (n INT); ALTER TABLE t DROP COLUMN n;",
		"ALTER TABLE missing ADD COLUMN b INT;",
	} {
		_, err := newTranslator().translatePatch(src)
		c.Check(err, tc.NotNil, tc.Commentf("%s", src))
	}
}

func (s *generatorSuite) TestIdentifierCollisions(c *tc.C) {
	long := strings.Repeat("x", 80)
	c.Check(len(shortIdent(long)), tc.Equals, 63)
	c.Check(shortIdent(long), tc.Equals, shortIdent(long))
	c.Check(shortIdent(long) == shortIdent(long+"y"), tc.IsFalse)
	c.Check(utf8.ValidString(shortIdent(strings.Repeat("é", 40))), tc.IsTrue)
	for _, src := range []string{
		"CREATE TABLE t (id INT); CREATE TABLE t (id INT);",
		"CREATE TABLE t (id INT PRIMARY KEY); CREATE INDEX t_pkey ON t (id);",
		"CREATE TABLE t (id INT PRIMARY KEY); CREATE TABLE t_pkey (n INT);",
		"CREATE TABLE t (id INT, UNIQUE (id)); CREATE INDEX t_id_key ON t (id);",
		"CREATE TABLE t (id INT); CREATE INDEX " + long + " ON t (id); CREATE INDEX " + shortIdent(long) + " ON t (id);",
		"CREATE TABLE " + long + " (id INT);",
		"CREATE TABLE t (" + long + " INT);",
	} {
		_, err := newTranslator().translatePatch(src)
		c.Check(err, tc.NotNil, tc.Commentf("%s", src))
	}
	got, err := newTranslator().translatePatch("CREATE TABLE t (id INT, CONSTRAINT custom PRIMARY KEY (id));")
	c.Assert(err, tc.ErrorIsNil)
	c.Check(strings.Contains(got, `CONSTRAINT "t_pkey" PRIMARY KEY ("id")`), tc.IsTrue)
}

func (s *generatorSuite) TestNormalisePreservesLiteralBytes(c *tc.C) {
	src := "-- comment  \nSELECT 'line  \nnext  ', $tag$body  \n$tag$;  \n\n"
	got, err := normaliseSQL(src)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(got, tc.Equals, "-- comment\nSELECT 'line  \nnext  ', $tag$body  \n$tag$;\n")
}

func (s *generatorSuite) TestExactDiff(c *tc.C) {
	for _, test := range []struct{ src, diff, want string }{
		{"a\nb\nc\n", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n", "a\nB\nc\n"},
		{"prefix\na\nb\n", "--- a\n+++ b\n@@ -1,2 +1,2 @@\n a\n-b\n+B\n", "prefix\na\nB\n"},
		{"a\nb\n", "@@ -1 +1,2 @@\n a\n+middle\n", "a\nmiddle\nb\n"},
		{"a\nb\n", "@@ -2 +1,0 @@\n-b\n", "a\n"},
		{"a", "@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n", "b"},
		{"a\nb\nc\n", "@@ -1 +1 @@\n-a\n+A\n@@ -3 +3 @@\n-c\n+C\n", "A\nb\nC\n"},
	} {
		got, err := applyDiff(test.src, test.diff)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(got, tc.Equals, test.want)
	}
	for _, test := range []struct{ src, diff string }{
		{"a\n", ""}, {"a\n", "not a diff\n"},
		{"a\n", "@@ -0,0 +1 @@\n+new\n"},
		{"a\n", "@@ -1 +1 @@\n a\n"},
		{"a\n", "@@ -1 +1 @@\n-a\n+a\n"},
		{"a\na\n", "@@ -1 +1 @@\n-a\n+b\n"},
		{"a\n", "@@ -1 +1 @@\n-wrong\n+new\n"},
		{"a\n", "@@ -1,2 +1 @@\n-a\n+b\n"},
		{"a\n", "@@ --1 +1 @@\n-a\n+b\n"},
		{"a\n", "@@ -1 +1 @@\n-a\n+b\njunk\n"},
		{"a\n", "--- a\n@@ -1 +1 @@\n-a\n+b\n"},
		{"a\n", "--- a\n+++ b\n@@ -1 +1 @@\n-a\n+b\n--- c\n+++ d\n"},
		{"a\nb\n", "@@ -2 +2 @@\n-b\n+B\n@@ -1 +1 @@\n-a\n+A\n"},
		{"a\nb\n", "@@ -1 +1 @@\n-a\n+A\n@@ -2 +1 @@\n-b\n+B\n"},
		{"a\n", fmt.Sprintf("@@ -%d +1 @@\n-a\n+b\n", math.MaxInt)},
		{"a\n", fmt.Sprintf("@@ -1 +%d @@\n-a\n+b\n", math.MaxInt)},
	} {
		_, err := applyDiff(test.src, test.diff)
		c.Check(err, tc.NotNil, tc.Commentf("%q", test.diff))
	}
}

func (s *generatorSuite) TestGenerateCompleteNamedSequence(c *tc.C) {
	ns := fixtureNamespace("controller")
	out, err := generate(ns, nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(out, tc.HasLen, 3)
	for i, source := range ns.sources {
		name := fmt.Sprintf("sql/%04d-%s.sql", i,
			strings.ReplaceAll(strings.TrimSuffix(path.Base(source.Name), ".sql"), "#", "-"))
		c.Check(strings.Contains(out[name], "-- Source: "+source.Name+"\n"), tc.IsTrue)
		c.Check(strings.Contains(out[name], "-- Source SHA-256: "), tc.IsTrue)
	}
	for range 10 {
		repeated, err := generate(ns, nil)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(repeated, tc.DeepEquals, out)
	}
	ns.sources[1].Patch = coreschema.MakePatch("INSERT INTO t VALUES (?, ?);", "bound", true)
	_, err = generate(ns, nil)
	c.Check(err, tc.ErrorMatches, ".*controller.go#patch-0001: bound arguments.*")
}

func (s *generatorSuite) TestSources(c *tc.C) {
	for _, name := range []string{"controller", "model"} {
		ns := fixtureNamespace(name)
		dir := c.MkDir()
		writeSources(c, dir, ns)
		c.Assert(validateSources(ns, dir), tc.ErrorIsNil)
		// Excluded future files need not exist and are not compared to selected patches.
		writeFile(c, dir, name+"/sql/9999-future.PATCH.sql", "not yet selected")
		c.Assert(validateSources(ns, dir), tc.ErrorIsNil)
		for _, test := range []struct{ name, data, want string }{
			{name + "/sql/extra.PATCH.sql", "SELECT 1;", ".*unexpected or unregistered.*"},
			{name + "/sql/unexpected.txt", "text", ".*unexpected or unregistered.*"},
			{name + "/sql/0000-base.sql", "CREATE TABLE changed (n INT);", ".*differs from compiled schema.*"},
		} {
			fresh := c.MkDir()
			writeSources(c, fresh, ns)
			writeFile(c, fresh, test.name, test.data)
			c.Check(validateSources(ns, fresh), tc.ErrorMatches, test.want)
		}
		c.Assert(os.Remove(filepath.Join(dir, name, "sql", "0002-post.PATCH.sql")), tc.ErrorIsNil)
		c.Check(validateSources(ns, dir), tc.ErrorMatches, ".*missing selected source.*0002-post.PATCH.sql.*")
	}
}

func (s *generatorSuite) TestInvalidSourceInventory(c *tc.C) {
	for _, mutate := range []func(*namespace){
		func(ns *namespace) { ns.sources = nil },
		func(ns *namespace) { ns.sources = append(ns.sources, ns.sources[0]) },
		func(ns *namespace) { ns.sources[1].Name = "controller.go#patch-0002" },
		func(ns *namespace) { ns.sources[0].Name = "dqlite/controller/sql/../escape.sql" },
		func(ns *namespace) { ns.postPatchFiles = nil },
		func(ns *namespace) { ns.postPatchFiles = append(ns.postPatchFiles, ns.postPatchFiles[0]) },
		func(ns *namespace) { ns.postPatchFiles = []string{"../escape.PATCH.sql"} },
		func(ns *namespace) { ns.sources[1].Patch = coreschema.MakePatch("SELECT 1;", 1) },
	} {
		ns := fixtureNamespace("controller")
		dir := c.MkDir()
		writeSources(c, dir, ns)
		mutate(&ns)
		c.Check(validateSources(ns, dir), tc.NotNil)
	}
}

func (s *generatorSuite) TestRealAssembledSources(c *tc.C) {
	for _, ns := range []namespace{{
		"controller", schema.ControllerPatchSourcesForVersion(version.Current), schema.ControllerPostPatchFiles(),
	}, {
		"model", schema.ModelPatchSourcesForVersion(version.Current), schema.ModelPostPatchFiles(),
	}} {
		c.Assert(validateSources(ns, "../../domain/schema/dqlite"), tc.ErrorIsNil)
		if ns.name == "controller" {
			early := ns
			early.sources = early.sources[:3]
			out, err := generate(early, nil)
			c.Assert(err, tc.ErrorIsNil)
			lease := out["sql/0002-0002-lease.sql"]
			c.Check(strings.Contains(lease, `FOREIGN KEY ("lease_type_id") REFERENCES "lease_type" ("id")`), tc.IsTrue)
			c.Check(strings.Contains(lease, `FOREIGN KEY ("lease_uuid") REFERENCES "lease" ("uuid")`), tc.IsTrue)
		}
		_, err := generate(ns, nil)
		// Full-schema generation is deliberately unavailable at this checkpoint.
		want := "(?s)controller source dqlite/controller/sql/0003-changelog.sql: INTEGER PRIMARY KEY allocation is not implemented.*"
		if ns.name == "model" {
			want = "(?s)model source dqlite/model/sql/0001-changelog.sql: INTEGER PRIMARY KEY allocation is not implemented.*"
		}
		c.Check(err, tc.ErrorMatches, want)
	}
}

func (s *generatorSuite) TestRunAndFreshness(c *tc.C) {
	for _, change := range []string{"content", "missing", "extra", "Go patch", "source-only whitespace", "registered patch", "override"} {
		ns := []namespace{fixtureNamespace("controller"), fixtureNamespace("model")}
		src, dest := c.MkDir(), filepath.Join(c.MkDir(), "output")
		for _, n := range ns {
			writeSources(c, src, n)
		}
		c.Assert(run(ns, src, dest, false), tc.ErrorIsNil)
		before := snapshot(c, dest)
		c.Assert(run(ns, src, dest, true), tc.ErrorIsNil)
		c.Check(snapshot(c, dest), tc.DeepEquals, before)
		c.Assert(run(ns, src, dest, false), tc.ErrorIsNil)
		c.Check(snapshot(c, dest), tc.DeepEquals, before)
		switch change {
		case "content":
			writeFile(c, dest, "controller/sql/0000-0000-base.sql", generatedHeader+"drift\n")
		case "missing":
			c.Assert(os.Remove(filepath.Join(dest, "controller/sql/0000-0000-base.sql")), tc.ErrorIsNil)
		case "extra":
			writeFile(c, dest, "controller/sql/stale.sql", generatedHeader+"stale\n")
		case "Go patch":
			ns[0].sources[1].Patch = coreschema.MakePatch("INSERT INTO t (value, b) VALUES ('changed', 1);")
		case "source-only whitespace":
			ns[0].sources[1].Patch = coreschema.MakePatch("INSERT INTO t   (value, b) VALUES ('before', 1);")
		case "registered patch":
			ns[0].sources[2].Patch = coreschema.MakePatch("ALTER TABLE t ADD COLUMN later BOOLEAN DEFAULT 1;")
			writeSources(c, src, ns[0])
		case "override":
			writeFile(c, dest, "controller/overrides/0001-controller.go-patch-0001.diff", fixtureOverride("controller"))
		}
		changed := snapshot(c, dest)
		c.Check(run(ns, src, dest, true), tc.NotNil, tc.Commentf("%s", change))
		c.Check(snapshot(c, dest), tc.DeepEquals, changed, tc.Commentf("%s", change))
		c.Assert(run(ns, src, dest, false), tc.ErrorIsNil)
		c.Assert(run(ns, src, dest, true), tc.ErrorIsNil)
	}
}

func (s *generatorSuite) TestNoPartialOutputOnFailure(c *tc.C) {
	ns := []namespace{fixtureNamespace("controller"), fixtureNamespace("model")}
	src, dest := c.MkDir(), filepath.Join(c.MkDir(), "absent")
	for _, n := range ns {
		writeSources(c, src, n)
	}
	before := snapshot(c, filepath.Dir(dest))
	c.Check(run(ns, src, dest, true), tc.ErrorMatches, "missing generated file.*")
	c.Check(snapshot(c, filepath.Dir(dest)), tc.DeepEquals, before)
	ns[1].sources[1].Patch = coreschema.MakePatch("DROP TABLE t;")
	c.Check(run(ns, src, dest, false), tc.ErrorMatches, "model source model.go#patch-0001: unsupported statement.*")
	c.Check(snapshot(c, filepath.Dir(dest)), tc.DeepEquals, before)
	c.Check(run(ns[:1], src, dest, false), tc.ErrorMatches, "both controller and model.*")
}

func (s *generatorSuite) TestOverrides(c *tc.C) {
	ns := fixtureNamespace("controller")
	override := fixtureOverride("controller")
	out, err := generate(ns, map[string]string{"0001-controller.go-patch-0001.diff": override})
	c.Assert(err, tc.ErrorIsNil)
	c.Check(strings.Contains(out["sql/0001-controller.go-patch-0001.sql"], "'after'"), tc.IsTrue)
	c.Check(strings.Contains(out["overrides/0001-controller.go-patch-0001-auto.sql"], "'before'"), tc.IsTrue)
	for _, invalid := range []string{
		"", strings.Replace(override, "# Reason: fixture", "# Reason: ", 1),
		strings.Replace(override, "Source: controller.go", "Source: model.go", 1),
		strings.Replace(override, "'before'", "'stale'", 1),
		strings.Replace(override, `+INSERT INTO "t"`, `+DELETE FROM "t"`, 1),
	} {
		_, err := generate(ns, map[string]string{"0001-controller.go-patch-0001.diff": invalid})
		c.Check(err, tc.NotNil)
	}
	_, err = generate(ns, map[string]string{"unused.diff": override})
	c.Check(err, tc.ErrorMatches, "controller unused override unused.diff")
}

func (s *generatorSuite) TestOutputOwnership(c *tc.C) {
	for _, file := range []string{"controller/sql/hand.sql", "controller/overrides/hand-auto.sql", "controller/sql/notes.txt"} {
		dir := c.MkDir()
		writeFile(c, dir, file, "handwritten\n")
		writeFile(c, dir, "model/sql/stale.sql", generatedHeader+"stale\n")
		before := snapshot(c, dir)
		for _, check := range []bool{false, true} {
			err := syncOutput(dir, output{"controller/sql/new.sql": generatedHeader + "new\n"}, check)
			c.Check(err, tc.ErrorMatches, "refusing to overwrite or remove unowned file.*")
			c.Check(snapshot(c, dir), tc.DeepEquals, before)
		}
	}
	dir := c.MkDir()
	writeFile(c, dir, "controller/sql/stale.sql", generatedHeader+"stale\n")
	writeFile(c, dir, "controller/overrides/stale-auto.sql", generatedHeader+"stale\n")
	writeFile(c, dir, "controller/overrides/keep.diff", "handwritten\n")
	writeFile(c, dir, "controller/README.txt", "not generated\n")
	c.Assert(syncOutput(dir, output{"model/sql/new.sql": generatedHeader + "new\n"}, false), tc.ErrorIsNil)
	_, err := os.Stat(filepath.Join(dir, "controller/sql/stale.sql"))
	c.Check(err, tc.ErrorIs, fs.ErrNotExist)
	_, err = os.Stat(filepath.Join(dir, "controller/overrides/stale-auto.sql"))
	c.Check(err, tc.ErrorIs, fs.ErrNotExist)
	data, err := os.ReadFile(filepath.Join(dir, "controller/overrides/keep.diff"))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(string(data), tc.Equals, "handwritten\n")
	data, err = os.ReadFile(filepath.Join(dir, "controller/README.txt"))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(string(data), tc.Equals, "not generated\n")
	for _, name := range []string{"../escape.sql", "model/sql/../../escape.sql", "model\\sql\\file.sql", "model/sql/not-sql", "other/sql/a.sql"} {
		c.Check(syncOutput(dir, output{name: generatedHeader}, false), tc.NotNil)
	}
}

func (s *generatorSuite) TestRejectSymlinksAndUnexpectedDirectories(c *tc.C) {
	outside := c.MkDir()
	writeFile(c, outside, "target.sql", generatedHeader+"untouched\n")
	for _, where := range []string{"controller", "controller/sql", "controller/sql/link.sql", "controller/overrides"} {
		root := c.MkDir()
		filename := filepath.Join(root, where)
		c.Assert(os.MkdirAll(filepath.Dir(filename), 0o755), tc.ErrorIsNil)
		target := outside
		if strings.HasSuffix(where, ".sql") {
			target = filepath.Join(outside, "target.sql")
		}
		c.Assert(os.Symlink(target, filename), tc.ErrorIsNil)
		before := snapshot(c, outside)
		for _, check := range []bool{true, false} {
			c.Check(syncOutput(root, output{"controller/sql/new.sql": generatedHeader + "new\n"}, check), tc.NotNil)
			c.Check(snapshot(c, outside), tc.DeepEquals, before)
		}
	}
	ns := fixtureNamespace("controller")
	dir := c.MkDir()
	writeSources(c, dir, ns)
	c.Assert(os.Mkdir(filepath.Join(dir, "controller/sql/unexpected"), 0o755), tc.ErrorIsNil)
	c.Check(validateSources(ns, dir), tc.ErrorMatches, "unexpected non-regular input.*")
	overrides := c.MkDir()
	writeFile(c, overrides, "readme.txt", "unrecognised")
	_, err := readOverrides(overrides)
	c.Check(err, tc.ErrorMatches, "unexpected override file.*")
}

func fixtureNamespace(name string) namespace {
	base := "dqlite/" + name + "/sql/"
	return namespace{
		name: name,
		sources: []schema.PatchSource{
			{Name: base + "0000-base.sql", Patch: coreschema.MakePatch("CREATE TABLE t (value TEXT, b BOOLEAN);")},
			{Name: name + ".go#patch-0001", Patch: coreschema.MakePatch("INSERT INTO t (value, b) VALUES ('before', 1);")},
			{Name: base + "0002-post.PATCH.sql", Patch: coreschema.MakePatch("ALTER TABLE t ADD COLUMN later BOOLEAN DEFAULT 0;")},
		},
		postPatchFiles: []string{base + "9999-future.PATCH.sql", base + "0002-post.PATCH.sql"},
	}
}

func fixtureOverride(name string) string {
	return "# Source: " + name + ".go#patch-0001\n# Reason: fixture\n# Remove-when: fixture is retired\n" +
		"@@ -1 +1 @@\n-INSERT INTO \"t\" (\"value\", \"b\") VALUES ('before', TRUE);\n" +
		"+INSERT INTO \"t\" (\"value\", \"b\") VALUES ('after', TRUE);\n"
}

func writeSources(c *tc.C, dir string, ns namespace) {
	for _, source := range ns.sources {
		if strings.HasSuffix(source.Name, ".sql") {
			rel := strings.TrimPrefix(source.Name, "dqlite/")
			writeFile(c, dir, rel, coreschema.Stmt(source.Patch))
		}
	}
}

func writeFile(c *tc.C, dir, name, data string) {
	filename := filepath.Join(dir, filepath.FromSlash(name))
	c.Assert(os.MkdirAll(filepath.Dir(filename), 0o755), tc.ErrorIsNil)
	c.Assert(os.WriteFile(filename, []byte(data), 0o600), tc.ErrorIsNil)
}

type fileSnapshot struct {
	Mode fs.FileMode
	Time time.Time
	Data string
}

func snapshot(c *tc.C, dir string) map[string]fileSnapshot {
	files := make(map[string]fileSnapshot)
	err := filepath.WalkDir(dir, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		snap := fileSnapshot{Mode: info.Mode(), Time: info.ModTime()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			snap.Data = string(data)
		}
		files[filename] = snap
		return nil
	})
	c.Assert(err, tc.ErrorIsNil)
	return files
}
