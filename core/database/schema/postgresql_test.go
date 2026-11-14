// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"context"
	"database/sql"
	"testing"

	"github.com/juju/tc"

	coredatabase "github.com/juju/juju/core/database"
	"github.com/juju/juju/core/database/dialect"
	"github.com/juju/juju/internal/database/drivererrors"
	pgtesting "github.com/juju/juju/internal/database/postgresql/testing"
	databasetesting "github.com/juju/juju/internal/database/testing"
	coretesting "github.com/juju/juju/internal/testing"
)

type postgresqlSchemaSuite struct {
	coretesting.BaseSuite
	db     *sql.DB
	runner coredatabase.TxnRunner
}

func TestPostgreSQLSchemaSuite(t *testing.T) {
	tc.Run(t, &postgresqlSchemaSuite{})
}

func (s *postgresqlSchemaSuite) SetUpTest(c *tc.C) {
	s.BaseSuite.SetUpTest(c)
	s.db = pgtesting.Open(c)
	s.runner = databasetesting.NewTxnRunner(s.db)
}

func (s *postgresqlSchemaSuite) TestFreshAndIdenticalSchema(c *tc.C) {
	patches := []Patch{
		MakePatch("CREATE TABLE example (id INT PRIMARY KEY, value BOOLEAN);"),
		MakePatch("INSERT INTO example VALUES (1, TRUE);"),
	}
	schema := NewForDialect(dialect.PostgreSQL, patches...)
	change, err := schema.Ensure(c.Context(), s.runner)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(change, tc.DeepEquals, ChangeSet{Current: 0, Post: 2})

	rows, err := s.db.QueryContext(c.Context(), "SELECT version, hash, updated_at IS NOT NULL FROM schema ORDER BY version")
	c.Assert(err, tc.ErrorIsNil)
	defer func() { c.Check(rows.Close(), tc.ErrorIsNil) }()
	hashes := computeHashes(patches)
	var count int
	for rows.Next() {
		var version int
		var hash string
		var timestamp bool
		c.Assert(rows.Scan(&version, &hash, &timestamp), tc.ErrorIsNil)
		c.Assert(count < len(hashes), tc.IsTrue)
		c.Check(version, tc.Equals, count+1)
		c.Check(hash, tc.Equals, hashes[count])
		c.Check(timestamp, tc.IsTrue)
		count++
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	c.Check(count, tc.Equals, 2)

	change, err = schema.Ensure(c.Context(), s.runner)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(change, tc.DeepEquals, ChangeSet{Current: 2, Post: 2})
	c.Assert(s.db.QueryRowContext(c.Context(), "SELECT count(*) FROM example").Scan(&count), tc.ErrorIsNil)
	c.Check(count, tc.Equals, 1)
}

func (s *postgresqlSchemaSuite) TestRejectsIncrementalUpgrade(c *tc.C) {
	schema := NewForDialect(dialect.PostgreSQL, MakePatch("CREATE TABLE example (id INT PRIMARY KEY);"))
	_, err := schema.Ensure(c.Context(), s.runner)
	c.Assert(err, tc.ErrorIsNil)
	schema.Add(MakePatch("ALTER TABLE example ADD COLUMN value TEXT;"))
	_, err = schema.Ensure(c.Context(), s.runner)
	c.Check(err, tc.ErrorMatches, "PostgreSQL schema upgrades are not supported: current version 1, expected 2")
	var count int
	c.Assert(s.db.QueryRowContext(c.Context(), "SELECT count(*) FROM schema").Scan(&count), tc.ErrorIsNil)
	c.Check(count, tc.Equals, 1)
}

func (s *postgresqlSchemaSuite) TestRejectsChangedAndNewerSchema(c *tc.C) {
	first := MakePatch("CREATE TABLE example (id INT PRIMARY KEY);")
	second := MakePatch("INSERT INTO example VALUES (1);")
	schema := NewForDialect(dialect.PostgreSQL, first, second)
	_, err := schema.Ensure(c.Context(), s.runner)
	c.Assert(err, tc.ErrorIsNil)
	_, err = NewForDialect(dialect.PostgreSQL, first).Ensure(c.Context(), s.runner)
	c.Check(err, tc.ErrorMatches, "querying current schema version: number of computed hashes .*")
	_, err = NewForDialect(dialect.PostgreSQL, first,
		MakePatch("INSERT INTO example VALUES (2);")).Ensure(c.Context(), s.runner)
	c.Check(err, tc.ErrorMatches, "querying current schema version: hash mismatch for version 2")
}

func (s *postgresqlSchemaSuite) TestRollsBackPartialApplication(c *tc.C) {
	schema := NewForDialect(dialect.PostgreSQL,
		MakePatch("CREATE TABLE example (id INT PRIMARY KEY);"),
		MakePatch("INSERT INTO absent_table VALUES (1);"),
	)
	_, err := schema.Ensure(c.Context(), s.runner)
	c.Check(err, tc.ErrorMatches, "applying schema patches: failed to apply patch 1: .*")
	c.Check(drivererrors.IsError(err), tc.IsTrue)
	var example, bookkeeping sql.NullString
	err = s.db.QueryRowContext(c.Context(), "SELECT to_regclass('example')::text, to_regclass('schema')::text").
		Scan(&example, &bookkeeping)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(example.Valid, tc.IsFalse)
	c.Check(bookkeeping.Valid, tc.IsFalse)
}

func (s *postgresqlSchemaSuite) TestCancellation(c *tc.C) {
	ctx, cancel := context.WithCancel(c.Context())
	cancel()
	_, err := NewForDialect(dialect.PostgreSQL, MakePatch("SELECT 1")).Ensure(ctx, s.runner)
	c.Check(err, tc.ErrorIs, context.Canceled)
}

func (s *postgresqlSchemaSuite) TestConcurrentApplication(c *tc.C) {
	schema := NewForDialect(dialect.PostgreSQL,
		MakePatch("CREATE TABLE example (id INT PRIMARY KEY);"),
		MakePatch("INSERT INTO example VALUES (1);"),
	)
	type result struct {
		change ChangeSet
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			change, err := schema.Ensure(c.Context(), s.runner)
			results <- result{change: change, err: err}
		}()
	}
	var fresh, identical int
	for range 2 {
		result := <-results
		c.Assert(result.err, tc.ErrorIsNil)
		c.Check(result.change.Post, tc.Equals, 2)
		switch result.change.Current {
		case 0:
			fresh++
		case 2:
			identical++
		default:
			c.Errorf("unexpected prior version %d", result.change.Current)
		}
	}
	c.Check(fresh, tc.Equals, 1)
	c.Check(identical, tc.Equals, 1)
}
