// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package resource

import (
	coreunit "github.com/juju/juju/core/unit"
	"github.com/juju/juju/domain/deployment/charm/resource"
)

// ApplicationResources contains resources for an application and its units.
// Slice order is not guaranteed.
type ApplicationResources struct {
	// Resources are the current version of the resource for the application that
	// resource-get will retrieve.
	Resources []Resource

	// RepositoryResources provides resource information from the charm store
	// as of the last poll. Match entries to Resources by resource name, not
	// slice index. The slices may differ in length, order and resource names.
	RepositoryResources []resource.Resource

	// UnitResources reports the currently-in-use version of resources for each
	// unit.
	UnitResources []UnitResources
}

// Updates returns the list of charm store resources corresponding to
// the application's resources that are out of date. If there is a charm
// store resource with a different revision than the one used into the
// application, it will be returned.
// Any charm store resources with the same revision number from the
// corresponding application resources will be filtered out.
func (sr ApplicationResources) Updates() ([]resource.Resource, error) {
	storeResources := map[string]resource.Resource{}
	for _, res := range sr.RepositoryResources {
		storeResources[res.Name] = res
	}

	var updates []resource.Resource
	for _, res := range sr.Resources {
		if res.Origin != resource.OriginStore {
			continue
		}
		csRes, ok := storeResources[res.Name]
		// If the revision is the same then all the other info must be.
		if !ok || res.Revision == csRes.Revision {
			continue
		}
		updates = append(updates, csRes)
	}
	return updates, nil
}

// UnitResources contains the list of resources used by a unit.
type UnitResources struct {
	// Name is the name of the unit.
	Name coreunit.Name

	// Resources are the resource versions currently in use by this unit.
	Resources []Resource
}
