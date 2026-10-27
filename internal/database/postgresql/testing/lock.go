// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"database/sql"
	"time"

	"github.com/juju/tc"
)

// WaitForBlocked waits until another session is waiting on a lock held by
// the blocker backend.
func WaitForBlocked(c *tc.C, db *sql.DB, blocker int) {
	c.Helper()
	for {
		var waiting bool
		err := db.QueryRowContext(c.Context(),
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))",
			blocker,
		).Scan(&waiting)
		c.Assert(err, tc.ErrorIsNil)
		if waiting {
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-c.Context().Done():
			c.Fatal("timed out waiting for a session to block")
		}
	}
}
