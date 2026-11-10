// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/canonical/sqlair"
	"github.com/mattn/go-sqlite3"

	coredatabase "github.com/juju/juju/core/database"
)

// OpenSQLiteWithCurrentTimestamp opens an in-memory SQLite database whose
// CURRENT_TIMESTAMP uses now. The caller must close the returned database.
func OpenSQLiteWithCurrentTimestamp(now func() time.Time) (*sql.DB, coredatabase.TxnRunner) {
	sqliteDriver := &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.RegisterFunc("current_timestamp", func() string {
				return now().UTC().Format("2006-01-02 15:04:05")
			}, false)
		},
	}
	db := sql.OpenDB(sqliteClockConnector{driver: sqliteDriver})
	db.SetMaxOpenConns(1)
	return db, &txnRunner{db: sqlair.NewDB(db)}
}

type sqliteClockConnector struct {
	driver *sqlite3.SQLiteDriver
}

func (c sqliteClockConnector) Connect(context.Context) (driver.Conn, error) {
	return c.driver.Open(":memory:")
}

func (c sqliteClockConnector) Driver() driver.Driver {
	return c.driver
}
