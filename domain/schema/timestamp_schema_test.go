// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package schema

import (
	"database/sql"
	"time"

	"github.com/juju/tc"
)

func (s *controllerSchemaSuite) TestCurrentTimestampDefaults(c *tc.C) {
	s.applyDDL(c, ControllerDDL())
	checkCurrentTimestampDefaults(c, s.DB(), "controller", true)
}

func (s *modelSchemaSuite) TestCurrentTimestampDefaults(c *tc.C) {
	s.applyDDL(c, ModelDDL())
	checkCurrentTimestampDefaults(c, s.DB(), "model", true)
}

func checkCurrentTimestampDefaults(c *tc.C, db *sql.DB, namespace string, sqlite bool) {
	tests := []struct {
		name  string
		query string
	}{
		{
			"change_log.created_at",
			`INSERT INTO change_log (edit_type_id, namespace_id, changed)
VALUES (1, (SELECT MIN(id) FROM change_log_namespace), 'timestamp-1'),
       (1, (SELECT MIN(id) FROM change_log_namespace), 'timestamp-2')
RETURNING created_at, created_at = CURRENT_TIMESTAMP`,
		},
		{
			"change_log_witness.updated_at",
			`INSERT INTO change_log_witness (controller_id)
VALUES ('timestamp-1'), ('timestamp-2')
RETURNING updated_at, updated_at = CURRENT_TIMESTAMP`,
		},
	}

	switch namespace {
	case "controller":
	case "model":
		for _, query := range []string{
			`INSERT INTO net_node (uuid) VALUES ('timestamp-node-1'), ('timestamp-node-2')`,
			`INSERT INTO machine (uuid, name, net_node_uuid, life_id)
VALUES ('timestamp-machine-1', 'timestamp-1', 'timestamp-node-1', 0),
       ('timestamp-machine-2', 'timestamp-2', 'timestamp-node-2', 0)`,
		} {
			_, err := db.ExecContext(c.Context(), query)
			c.Assert(err, tc.ErrorIsNil)
		}
		tests = append(tests, []struct {
			name  string
			query string
		}{
			{
				"charm.create_time",
				`INSERT INTO charm (uuid, reference_name, revision, architecture_id)
VALUES ('timestamp-charm-1', 'timestamp', 1, 0),
       ('timestamp-charm-2', 'timestamp', 2, 0)
RETURNING create_time, create_time = CURRENT_TIMESTAMP`,
			},
			{
				"machine_requires_reboot.created_at",
				`INSERT INTO machine_requires_reboot (machine_uuid)
VALUES ('timestamp-machine-1'), ('timestamp-machine-2')
RETURNING created_at, created_at = CURRENT_TIMESTAMP`,
			},
			{
				"removal.scheduled_for",
				`INSERT INTO removal (uuid, removal_type_id, entity_uuid)
VALUES ('timestamp-removal-1', 0, 'timestamp-1'),
       ('timestamp-removal-2', 0, 'timestamp-2')
RETURNING scheduled_for, scheduled_for = CURRENT_TIMESTAMP`,
			},
		}...)
	default:
		c.Fatalf("unknown schema namespace %q", namespace)
	}

	for _, test := range tests {
		rows, err := db.QueryContext(c.Context(), test.query)
		c.Assert(err, tc.ErrorIsNil, tc.Commentf("%s", test.name))
		var count int
		for rows.Next() {
			var timestamp time.Time
			var current bool
			c.Assert(rows.Scan(&timestamp, &current), tc.ErrorIsNil, tc.Commentf("%s", test.name))
			c.Check(current, tc.IsTrue, tc.Commentf("%s", test.name))
			c.Check(timestamp.IsZero(), tc.IsFalse, tc.Commentf("%s", test.name))
			if sqlite {
				c.Check(timestamp.Nanosecond(), tc.Equals, 0, tc.Commentf("%s", test.name))
			}
			count++
		}
		c.Assert(rows.Err(), tc.ErrorIsNil, tc.Commentf("%s", test.name))
		c.Assert(rows.Close(), tc.ErrorIsNil)
		c.Check(count, tc.Equals, 2, tc.Commentf("%s", test.name))
	}

	_, err := db.ExecContext(c.Context(), `INSERT INTO change_log_witness (controller_id, updated_at)
VALUES ('legacy-fractional', '2024-01-01 00:00:00.500'),
       ('legacy-whole', '2024-01-01 00:00:00')`)
	c.Assert(err, tc.ErrorIsNil)
	rows, err := db.QueryContext(c.Context(), `SELECT controller_id, updated_at
FROM change_log_witness
WHERE controller_id LIKE 'legacy-%'
ORDER BY updated_at`)
	c.Assert(err, tc.ErrorIsNil)
	defer rows.Close()
	var names []string
	var timestamps []time.Time
	for rows.Next() {
		var name string
		var timestamp time.Time
		c.Assert(rows.Scan(&name, &timestamp), tc.ErrorIsNil)
		names = append(names, name)
		timestamps = append(timestamps, timestamp)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	c.Check(names, tc.DeepEquals, []string{"legacy-whole", "legacy-fractional"})
	c.Assert(timestamps, tc.HasLen, 2)
	c.Check(timestamps[1].Sub(timestamps[0]), tc.Equals, 500*time.Millisecond)
}
