// Copyright 2023 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"context"
	"database/sql"
	"time"

	"github.com/canonical/sqlair"
	"github.com/juju/tc"

	"github.com/juju/juju/domain/cloudimagemetadata"
	"github.com/juju/juju/internal/errors"
)

// retrieveMetadataFromDB retrieves all metadata from the cloud_image_metadata database table.
// It joins the architecture table to fetch architecture-related details and returns the metadata slice.
//
// It is used in test to keep save and find tests independent of each other
func (s *stateSuite) retrieveMetadataFromDB(c *tc.C) ([]cloudimagemetadata.Metadata, error) {
	var metadata []cloudimagemetadata.Metadata
	err := s.TxnRunner().StdTxn(c.Context(), func(ctx context.Context, tx *sql.Tx) error {
		metadata = nil

		rows, err := tx.Query(`
SELECT 
    created_at,
    source,
    stream,
    region,
    version,
    virt_type,
    root_storage_type,
    root_storage_size,
    priority,
    arch.name as archName,
    image_id
FROM cloud_image_metadata
JOIN architecture arch on cloud_image_metadata.architecture_id = arch.id
`)
		if err != nil {
			return errors.Capture(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var dbMetadata cloudimagemetadata.Metadata
			if err = rows.Scan(
				&dbMetadata.CreationTime,
				&dbMetadata.Source,
				&dbMetadata.Stream,
				&dbMetadata.Region,
				&dbMetadata.Version,
				&dbMetadata.VirtType,
				&dbMetadata.RootStorageType,
				&dbMetadata.RootStorageSize,
				&dbMetadata.Priority,
				&dbMetadata.Arch,
				&dbMetadata.ImageID,
			); err != nil {
				return errors.Capture(err)
			}
			metadata = append(metadata, dbMetadata)
		}
		return errors.Capture(err)
	})
	return metadata, err
}

// createdAt holds the creation times that test queries bind. SQLite compares
// them as text with the expiry time that the state binds, so they come from
// the same wall clock and time zone.
type createdAt struct {
	Now          time.Time `db:"now"`
	TwoDaysAgo   time.Time `db:"two_days_ago"`
	ThreeDaysAgo time.Time `db:"three_days_ago"`
}

// runQuery executes the provided SQL query string using the current state's database connection.
// The query binds creation times with $createdAt.now, $createdAt.two_days_ago
// and $createdAt.three_days_ago.
//
// It is a convenient function to set up test with a specific database state.
func (s *stateSuite) runQuery(c *tc.C, query string) error {
	db, err := s.state.DB(c.Context())
	if err != nil {
		return err
	}
	now := time.Now()
	times := createdAt{
		Now:          now,
		TwoDaysAgo:   now.Add(-48 * time.Hour),
		ThreeDaysAgo: now.Add(-72 * time.Hour),
	}
	stmt, err := sqlair.Prepare(query, times)
	if err != nil {
		return err
	}
	return db.Txn(c.Context(), func(ctx context.Context, tx *sqlair.TX) error {
		return tx.Query(ctx, stmt, times).Run()
	})
}

// filter returns a new slice containing only the elements of the input slice that satisfy the accept function.
func filter[S any](input []S, accept func(S) bool) []S {
	result := make([]S, 0, len(input))
	for _, item := range input {
		if accept(item) {
			result = append(result, item)
		}
	}
	return result
}
