// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provisioner

import (
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/juju/errors"
	"github.com/juju/utils/fslock"

	"github.com/juju/juju/agent"
	apiprovisioner "github.com/juju/juju/api/provisioner"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/container"
	"github.com/juju/juju/container/kvm"
	"github.com/juju/juju/container/lxc"
	"github.com/juju/juju/container/lxd"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/state"
	"github.com/juju/juju/watcher"
	"github.com/juju/juju/worker"
)

// ContainerSetup is a StringsWatchHandler that is notified when containers
// are created on the given machine. It will set up the machine to be able
// to create containers and start a suitable provisioner.
type ContainerSetup struct {
	runner              worker.Runner
	supportedContainers []instance.ContainerType
	imageURLGetter      container.ImageURLGetter
	provisioner         *apiprovisioner.State
	machine             *apiprovisioner.Machine
	config              agent.Config
	initLock            *fslock.Lock
	lxcDefaultMTU       int

	// Save the workerName so the worker thread can be stopped.
	workerName string
	// setupDone[containerType] is non zero if the container setup has been invoked
	// for that container type.
	setupDone map[instance.ContainerType]*int32
	// The number of provisioners started. Once all necessary provisioners have
	// been started, the container watcher can be stopped.
	numberProvisioners int32
}

// ContainerSetupParams are used to initialise a container setup handler.
type ContainerSetupParams struct {
	Runner              worker.Runner
	WorkerName          string
	SupportedContainers []instance.ContainerType
	ImageURLGetter      container.ImageURLGetter
	Machine             *apiprovisioner.Machine
	Provisioner         *apiprovisioner.State
	Config              agent.Config
	InitLock            *fslock.Lock
}

// NewContainerSetupHandler returns a StringsWatchHandler which is notified when
// containers are created on the given machine.
func NewContainerSetupHandler(params ContainerSetupParams) watcher.StringsHandler {
	return &ContainerSetup{
		runner:              params.Runner,
		imageURLGetter:      params.ImageURLGetter,
		machine:             params.Machine,
		supportedContainers: params.SupportedContainers,
		provisioner:         params.Provisioner,
		config:              params.Config,
		workerName:          params.WorkerName,
		initLock:            params.InitLock,
	}
}

// SetUp is defined on the StringsWatchHandler interface.
func (cs *ContainerSetup) SetUp() (watcher watcher.StringsWatcher, err error) {
	// Set up the semaphores for each container type.
	cs.setupDone = make(map[instance.ContainerType]*int32, len(instance.ContainerTypes))
	for _, containerType := range instance.ContainerTypes {
		zero := int32(0)
		cs.setupDone[containerType] = &zero
	}
	// Listen to all container lifecycle events on our machine.
	if watcher, err = cs.machine.WatchAllContainers(); err != nil {
		return nil, err
	}
	return watcher, nil
}

// Handle is called whenever containers change on the machine being watched.
// Machines start out with no containers so the first time Handle is called,
// it will be because a container has been added.
func (cs *ContainerSetup) Handle(_ <-chan struct{}, containerIds []string) (resultError error) {
	// Consume the initial watcher event.
	if len(containerIds) == 0 {
		return nil
	}

	logger.Infof("initial container setup with ids: %v", containerIds)
	for _, id := range containerIds {
		containerType := state.ContainerTypeFromId(id)
		// If this container type has been dealt with, do nothing.
		if atomic.LoadInt32(cs.setupDone[containerType]) != 0 {
			continue
		}
		if err := cs.initialiseAndStartProvisioner(containerType); err != nil {
			logger.Errorf("starting container provisioner for %v: %v", containerType, err)
			// Just because dealing with one type of container fails, we won't exit the entire
			// function because we still want to try and start other container types. So we
			// take note of and return the first such error.
			if resultError == nil {
				resultError = err
			}
		}
	}
	return resultError
}

func (cs *ContainerSetup) initialiseAndStartProvisioner(containerType instance.ContainerType) (resultError error) {
	// Flag that this container type has been handled.
	atomic.StoreInt32(cs.setupDone[containerType], 1)

	defer func() {
		if resultError != nil {
			logger.Warningf("not stopping machine agent container watcher due to error: %v", resultError)
			return
		}
		if atomic.AddInt32(&cs.numberProvisioners, 1) == int32(len(cs.supportedContainers)) {
			// We only care about the initial container creation.
			// This worker has done its job so stop it.
			// We do not expect there will be an error, and there's not much we can do anyway.
			if err := cs.runner.StopWorker(cs.workerName); err != nil {
				logger.Warningf("stopping machine agent container watcher: %v", err)
			}
		}
	}()

	logger.Debugf("setup and start provisioner for %s containers", containerType)
	toolsFinder := getToolsFinder(cs.provisioner)
	initialiser, broker, toolsFinder, err := cs.getContainerArtifacts(containerType, toolsFinder)
	if err != nil {
		return errors.Annotate(err, "initialising container infrastructure on host machine")
	}
	if err := cs.runInitialiser(containerType, initialiser); err != nil {
		return errors.Annotate(err, "setting up container dependencies on host machine")
	}
	return StartProvisioner(cs.runner, containerType, cs.provisioner, cs.config, broker, toolsFinder)
}

// runInitialiser runs the container initialiser with the initialisation hook held.
func (cs *ContainerSetup) runInitialiser(containerType instance.ContainerType, initialiser container.Initialiser) error {
	logger.Debugf("running initialiser for %s containers", containerType)
	if err := cs.initLock.Lock(fmt.Sprintf("initialise-%s", containerType)); err != nil {
		return errors.Annotate(err, "failed to acquire initialization lock")
	}
	defer cs.initLock.Unlock()

	if err := initialiser.Initialise(); err != nil {
		return errors.Trace(err)
	}

	return nil
}

// TearDown is defined on the StringsWatchHandler interface.
func (cs *ContainerSetup) TearDown() error {
	// Nothing to do here.
	return nil
}

// getContainerArtifacts returns type-specific interfaces for
// managing containers.
//
// The ToolsFinder passed in may be replaced or wrapped to
// enforce container-specific constraints.
func (cs *ContainerSetup) getContainerArtifacts(
	containerType instance.ContainerType, toolsFinder ToolsFinder,
) (
	container.Initialiser,
	environs.InstanceBroker,
	ToolsFinder,
	error,
) {
	var initialiser container.Initialiser
	var broker environs.InstanceBroker

	managerConfig, err := containerManagerConfig(containerType, cs.provisioner, cs.config)
	if err != nil {
		return nil, nil, nil, err
	}

	// Override default MTU for LXC NICs, if needed.
	if mtu := managerConfig.PopValue(container.ConfigLXCDefaultMTU); mtu != "" {
		value, err := strconv.Atoi(mtu)
		if err != nil {
			return nil, nil, nil, errors.Trace(err)
		}
		logger.Infof("setting MTU to %v for all LXC containers' interfaces", value)
		cs.lxcDefaultMTU = value
	}

	switch containerType {
	case instance.LXC:
		series, err := cs.machine.Series()
		if err != nil {
			return nil, nil, nil, err
		}

		initialiser = lxc.NewContainerInitialiser(series)
		broker, err = NewLxcBroker(
			cs.provisioner,
			cs.config,
			managerConfig,
			cs.imageURLGetter,
			cs.lxcDefaultMTU,
		)
		if err != nil {
			return nil, nil, nil, err
		}

		// LXC containers must have the same architecture as the host.
		// We should call through to the finder since the version of
		// tools running on the host may not match, but we want to
		// override the arch constraint with the arch of the host.
		toolsFinder = hostArchToolsFinder{toolsFinder}

	case instance.KVM:
		initialiser = kvm.NewContainerInitialiser()
		broker, err = NewKvmBroker(
			cs.provisioner,
			cs.config,
			managerConfig,
		)
		if err != nil {
			logger.Errorf("failed to create new kvm broker")
			return nil, nil, nil, err
		}
	case instance.LXD:
		series, err := cs.machine.Series()
		if err != nil {
			return nil, nil, nil, err
		}

		initialiser = lxd.NewContainerInitialiser(series)
		manager, err := lxd.NewContainerManager(managerConfig)
		if err != nil {
			return nil, nil, nil, err
		}
		broker, err = NewLxdBroker(
			cs.provisioner,
			manager,
			cs.config,
		)
		if err != nil {
			logger.Errorf("failed to create new lxd broker")
			return nil, nil, nil, err
		}
	default:
		return nil, nil, nil, fmt.Errorf("unknown container type: %v", containerType)
	}

	return initialiser, broker, toolsFinder, nil
}

func containerManagerConfig(
	containerType instance.ContainerType,
	provisioner *apiprovisioner.State,
	agentConfig agent.Config,
) (container.ManagerConfig, error) {
	// Ask the provisioner for the container manager configuration.
	managerConfigResult, err := provisioner.ContainerManagerConfig(
		params.ContainerManagerConfigParams{Type: containerType},
	)
	if err != nil {
		return nil, errors.Trace(err)
	}
	managerConfig := container.ManagerConfig(managerConfigResult.ManagerConfig)
	return managerConfig, nil
}

// Override for testing.
var StartProvisioner = startProvisionerWorker

// startProvisionerWorker kicks off a provisioner task responsible for creating containers
// of the specified type on the machine.
func startProvisionerWorker(
	runner worker.Runner,
	containerType instance.ContainerType,
	provisioner *apiprovisioner.State,
	config agent.Config,
	broker environs.InstanceBroker,
	toolsFinder ToolsFinder,
) error {

	workerName := fmt.Sprintf("%s-provisioner", containerType)
	// The provisioner task is created after a container record has
	// already been added to the machine. It will see that the
	// container does not have an instance yet and create one.
	return runner.StartWorker(workerName, func() (worker.Worker, error) {
		w, err := NewContainerProvisioner(containerType, provisioner, config, broker, toolsFinder)
		if err != nil {
			return nil, errors.Trace(err)
		}
		return w, nil
	})
}
