// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package metadata_test

import (
	"bytes"

	gc "gopkg.in/check.v1"

	"github.com/juju/juju/cmd/juju/metadata"
)

var _ = gc.Suite(&listFormatterSuite{})

type listFormatterSuite struct {}

func (s *listFormatterSuite) TestFormatTabular(c *gc.C) {

	meta := []metadata.MetadataInfo{
		{
			Source: "cloud",
			Series: "trusty",
			Arch: "amd64",
			Region: "us-east-1",
			ImageId: "ami-1234567",
			Stream: "release",
			VirtType: "hpe",
			RootStorageType: "ebs",
		}, {
			Source: "othersource",
			Series: "xenialism",
			Arch: "amd",
			Region: "long-region",
			ImageId: "ami-123",
			Stream: "daily",
			VirtType: "hpe",
			RootStorageType: "bigstorage-type",
		},
	}
	b := bytes.NewBuffer(nil)
	metadata.FormatMetadataTabular(b, meta)
	c.Check(b.String(), gc.Equals,
`Source       Series     Arch   Region       Image id     Stream   Virt Type  Storage Type
cloud        trusty     amd64  us-east-1    ami-1234567  release  hpe        ebs
othersource  xenialism  amd    long-region  ami-123      daily    hpe        bigstorage-type
`)
}