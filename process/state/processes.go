// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/names"

	"github.com/juju/juju/process"
	"github.com/juju/juju/process/persistence"
)

var logger = loggo.GetLogger("juju.process.state")

// TODO(ericsnow) Add names.ProcessTag and use it here?

// TODO(ericsnow) We need a worker to clean up dying procs.

// The persistence methods needed for workload processes in state.
type processesPersistence interface {
	Insert(info process.Info) (bool, error)
	SetStatus(id string, status process.PluginStatus) (bool, error)
	List(ids ...string) ([]process.Info, []string, error)
	ListAll() ([]process.Info, error)
	Remove(id string) (bool, error)
}

// UnitProcesses provides the functionality related to a unit's
// processes, as needed by state.
type UnitProcesses struct {
	// Persist is the persistence layer that will be used.
	Persist processesPersistence
	// Unit identifies the unit associated with the processes.
	Unit names.UnitTag
}

// NewUnitProcesses builds a UnitProcesses for a unit.
func NewUnitProcesses(st persistence.PersistenceBase, unit names.UnitTag) *UnitProcesses {
	persist := persistence.NewPersistence(st, unit)
	return &UnitProcesses{
		Persist: persist,
		Unit:    unit,
	}
}

// Add registers the provided process info in state.
func (ps UnitProcesses) Add(info process.Info) error {
	logger.Debugf("adding %#v", info)
	if err := info.Validate(); err != nil {
		return errors.NewNotValid(err, "bad process info")
	}

	ok, err := ps.Persist.Insert(info)
	if err != nil {
		return errors.Trace(err)
	}
	if !ok {
		return errors.NotValidf("process %s (already in state)", info.ID())
	}

	return nil
}

// SetStatus updates the raw status for the identified process to the
// provided value.
func (ps UnitProcesses) SetStatus(id string, status process.PluginStatus) error {
	logger.Debugf("setting status for %q to %#v", id, status)
	found, err := ps.Persist.SetStatus(id, status)
	if err != nil {
		return errors.Trace(err)
	}
	if !found {
		return errors.NotFoundf(id)
	}
	return nil
}

// List builds the list of process information for the provided process
// IDs. If none are provided then the list contains the info for all
// workload processes associated with the unit. Missing processes
// are ignored.
func (ps UnitProcesses) List(ids ...string) ([]process.Info, error) {
	logger.Debugf("listing %v", ids)
	if len(ids) == 0 {
		results, err := ps.Persist.ListAll()
		if err != nil {
			return nil, errors.Trace(err)
		}
		return results, nil
	}

	results, _, err := ps.Persist.List(ids...)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return results, nil
}

// Remove removes the identified process from state. It does not
// trigger the actual destruction of the process.
func (ps UnitProcesses) Remove(id string) error {
	logger.Debugf("removing %q", id)
	// If the record wasn't found then we're already done.
	_, err := ps.Persist.Remove(id)
	if err != nil {
		return errors.Trace(err)
	}
	return nil
}
