// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"time"
)

// ZeroTime can be used in tests instead of time.Now() when the returned
// time.Time value is not relevant.
//
// Example: instead of now := time.Now() use now := testing.ZeroTime().
func ZeroTime() time.Time {
	return time.Time{}
}

// NonZeroTime can be used in tests instead of time.Now() when the returned
// time.Time value must be non-zero (its IsZero() method returns false).
func NonZeroTime() time.Time {
	return time.Unix(0, 1) // 1 nanosecond since epoch
}

// DatabaseNow returns the current time in UTC at microsecond precision, so it
// can be round-tripped exactly through the supported database timestamp types.
func DatabaseNow() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}
