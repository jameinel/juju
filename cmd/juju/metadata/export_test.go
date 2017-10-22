// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package metadata

import (
	"github.com/juju/cmd"

	"github.com/juju/juju/cmd/modelcmd"
)
var (
	FormatMetadataTabular = formatMetadataTabular
)

func OverrideAPI(c cmd.Command, api MetadataListAPI) {
	inner := modelcmd.InnerCommand(c)
	imageCmd, ok := inner.(*listImageMetadataCommand)
	if ok {
		imageCmd.api = api
	} else {
		panic("did not pass a listImageMetadataCommand to OverrideAPI")
	}
}
