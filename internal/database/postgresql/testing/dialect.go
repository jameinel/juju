// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"fmt"
	"os"

	"github.com/juju/juju/core/database/dialect"
)

// schemaChangeKeyPrefix is the bytes of "jujut" as a big-endian integer. It
// fills the top 40 bits of a test process's schema change lock key, so the
// key never equals the production key, the bytes of "juju_ddl".
const schemaChangeKeyPrefix = 0x6a756a7574

// Dialect is the PostgreSQL dialect for the schemas that tests apply. It
// differs from [dialect.PostgreSQL] only in the advisory lock that begins a
// schema change: each test process takes its own, rather than the one that
// every Juju process using the database shares.
//
// The shared lock stops concurrent model schema changes from exhausting the
// server's lock table. Test schemas live only as long as their test, and
// the test server's lock table is sized for the test packages that run in
// parallel, so serialising every schema change across those packages would
// only slow them down. Schema changes within a process still run one at a
// time.
var Dialect dialect.Dialect = processDialect{
	Dialect: dialect.PostgreSQL,
	key:     schemaChangeKey(os.Getpid()),
}

// schemaChangeKey returns the schema change lock key of the test process
// with the given PID.
func schemaChangeKey(pid int) int64 {
	return schemaChangeKeyPrefix<<24 | int64(pid)&0xffffff
}

type processDialect struct {
	dialect.Dialect
	key int64
}

// BeginSchemaChangeSQL returns the statements of [dialect.PostgreSQL] with
// the process's lock key.
func (d processDialect) BeginSchemaChangeSQL() string {
	return fmt.Sprintf(`
SET TRANSACTION ISOLATION LEVEL READ COMMITTED;
SELECT pg_advisory_xact_lock(%d);
`, d.key)
}
