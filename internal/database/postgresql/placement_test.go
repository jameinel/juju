// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/juju/tc"

	coretesting "github.com/juju/juju/internal/testing"
)

type placementSuite struct {
	coretesting.BaseSuite
}

func TestPlacementSuite(t *testing.T) {
	tc.Run(t, &placementSuite{})
}

func (s *placementSuite) TestSchemaNameDefaultPrefix(c *tc.C) {
	p := Placement{}
	c.Check(p.SchemaName("controller"), tc.Equals, "juju_controller")
	c.Check(
		p.SchemaName("0b6c6d2e-1c55-4a0c-8f4f-2a3c1e9d7b10"),
		tc.Equals, "juju_0b6c6d2e-1c55-4a0c-8f4f-2a3c1e9d7b10",
	)
}

func (s *placementSuite) TestSchemaNameCustomPrefix(c *tc.C) {
	p := Placement{SchemaPrefix: "juju_test_1_"}
	c.Check(p.SchemaName("controller"), tc.Equals, "juju_test_1_controller")
}

func (s *placementSuite) TestQuoteIdentifier(c *tc.C) {
	c.Check(QuoteIdentifier("juju_controller"), tc.Equals, `"juju_controller"`)
	c.Check(QuoteIdentifier(`a"b`), tc.Equals, `"a""b"`)
}

func (s *placementSuite) TestWithRootCAsEmptyIsNoop(c *tc.C) {
	cfg, err := parseConfig("postgres://u:p@localhost/db?sslmode=disable", WithRootCAs(""))
	c.Assert(err, tc.ErrorIsNil)
	c.Check(cfg.TLSConfig, tc.IsNil)
}

func (s *placementSuite) TestWithRootCAsInvalidPEM(c *tc.C) {
	_, err := parseConfig("postgres://u:p@localhost/db?sslmode=verify-full", WithRootCAs("not a cert"))
	c.Check(err, tc.ErrorMatches, ".*no PEM certificates found")
}

func (s *placementSuite) TestWithRootCAsRejectsUnverifiedModes(c *tc.C) {
	for _, mode := range []string{"disable", "allow", "prefer", "require"} {
		c.Logf("sslmode=%s", mode)
		_, err := parseConfig(
			"postgres://u:p@localhost/db?sslmode="+mode, WithRootCAs(coretesting.CACert),
		)
		c.Check(err, tc.ErrorMatches, ".*requires sslmode=verify-ca or sslmode=verify-full")
	}
}

func (s *placementSuite) TestWithRootCAsVerifyFull(c *tc.C) {
	cfg, err := parseConfig(
		"postgres://u:p@db1,db2/db?sslmode=verify-full", WithRootCAs(coretesting.CACert),
	)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(cfg.TLSConfig, tc.NotNil)
	c.Check(cfg.TLSConfig.RootCAs.Equal(caPool(c, coretesting.CACert)), tc.IsTrue)
	c.Assert(cfg.Fallbacks, tc.HasLen, 1)
	c.Check(cfg.Fallbacks[0].TLSConfig.RootCAs.Equal(caPool(c, coretesting.CACert)), tc.IsTrue)
}

func (s *placementSuite) TestWithRootCAsVerifyCAUsesPool(c *tc.C) {
	serverCert := certDER(c, coretesting.ServerCert)

	cfg, err := parseConfig(
		"postgres://u:p@localhost/db?sslmode=verify-ca", WithRootCAs(coretesting.CACert),
	)
	c.Assert(err, tc.ErrorIsNil)
	c.Assert(cfg.TLSConfig, tc.NotNil)
	c.Check(cfg.TLSConfig.VerifyPeerCertificate([][]byte{serverCert}, nil), tc.ErrorIsNil)

	cfg, err = parseConfig(
		"postgres://u:p@localhost/db?sslmode=verify-ca", WithRootCAs(coretesting.OtherCACert),
	)
	c.Assert(err, tc.ErrorIsNil)
	c.Check(cfg.TLSConfig.VerifyPeerCertificate([][]byte{serverCert}, nil), tc.ErrorMatches, ".*unknown authority.*")
}

func (s *placementSuite) TestDeleteSchemaRejectsShortLockTimeout(c *tc.C) {
	// Zero disables PostgreSQL's lock timeout, so it must not get through.
	for _, timeout := range []time.Duration{-time.Second, 0, time.Millisecond - 1} {
		c.Logf("lock timeout %v", timeout)
		err := DeleteSchema(c.Context(), nil, "juju_x", timeout)
		c.Check(err, tc.ErrorMatches, `deleting schema "juju_x": lock timeout .* is less than a millisecond`)
	}
}

func (s *placementSuite) TestWithDetail(c *tc.C) {
	err := withDetail(fmt.Errorf("dropping: %w", &pgconn.PgError{
		Severity: "ERROR",
		Code:     "2BP01",
		Message:  "cannot drop table t because other objects depend on it",
		Detail:   "view v depends on table t",
		Hint:     "Use DROP ... CASCADE to drop the dependent objects too.",
	}))
	c.Check(err, tc.ErrorMatches,
		`dropping: ERROR: cannot drop table t because other objects depend on it \(SQLSTATE 2BP01\): view v depends on table t`)
	var pgErr *pgconn.PgError
	c.Check(errors.As(err, &pgErr), tc.IsTrue)

	err = withDetail(&pgconn.PgError{
		Severity: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout",
	})
	c.Check(err, tc.ErrorMatches, `ERROR: canceling statement due to lock timeout \(SQLSTATE 55P03\)`)

	plain := errors.New("boom")
	c.Check(withDetail(plain), tc.Equals, plain)
}

func caPool(c *tc.C, caPEM string) *x509.CertPool {
	pool := x509.NewCertPool()
	c.Assert(pool.AppendCertsFromPEM([]byte(caPEM)), tc.IsTrue)
	return pool
}

func certDER(c *tc.C, certPEM string) []byte {
	block, _ := pem.Decode([]byte(certPEM))
	c.Assert(block, tc.NotNil)
	return block.Bytes
}
