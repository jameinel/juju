// Copyright 2024 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"math"
	"time"
)

// Lease represents a lease to be serialised to the database.
type Lease struct {
	// UUID is the unique id of the lease.
	UUID string `db:"uuid"`
	// Type is the name of the type.
	Type string `db:"type"`
	// ModelUUID is the UUID of the model the lease is for.
	ModelUUID string `db:"model_uuid"`
	// Name is the name of the lease.
	Name string `db:"name"`
	// Holder is the holder of the lease.
	Holder string `db:"holder"`
	// Start is the lease start time.
	Start time.Time `db:"start"`
	// Expiry is the lease expiry time. Expiry is only used to read expiry
	// times, ExpiryAt is used for writing.
	Expiry time.Time `db:"expiry"`
	// StartAt is the lease start time to write, as a database timestamp.
	StartAt string `db:"start_at"`
	// ExpiryAt is the lease expiry time to write, as a database timestamp.
	ExpiryAt string `db:"expiry_at"`
}

// timestampLayout is the layout of timestamps written to the database. It
// matches the text produced by SQLite's `datetime('now')` and is accepted
// by PostgreSQL timestamp columns.
const timestampLayout = "2006-01-02 15:04:05"

// formatTimestamp returns t as a database timestamp in UTC.
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}

// expiryFrom returns the expiry time for a lease starting at now and held for
// the duration, rounded up to whole seconds.
func expiryFrom(now time.Time, duration time.Duration) time.Time {
	seconds := int64(math.Ceil(duration.Seconds()))
	return now.Truncate(time.Second).Add(time.Duration(seconds) * time.Second)
}

// LeasePin represents a lease pin to be serialised to the database.
type LeasePin struct {
	// UUID is the unique identifier of the lease pin.
	UUID string `db:"uuid"`
	// EntityID is the id of the entity requesting the pin.
	EntityID string `db:"entity_id"`
}

// leadership represents a single row from the leadership table for
// applications.
type leadership struct {
	ModelUUID string `db:"model_uuid"`
	Name      string `db:"name"`
	Holder    string `db:"holder"`
}
