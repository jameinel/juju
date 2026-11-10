// Copyright 2025 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package application

import "github.com/juju/juju/domain/sequence"

const (
	// CharmSequenceNamespace is the namespace for charm sequences.
	CharmSequenceNamespace = sequence.StaticNamespace("charm")

	// CharmInsertionOrderSequenceNamespace is the namespace for the order in
	// which charms are added to the model. Charm names cannot contain
	// underscores, so it cannot clash with a charm revision namespace.
	CharmInsertionOrderSequenceNamespace = sequence.StaticNamespace("charm_insertion_order")

	// ApplicationSequenceNamespace is the namespace for application unit
	// sequences.
	ApplicationSequenceNamespace = sequence.StaticNamespace("application")

	// ApplicationCharmSequenceNamespace is the namespace for application charm
	// sequences.
	ApplicationCharmSequenceNamespace = sequence.StaticNamespace("application_charm")
)
