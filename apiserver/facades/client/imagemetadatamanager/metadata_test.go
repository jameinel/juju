// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package imagemetadatamanager_test

import (
	"github.com/juju/errors"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/environs/config"
	imagetesting "github.com/juju/juju/environs/imagemetadata/testing"
	"github.com/juju/juju/state/cloudimagemetadata"
	sstesting "github.com/juju/juju/environs/simplestreams/testing"
)

type metadataSuite struct {
	baseImageMetadataSuite
}

var _ = gc.Suite(&metadataSuite{})

func (s *metadataSuite) TestFindNil(c *gc.C) {
	found, err := s.api.List(params.ImageMetadataFilter{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(found.Result, gc.HasLen, 0)
	s.assertCalls(c, controllerTag, findMetadata)
}

func (s *metadataSuite) TestFindEmpty(c *gc.C) {
	s.state.findMetadata = func(f cloudimagemetadata.MetadataFilter) (map[string][]cloudimagemetadata.Metadata, error) {
		return map[string][]cloudimagemetadata.Metadata{}, nil
	}

	found, err := s.api.List(params.ImageMetadataFilter{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(found.Result, gc.HasLen, 0)
	s.assertCalls(c, controllerTag, findMetadata)
}

func (s *metadataSuite) TestFindEmptyGroups(c *gc.C) {
	s.state.findMetadata = func(f cloudimagemetadata.MetadataFilter) (map[string][]cloudimagemetadata.Metadata, error) {
		return map[string][]cloudimagemetadata.Metadata{
			"public": []cloudimagemetadata.Metadata{},
			"custom": []cloudimagemetadata.Metadata{},
		}, nil
	}

	found, err := s.api.List(params.ImageMetadataFilter{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(found.Result, gc.HasLen, 0)
	s.assertCalls(c, controllerTag, findMetadata)
}

func (s *metadataSuite) TestFindError(c *gc.C) {
	msg := "find error"
	s.state.findMetadata = func(f cloudimagemetadata.MetadataFilter) (map[string][]cloudimagemetadata.Metadata, error) {
		return nil, errors.New(msg)
	}

	found, err := s.api.List(params.ImageMetadataFilter{})
	c.Assert(err, gc.ErrorMatches, msg)
	c.Assert(found.Result, gc.HasLen, 0)
	s.assertCalls(c, controllerTag, findMetadata)
}

func (s *metadataSuite) TestFindOrder(c *gc.C) {
	customImageId := "custom1"
	customImageId2 := "custom2"
	customImageId3 := "custom3"
	publicImageId := "public1"

	s.state.findMetadata = func(f cloudimagemetadata.MetadataFilter) (map[string][]cloudimagemetadata.Metadata, error) {
		return map[string][]cloudimagemetadata.Metadata{
				"public": []cloudimagemetadata.Metadata{
					cloudimagemetadata.Metadata{ImageId: publicImageId, Priority: 15},
				},
				"custom": []cloudimagemetadata.Metadata{
					cloudimagemetadata.Metadata{ImageId: customImageId, Priority: 87},
					cloudimagemetadata.Metadata{ImageId: customImageId2, Priority: 20},
					cloudimagemetadata.Metadata{ImageId: customImageId3, Priority: 56},
				},
			},
			nil
	}

	found, err := s.api.List(params.ImageMetadataFilter{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(found.Result, gc.HasLen, 4)

	c.Assert(found.Result, jc.SameContents, []params.CloudImageMetadata{
		params.CloudImageMetadata{ImageId: customImageId, Priority: 87},
		params.CloudImageMetadata{ImageId: customImageId3, Priority: 56},
		params.CloudImageMetadata{ImageId: customImageId2, Priority: 20},
		params.CloudImageMetadata{ImageId: publicImageId, Priority: 15},
	})
	s.assertCalls(c, controllerTag, findMetadata)
}

func (s *metadataSuite) TestSaveEmpty(c *gc.C) {
	errs, err := s.api.Save(params.MetadataSaveParams{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(errs.Results, gc.HasLen, 0)
	s.assertCalls(c, controllerTag)
}

func (s *metadataSuite) TestSave(c *gc.C) {
	m := params.CloudImageMetadata{
		Source: "custom",
	}
	msg := "save error"

	saveCalls := 0
	s.state.saveMetadata = func(m []cloudimagemetadata.Metadata) error {
		saveCalls += 1
		c.Assert(m, gc.HasLen, saveCalls)
		// TODO (anastasiamac 2016-08-24) This is a check for a band-aid solution.
		// Once correct value is read from simplestreams, this needs to go.
		// Bug# 1616295
		// Ensure empty stream is changed to release
		c.Assert(m[0].Stream, gc.DeepEquals, "released")
		if saveCalls == 1 {
			// don't err on first call
			return nil
		}
		return errors.New(msg)
	}

	errs, err := s.api.Save(params.MetadataSaveParams{
		Metadata: []params.CloudImageMetadataList{{
			Metadata: []params.CloudImageMetadata{m},
		}, {
			Metadata: []params.CloudImageMetadata{m, m},
		}},
	})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(errs.Results, gc.HasLen, 2)
	c.Assert(errs.Results[0].Error, gc.IsNil)
	c.Assert(errs.Results[1].Error, gc.ErrorMatches, msg)
	s.assertCalls(c, controllerTag, modelConfig, saveMetadata, saveMetadata)
}

func (s *metadataSuite) TestDeleteEmpty(c *gc.C) {
	errs, err := s.api.Delete(params.MetadataImageIds{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(errs.Results, gc.HasLen, 0)
	s.assertCalls(c, controllerTag)
}

func (s *metadataSuite) TestDelete(c *gc.C) {
	idOk := "ok"
	idFail := "fail"
	msg := "delete error"

	s.state.deleteMetadata = func(imageId string) error {
		if imageId == idFail {
			return errors.New(msg)
		}
		return nil
	}

	errs, err := s.api.Delete(params.MetadataImageIds{[]string{idOk, idFail}})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(errs.Results, gc.HasLen, 2)
	c.Assert(errs.Results[0].Error, gc.IsNil)
	c.Assert(errs.Results[1].Error, gc.ErrorMatches, msg)
	s.assertCalls(c, controllerTag, deleteMetadata, deleteMetadata)
}

// useTestImageData causes the given content to be served when published metadata is requested.
func useTestImageData(c *gc.C, files map[string]string) {
	if files != nil {
		sstesting.SetRoundTripperFiles(sstesting.AddSignedFiles(c, files), nil)
	} else {
		sstesting.SetRoundTripperFiles(nil, nil)
	}
}

// TODO (anastasiamac 2015-09-04) This metadata is so verbose.
// Need to generate the text by creating a struct and marshalling it.
var testImagesData = map[string]string{
	"/streams/v1/index.json": `
		{
		 "index": {
		  "com.ubuntu.cloud:released:aws": {
		   "updated": "Wed, 01 May 2013 13:31:26 +0000",
		   "clouds": [
			{
			 "region": "dummy_region",
			 "endpoint": "https://anywhere"
			},
			{
			 "region": "another_dummy_region",
			 "endpoint": ""
			}
		   ],
		   "cloudname": "aws",
		   "datatype": "image-ids",
		   "format": "products:1.0",
		   "products": [
			"com.ubuntu.cloud:server:12.04:amd64",
			"com.ubuntu.cloud:server:14.04:amd64"
		   ],
		   "path": "streams/v1/image_metadata.json"
		   }
		  },
		 "updated": "Wed, 27 May 2015 13:31:26 +0000",
		 "format": "index:1.0"
		}
`,
	"/streams/v1/image_metadata.json": `
{
 "updated": "Wed, 27 May 2015 13:31:26 +0000",
 "content_id": "com.ubuntu.cloud:released:aws",
 "products": {
  "com.ubuntu.cloud:server:14.04:amd64": {
   "release": "trusty",
   "version": "14.04",
   "arch": "amd64",
   "versions": {
    "20140118": {
     "items": {
      "nzww1pe": {
       "root_store": "ebs",
       "virt": "pv",
       "crsn": "da1",
       "id": "ami-36745463"
      },
      "nzww1pe2": {
       "root_store": "ebs",
       "virt": "pv",
       "crsn": "da2",
       "id": "ami-1136745463"
      }
     },
     "pubname": "ubuntu-trusty-14.04-amd64-server-20140118",
     "label": "release"
    }
   }
  },
  "com.ubuntu.cloud:server:12.04:amd64": {
   "release": "precise",
   "version": "12.04",
   "arch": "amd64",
   "versions": {
    "20121218": {
     "items": {
      "usww1pe": {
       "root_store": "ebs",
       "virt": "pv",
       "crsn": "da1",
       "id": "ami-26745463"
      },
      "usww1pe2": {
       "root_store": "ebs",
       "virt": "pv",
       "crsn": "da2",
       "id": "ami-1126745463"
      }
     },
     "pubname": "ubuntu-precise-12.04-amd64-server-20121218",
     "label": "release"
    }
   }
  }
 },
 "_aliases": {
  "crsn": {
   "da1": {
    "region": "dummy_region",
    "endpoint": "https://anywhere"
   },
   "da2": {
    "region": "another_dummy_region",
    "endpoint": ""
   }
  }
 },
 "format": "products:1.0"
}
`,
}

var _ = gc.Suite(&imageMetadataUpdateSuite{})

type imageMetadataUpdateSuite struct {
	baseImageMetadataSuite
}

func (s *imageMetadataUpdateSuite) SetUpSuite(c *gc.C) {
	s.BaseSuite.SetUpSuite(c)
	imagetesting.PatchOfficialDataSources(&s.CleanupSuite, "test:")
	useTestImageData(c, testImagesData)
}

func (s *imageMetadataUpdateSuite) TearDownSuite(c *gc.C) {
	useTestImageData(c, nil)
	s.BaseSuite.TearDownSuite(c)
}


func (s *imageMetadataUpdateSuite) TestSmoke(c *gc.C) {
	// The bulk of the tests are in apiserver/facades/controller/imagemetadata/metadata_test which shares the "common"
	// functionality with this API. This just tests that the suite properly exposes the common function.
	expected := []cloudimagemetadata.Metadata{
			{
				MetadataAttributes: cloudimagemetadata.MetadataAttributes{
					RootStorageType: "ebs",
					VirtType:        "pv",
					Arch:            "amd64",
					Series:          "trusty",
					Region:          "dummy_region",
					Source:          "default cloud images",
					Stream:          "released"},
				Priority: 10,
				ImageId:  "ami-36745463",
			}, {
				MetadataAttributes: cloudimagemetadata.MetadataAttributes{
					RootStorageType: "ebs",
					VirtType:        "pv",
					Arch:            "amd64",
					Series:          "precise",
					Region:          "dummy_region",
					Source:          "default cloud images",
					Stream:          "released"},
				Priority: 10,
				ImageId:  "ami-26745463",
			},
		}

	// testingEnvConfig prepares an environment configuration using
	// mock provider which impelements simplestreams.HasRegion interface.
	s.state.modelConfig = func() (*config.Config, error) {
		s.state.
		cfg := config.New(config.UseDefaults, dummy.SampleConfig())
		return s.env.Config(), nil
	}

	s.state.saveMetadata = func(m []cloudimagemetadata.Metadata) error {
		s.saved = append(s.saved, m...)
		return nil
	}

	func (s *regionMetadataSuite) checkStoredPublished(c *gc.C) {
		err := s.api.UpdateFromPublishedImages()
		c.Assert(err, jc.ErrorIsNil)
		s.assertCalls(c, modelConfig, saveMetadata)
		c.Assert(s.saved, jc.SameContents, s.expected)
	}

	func (s *regionMetadataSuite) TestUpdateFromPublishedImagesForProviderWithRegions(c *gc.C) {
		// This will only save image metadata specific to provider cloud spec.
		s.setExpectations(c)
		s.checkStoredPublished(c)
	}
}