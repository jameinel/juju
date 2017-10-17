// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package imagecommon

import (
	"strings"

	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/utils/series"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/config"
	envmetadata "github.com/juju/juju/environs/imagemetadata"
	"github.com/juju/juju/environs/simplestreams"
	"github.com/juju/juju/state/cloudimagemetadata"
)

// ImageMetadataInterface is an interface for manipulating images metadata.
type ImageMetadataInterface interface {

	// SaveMetadata persists collection of given images metadata.
	SaveMetadata([]cloudimagemetadata.Metadata) error

	// ModelConfig retrieves configuration for a current model.
	ModelConfig() (*config.Config, error)
}


// Save stores given cloud image metadata using given persistence interface.
func Save(st ImageMetadataInterface, metadata params.MetadataSaveParams) ([]params.ErrorResult, error) {
	all := make([]params.ErrorResult, len(metadata.Metadata))
	if len(metadata.Metadata) == 0 {
		return nil, nil
	}
	// TODO(jam): 2017-10-17 Pass in only what we need rather than all ModelConfig
	modelCfg, err := st.ModelConfig()
	if err != nil {
		return nil, errors.Annotatef(err, "getting model config")
	}
	for i, one := range metadata.Metadata {
		md := ParseMetadataListFromParams(one, modelCfg)
		err := st.SaveMetadata(md)
		all[i] = params.ErrorResult{Error: common.ServerError(err)}
	}
	return all, nil
}

// ParseMetadataListFromParams translates params.CloudImageMetadataList
// into a collection of cloudimagemetadata.Metadata.
func ParseMetadataListFromParams(p params.CloudImageMetadataList, cfg *config.Config) []cloudimagemetadata.Metadata {
	results := make([]cloudimagemetadata.Metadata, len(p.Metadata))
	for i, metadata := range p.Metadata {
		results[i] = cloudimagemetadata.Metadata{
			MetadataAttributes: cloudimagemetadata.MetadataAttributes{
				Stream:          metadata.Stream,
				Region:          metadata.Region,
				Version:         metadata.Version,
				Series:          metadata.Series,
				Arch:            metadata.Arch,
				VirtType:        metadata.VirtType,
				RootStorageType: metadata.RootStorageType,
				RootStorageSize: metadata.RootStorageSize,
				Source:          metadata.Source,
			},
			Priority: metadata.Priority,
			ImageId:  metadata.ImageId,
		}
		// TODO (anastasiamac 2016-08-24) This is a band-aid solution.
		// Once correct value is read from simplestreams, this needs to go.
		// Bug# 1616295
		if results[i].Stream == "" {
			results[i].Stream = cfg.ImageStream()
		}
	}
	return results
}

func NewUpdaterFromPublished(newEnviron func() (environs.Environ, error), logger loggo.Logger, metadata ImageMetadataInterface) (*UpdaterFromPublished){
	return &UpdaterFromPublished{
		newEnviron: newEnviron,
		logger: logger,
		metadata: metadata,
	}
}

type UpdaterFromPublished struct {
	newEnviron func() (environs.Environ, error)
	logger loggo.Logger
	metadata ImageMetadataInterface
}

func (updater *UpdaterFromPublished) Update() error {
	env, err := updater.newEnviron()
	if err != nil {
		return errors.Annotatef(err, "getting environ")
	}

	sources, err := environs.ImageMetadataSources(env)
	if err != nil {
		return errors.Annotatef(err, "getting cloud specific image metadata sources")
	}

	cons := envmetadata.NewImageConstraint(simplestreams.LookupParams{})
	if inst, ok := env.(simplestreams.HasRegion); !ok {
		// Published image metadata for some providers are in simple streams.
		// Providers that do not rely on simplestreams, don't need to do anything here.
		return nil
	} else {
		// If we can determine current region,
		// we want only metadata specific to this region.
		cloud, err := inst.Region()
		if err != nil {
			return errors.Annotatef(err, "getting cloud specific region information")
		}
		cons.CloudSpec = cloud
	}

	// We want all relevant metadata from all data sources.
	for _, source := range sources {
		updater.logger.Debugf("looking in data source %v", source.Description())
		metadata, info, err := envmetadata.Fetch([]simplestreams.DataSource{source}, cons)
		if err != nil {
			// Do not stop looking in other data sources if there is an issue here.
			updater.logger.Errorf("encountered %v while getting published images metadata from %v", err, source.Description())
			continue
		}
		err = updater.saveAll(info, source.Priority(), metadata)
		if err != nil {
			// Do not stop looking in other data sources if there is an issue here.
			updater.logger.Errorf("encountered %v while saving published images metadata from %v", err, source.Description())
		}
	}
	return nil
}

func (updater *UpdaterFromPublished) saveAll(info *simplestreams.ResolveInfo, priority int, published []*envmetadata.ImageMetadata) error {
	metadata, parseErrs := convertToParams(info, priority, published)

	// Store converted metadata.
	// Note that whether the metadata actually needs
	// to be stored will be determined within this call.
	errs, err := Save(updater.metadata, metadata)
	if err != nil {
		return errors.Annotatef(err, "saving published images metadata")
	}

	return processErrors(append(errs, parseErrs...))
}

// convertToParams converts model-specific images metadata to structured metadata format.
var convertToParams = func(info *simplestreams.ResolveInfo, priority int, published []*envmetadata.ImageMetadata) (params.MetadataSaveParams, []params.ErrorResult) {
	metadata := []params.CloudImageMetadataList{{}}
	errs := []params.ErrorResult{}
	for _, p := range published {
		s, err := series.VersionSeries(p.Version)
		if err != nil {
			errs = append(errs, params.ErrorResult{Error: common.ServerError(err)})
			continue
		}

		m := params.CloudImageMetadata{
			Source:          info.Source,
			ImageId:         p.Id,
			Stream:          p.Stream,
			Region:          p.RegionName,
			Arch:            p.Arch,
			VirtType:        p.VirtType,
			RootStorageType: p.Storage,
			Series:          s,
			Priority:        priority,
		}

		metadata[0].Metadata = append(metadata[0].Metadata, m)
	}
	return params.MetadataSaveParams{Metadata: metadata}, errs
}

func processErrors(errs []params.ErrorResult) error {
	msgs := []string{}
	for _, e := range errs {
		if e.Error != nil && e.Error.Message != "" {
			msgs = append(msgs, e.Error.Message)
		}
	}
	if len(msgs) != 0 {
		return errors.Errorf("saving some image metadata:\n%v", strings.Join(msgs, "\n"))
	}
	return nil
}
