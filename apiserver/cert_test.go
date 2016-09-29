// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package apiserver_test

import (
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/api"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/cert"
	coretesting "github.com/juju/juju/testing"
)

type certSuite struct {
	apiserverBaseSuite
}

var _ = gc.Suite(&certSuite{})

func (s *certSuite) TestUpdateCert(c *gc.C) {
	c.Skip("can't update what you don't have")
	config := s.sampleConfig(c)
	certChanged := make(chan params.StateServingInfo)
	config.CertChanged = certChanged

	srv := s.newServer(c, config)

	// Sanity check that the server works initially.
	conn := s.OpenAPIAsAdmin(c, srv)
	err := conn.Ping()
	c.Assert(err, jc.ErrorIsNil)

	// Create a new certificate that's a year out of date, so we can
	// tell that the server is using it because the connection
	// will fail.
	srvCert, srvKey, err := cert.NewServer(coretesting.CACert, coretesting.CAKey, time.Now().AddDate(-1, 0, 0), nil)
	c.Assert(err, jc.ErrorIsNil)
	info := params.StateServingInfo{
		Cert:       string(srvCert),
		PrivateKey: string(srvKey),
		// No other fields are used by the cert listener.
	}
	certChanged <- info
	// Send the same info again so that we are sure that
	// the previously received information was acted upon
	// (an alternative would be to sleep for a while, but this
	// approach is quicker and more certain).
	certChanged <- info

	// Check that we can't connect to the server because of the bad certificate.
	apiInfo := s.APIInfo(srv)
	apiInfo.Tag = s.Owner
	apiInfo.Password = ownerPassword
	_, err = api.Open(apiInfo, api.DialOpts{})
	c.Assert(err, gc.ErrorMatches, `unable to connect to API: .*: certificate has expired or is not yet valid`)

	// Now change it back and check that we can connect again.
	info = params.StateServingInfo{
		Cert:       coretesting.ServerCert,
		PrivateKey: coretesting.ServerKey,
		// No other fields are used by the cert listener.
	}
	certChanged <- info
	certChanged <- info

	conn = s.OpenAPIAsAdmin(c, srv)
	err = conn.Ping()
	c.Assert(err, jc.ErrorIsNil)
}
