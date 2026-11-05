// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"time"

	"github.com/juju/tc"

	coresecrets "github.com/juju/juju/core/secrets"
	domainsecret "github.com/juju/juju/domain/secret"
	"github.com/juju/juju/internal/uuid"
)

// These tests cover the queries that report the latest revision of a secret
// when the secret has several revisions, so that every column comes from the
// latest revision rather than from an arbitrary one.

// createSecretRevisions creates a mysql application secret with one revision
// per expiry. A nil expiry creates a revision without an expire time.
func (s *stateSuite) createSecretRevisions(c *tc.C, expiries ...*time.Time) *coresecrets.URI {
	s.setupUnits(c, "mysql")

	uri := coresecrets.NewURI()
	err := s.createCharmApplicationSecret(c, 1, uri, "mysql", domainsecret.UpsertSecretParams{
		Label:          new("my label"),
		Data:           coresecrets.SecretData{"rev": "1"},
		RotatePolicy:   new(domainsecret.RotateDaily),
		NextRotateTime: new(time.Now().Add(time.Hour)),
		ExpireTime:     expiries[0],
		RevisionUUID:   new(uuid.MustNewUUID().String()),
	})
	c.Assert(err, tc.ErrorIsNil)

	for i, expiry := range expiries[1:] {
		err := s.state.UpdateSecret(c.Context(), uri, domainsecret.UpsertSecretParams{
			Data:         coresecrets.SecretData{"rev": string(rune('2' + i))},
			ExpireTime:   expiry,
			RevisionUUID: new(uuid.MustNewUUID().String()),
		})
		c.Assert(err, tc.ErrorIsNil)
	}
	return uri
}

func (s *stateSuite) TestGetRotationExpiryInfoLatestRevision(c *tc.C) {
	now := time.Now().Truncate(time.Second)
	expire1 := now.Add(time.Hour)
	expire2 := now.Add(2 * time.Hour)
	uri := s.createSecretRevisions(c, &expire1, &expire2)

	result, err := s.state.GetRotationExpiryInfo(c.Context(), uri)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result.LatestRevision, tc.Equals, 2)
	c.Check(result.RotatePolicy, tc.Equals, coresecrets.RotateDaily)
	c.Assert(result.LatestExpireTime, tc.NotNil)
	c.Check(result.LatestExpireTime.Equal(expire2), tc.IsTrue)
}

func (s *stateSuite) TestGetRotationExpiryInfoLatestRevisionWithoutExpiry(c *tc.C) {
	now := time.Now().Truncate(time.Second)
	expire1 := now.Add(time.Hour)
	expire2 := now.Add(2 * time.Hour)
	uri := s.createSecretRevisions(c, &expire1, &expire2, nil)

	result, err := s.state.GetRotationExpiryInfo(c.Context(), uri)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result.LatestRevision, tc.Equals, 3)
	c.Check(result.LatestExpireTime, tc.IsNil)
}

func (s *stateSuite) TestUpdateSecretExpiryLatestRevision(c *tc.C) {
	uri := s.createSecretRevisions(c, nil, nil, nil)

	// Updating only the expiry changes the latest revision in place.
	expire := time.Now().Truncate(time.Second).Add(time.Hour)
	err := s.state.UpdateSecret(c.Context(), uri, domainsecret.UpsertSecretParams{
		ExpireTime: &expire,
	})
	c.Assert(err, tc.ErrorIsNil)

	var revisions []int
	rows, err := s.DB().QueryContext(c.Context(), `
SELECT sr.revision
FROM   secret_revision_expire sre
JOIN   secret_revision sr ON sr.uuid = sre.revision_uuid
WHERE  sr.secret_id = ?`, uri.ID)
	c.Assert(err, tc.ErrorIsNil)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rev int
		c.Assert(rows.Scan(&rev), tc.ErrorIsNil)
		revisions = append(revisions, rev)
	}
	c.Assert(rows.Err(), tc.ErrorIsNil)
	c.Check(revisions, tc.DeepEquals, []int{3})

	md, err := s.state.GetSecret(c.Context(), uri)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(md.LatestRevision, tc.Equals, 3)
	c.Assert(md.LatestExpireTime, tc.NotNil)
	c.Check(md.LatestExpireTime.Equal(expire), tc.IsTrue)
}

func (s *stateSuite) TestSecretMetadataLatestRevision(c *tc.C) {
	now := time.Now().Truncate(time.Second)
	expire1 := now.Add(time.Hour)
	expire2 := now.Add(2 * time.Hour)
	uri := s.createSecretRevisions(c, &expire1, &expire2)
	ctx := c.Context()

	check := func(md *coresecrets.SecretMetadata) {
		c.Check(md.URI.ID, tc.Equals, uri.ID)
		c.Check(md.LatestRevision, tc.Equals, 2)
		c.Check(md.Label, tc.Equals, "my label")
		c.Check(md.RotatePolicy, tc.Equals, coresecrets.RotateDaily)
		c.Check(md.Owner, tc.DeepEquals, coresecrets.Owner{
			Kind: coresecrets.ApplicationOwner, ID: "mysql",
		})
		if c.Check(md.LatestExpireTime, tc.NotNil) {
			c.Check(md.LatestExpireTime.Equal(expire2), tc.IsTrue)
		}
	}

	md, err := s.state.GetSecret(ctx, uri)
	c.Assert(err, tc.ErrorIsNil)
	check(md)

	mds, _, err := s.state.ListSecretsByLabels(ctx, domainsecret.Labels{"my label"}, nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(mds, tc.HasLen, 1)
	check(mds[0])

	mds, _, err = s.state.ListAllSecrets(ctx)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(mds, tc.HasLen, 1)
	check(mds[0])

	mds, revs, err := s.state.ListCharmSecrets(ctx, domainsecret.ApplicationOwners{"mysql"}, nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(mds, tc.HasLen, 1)
	check(mds[0])
	c.Check(revs[0], tc.HasLen, 2)
}

func (s *stateSuite) TestGetSecretsRevisionExpiryChangesLatestRevision(c *tc.C) {
	now := time.Now().Truncate(time.Second)
	expire1 := now.Add(time.Hour)
	expire2 := now.Add(2 * time.Hour)
	uri := s.createSecretRevisions(c, &expire1, &expire2, nil)
	ctx := c.Context()
	rev1 := getRevUUID(c, s.DB(), uri, 1)
	rev2 := getRevUUID(c, s.DB(), uri, 2)
	rev3 := getRevUUID(c, s.DB(), uri, 3)
	owners := domainsecret.ApplicationOwners{"mysql"}

	want := func(rev int, revUUID string, expire time.Time) []domainsecret.ExpiryInfo {
		return []domainsecret.ExpiryInfo{{
			URI:             uri,
			Revision:        rev,
			RevisionUUID:    revUUID,
			NextTriggerTime: expire.UTC(),
		}}
	}

	// The latest revision with an expiry is reported.
	result, err := s.state.GetSecretsRevisionExpiryChanges(ctx, owners, nil)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result, tc.DeepEquals, want(2, rev2, expire2))

	// The latest revision is chosen among the requested revisions only.
	result, err = s.state.GetSecretsRevisionExpiryChanges(ctx, owners, nil, rev1)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result, tc.DeepEquals, want(1, rev1, expire1))

	result, err = s.state.GetSecretsRevisionExpiryChanges(ctx, owners, nil, rev1, rev2)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result, tc.DeepEquals, want(2, rev2, expire2))

	result, err = s.state.GetSecretsRevisionExpiryChanges(ctx, owners, nil, rev1, rev3)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result, tc.DeepEquals, want(1, rev1, expire1))

	result, err = s.state.GetSecretsRevisionExpiryChanges(ctx, owners, nil, rev3)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(result, tc.HasLen, 0)

	// The initial watch statement reports the same revision.
	_, f := s.state.InitialWatchStatementForSecretsRevisionExpiryChanges(owners, nil)
	uuids, err := f(ctx, s.TxnRunner())
	c.Assert(err, tc.ErrorIsNil)
	c.Check(uuids, tc.DeepEquals, []string{rev2})
}
