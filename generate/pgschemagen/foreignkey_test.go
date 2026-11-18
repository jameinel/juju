// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"database/sql"
	"testing"

	"github.com/juju/tc"
	_ "github.com/mattn/go-sqlite3"

	"github.com/juju/juju/internal/database"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	databasetesting "github.com/juju/juju/internal/database/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

const foreignKeyFixture = `
CREATE TABLE parent (id INT PRIMARY KEY, code TEXT UNIQUE);
CREATE TABLE child (id INT PRIMARY KEY, parent_id INT,
    CONSTRAINT child_parent FOREIGN KEY (parent_id)
        REFERENCES parent (id) ON UPDATE CASCADE ON DELETE CASCADE);
CREATE TABLE node (id INT PRIMARY KEY, parent_id INT,
    FOREIGN KEY (parent_id) REFERENCES node (id));
CREATE TABLE composite_parent (a INT, b TEXT);
CREATE UNIQUE INDEX composite_key ON composite_parent (a, b);
CREATE TABLE composite_child (x TEXT, y INT,
    FOREIGN KEY (x, y) REFERENCES composite_parent (b, a) ON DELETE SET NULL);
`

type foreignKeySuite struct {
	coretesting.BaseSuite
}

func TestForeignKeySuite(t *testing.T) {
	tc.Run(t, &foreignKeySuite{})
}

func (s *foreignKeySuite) TestSQLiteForeignKeys(c *tc.C) {
	db, err := sql.Open("sqlite3", "file::memory:?_foreign_keys=1")
	c.Assert(err, tc.ErrorIsNil)
	db.SetMaxOpenConns(1)
	c.Cleanup(func() { c.Check(db.Close(), tc.ErrorIsNil) })
	checkForeignKeyBehavior(c, db, foreignKeyFixture)
}

func (s *foreignKeySuite) TestPostgreSQLForeignKeys(c *tc.C) {
	db := pgtesting.Open(c)
	translated, err := newTranslator().translatePatch(foreignKeyFixture)
	c.Assert(err, tc.ErrorIsNil)
	checkForeignKeyBehavior(c, db, translated)
	var deferrable bool
	err = db.QueryRowContext(c.Context(), `SELECT condeferrable FROM pg_constraint
		WHERE conname = 'child_parent' AND conrelid = 'child'::regclass`).Scan(&deferrable)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(deferrable, tc.IsFalse)
}

type dqliteForeignKeySuite struct {
	databasetesting.DqliteSuite
}

func TestDqliteForeignKeySuite(t *testing.T) {
	tc.Run(t, &dqliteForeignKeySuite{})
}

func (s *dqliteForeignKeySuite) TestForeignKeys(c *tc.C) {
	checkForeignKeyBehavior(c, s.DB(), foreignKeyFixture)
}

func checkForeignKeyBehavior(c *tc.C, db *sql.DB, ddl string) {
	tokens, err := lex(ddl)
	c.Assert(err, tc.ErrorIsNil)
	statements, err := splitStatements(tokens, false)
	c.Assert(err, tc.ErrorIsNil)
	for _, stmt := range statements {
		if len(significant(stmt.body)) == 0 {
			continue
		}
		_, err = db.ExecContext(c.Context(), stmt.text())
		c.Assert(err, tc.ErrorIsNil, tc.Commentf("%s", stmt.text()))
	}
	for _, q := range []string{
		"INSERT INTO parent VALUES (1, 'one')",
		"INSERT INTO child VALUES (1, 1)",
		"INSERT INTO child VALUES (2, NULL)",
		"INSERT INTO node VALUES (1, 1)",
		"INSERT INTO composite_parent VALUES (10, 'ten')",
		"INSERT INTO composite_child VALUES ('ten', 10)",
		"INSERT INTO composite_child VALUES (NULL, 99)",
	} {
		_, err := db.ExecContext(c.Context(), q)
		c.Assert(err, tc.ErrorIsNil, tc.Commentf("%s", q))
	}
	_, err = db.ExecContext(c.Context(), "INSERT INTO child VALUES (3, 99)")
	c.Check(database.IsErrConstraintForeignKey(err), tc.IsTrue)
	_, err = db.ExecContext(c.Context(), "UPDATE child SET parent_id = 99 WHERE id = 1")
	c.Check(database.IsErrConstraintForeignKey(err), tc.IsTrue)
	_, err = db.ExecContext(c.Context(), "INSERT INTO composite_child VALUES ('missing', 10)")
	c.Check(database.IsErrConstraintForeignKey(err), tc.IsTrue)
	tx, err := db.BeginTx(c.Context(), nil)
	c.Assert(err, tc.ErrorIsNil)
	_, err = tx.ExecContext(c.Context(), "INSERT INTO child VALUES (4, 99)")
	c.Check(database.IsErrConstraintForeignKey(err), tc.IsTrue)
	c.Assert(tx.Rollback(), tc.ErrorIsNil)
	_, err = db.ExecContext(c.Context(), "UPDATE parent SET id = 2 WHERE id = 1")
	c.Assert(err, tc.ErrorIsNil)
	var parentID int
	err = db.QueryRowContext(c.Context(), "SELECT parent_id FROM child WHERE id = 1").Scan(&parentID)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(parentID, tc.Equals, 2)
	_, err = db.ExecContext(c.Context(), "DELETE FROM parent WHERE id = 2")
	c.Assert(err, tc.ErrorIsNil)
	var count int
	err = db.QueryRowContext(c.Context(), "SELECT count(*) FROM child").Scan(&count)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(count, tc.Equals, 1)
	_, err = db.ExecContext(c.Context(), "DELETE FROM composite_parent WHERE a = 10")
	c.Assert(err, tc.ErrorIsNil)
	err = db.QueryRowContext(c.Context(), "SELECT count(*) FROM composite_child WHERE x IS NULL AND y IS NULL").Scan(&count)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(count, tc.Equals, 1)
}
