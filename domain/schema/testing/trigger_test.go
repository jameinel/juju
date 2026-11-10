// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing_test

import (
	"testing"

	"github.com/juju/tc"

	"github.com/juju/juju/domain/schema"
	schematesting "github.com/juju/juju/domain/schema/testing"
)

type triggerSuite struct {
	schematesting.ModelSuite
}

func TestTriggerSuite(t *testing.T) {
	tc.Run(t, &triggerSuite{})
}

func (s *triggerSuite) TestFailingTrigger(c *tc.C) {
	s.assertFailingTrigger(c, "")
}

func (s *triggerSuite) TestFailingTriggerWhen(c *tc.C) {
	s.assertFailingTrigger(c, `
NEW.value = 2 AND EXISTS (SELECT 1 FROM trigger_fixture WHERE value = 1)`)
}

func (s *triggerSuite) assertFailingTrigger(c *tc.C, when string) {
	_, err := s.DB().ExecContext(c.Context(), `
CREATE TABLE trigger_fixture (value INT);
INSERT INTO trigger_fixture (value) VALUES (1)`)
	c.Assert(err, tc.ErrorIsNil)

	trigger := schematesting.FailingTrigger{
		Name:    "trg_fixture_update",
		Event:   "BEFORE UPDATE",
		Table:   "trigger_fixture",
		When:    when,
		Message: "can't update fixture",
	}
	_, err = s.DB().ExecContext(c.Context(), schematesting.FailingTriggerSQL(trigger))
	c.Assert(err, tc.ErrorIsNil)

	if when != "" {
		_, err = s.DB().ExecContext(c.Context(), "UPDATE trigger_fixture SET value = 3")
		c.Assert(err, tc.ErrorIsNil)
		_, err = s.DB().ExecContext(c.Context(), "UPDATE trigger_fixture SET value = 1")
		c.Assert(err, tc.ErrorIsNil)
	}

	_, err = s.DB().ExecContext(c.Context(), "UPDATE trigger_fixture SET value = 2")
	c.Assert(err, tc.ErrorMatches, ".*can't update fixture.*")
	s.checkFixtureValue(c, 1)

	_, err = s.DB().ExecContext(c.Context(), schematesting.DropFailingTriggerSQL(trigger))
	c.Assert(err, tc.ErrorIsNil)
	_, err = s.DB().ExecContext(c.Context(), schematesting.DropFailingTriggerSQL(trigger))
	c.Assert(err, tc.ErrorIsNil)
	_, err = s.DB().ExecContext(c.Context(), "UPDATE trigger_fixture SET value = 2")
	c.Assert(err, tc.ErrorIsNil)
	s.checkFixtureValue(c, 2)

	_, err = s.DB().ExecContext(c.Context(), "UPDATE trigger_fixture SET value = 1")
	c.Assert(err, tc.ErrorIsNil)
	_, err = s.DB().ExecContext(c.Context(), schematesting.FailingTriggerSQL(trigger))
	c.Assert(err, tc.ErrorIsNil)
	_, err = s.DB().ExecContext(c.Context(), "UPDATE trigger_fixture SET value = 2")
	c.Assert(err, tc.ErrorMatches, ".*can't update fixture.*")
	s.checkFixtureValue(c, 1)
}

func (s *triggerSuite) checkFixtureValue(c *tc.C, expected int) {
	var value int
	err := s.DB().QueryRowContext(c.Context(), "SELECT value FROM trigger_fixture").Scan(&value)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(value, tc.Equals, expected)
}

func (s *triggerSuite) TestDropSchemaTrigger(c *tc.C) {
	_, err := s.DB().ExecContext(c.Context(), `
INSERT INTO model (uuid, controller_uuid, name, qualifier, type, cloud, cloud_type)
VALUES ('model-uuid', 'controller-uuid', 'before', 'admin', 'iaas', 'cloud', 'lxd')`)
	c.Assert(err, tc.ErrorIsNil)

	_, err = s.DB().ExecContext(c.Context(), "UPDATE model SET name = 'after'")
	c.Assert(err, tc.ErrorMatches, ".*model table is immutable.*")

	for range 2 {
		_, err = s.DB().ExecContext(c.Context(),
			schematesting.DropTriggerSQL("trg_model_immutable_update", "model"))
		c.Assert(err, tc.ErrorIsNil)
	}
	_, err = s.DB().ExecContext(c.Context(), "UPDATE model SET name = 'after'")
	c.Assert(err, tc.ErrorIsNil)
	var name string
	err = s.DB().QueryRowContext(c.Context(), "SELECT name FROM model").Scan(&name)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(name, tc.Equals, "after")

	_, err = s.DB().ExecContext(c.Context(), "DELETE FROM model")
	c.Assert(err, tc.ErrorMatches, ".*model table is immutable.*")

	if schematesting.BackendForTest() == schema.BackendPostgreSQL {
		var functionExists bool
		err = s.DB().QueryRowContext(c.Context(),
			"SELECT to_regprocedure('trg_model_immutable_update()') IS NOT NULL").Scan(&functionExists)
		c.Assert(err, tc.ErrorIsNil)
		c.Check(functionExists, tc.IsTrue)
	}
}
