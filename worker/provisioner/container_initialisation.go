// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provisioner

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/juju/errors"
	"github.com/juju/utils"
	"github.com/juju/utils/exec"
	"github.com/juju/utils/fslock"

	"github.com/juju/juju/agent"
	apiprovisioner "github.com/juju/juju/api/provisioner"
	"github.com/juju/juju/api/watcher"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/container"
	"github.com/juju/juju/container/kvm"
	"github.com/juju/juju/container/lxc"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/service"
	"github.com/juju/juju/service/common"
	"github.com/juju/juju/state"
	"github.com/juju/juju/version"
	"github.com/juju/juju/worker"
)

// ContainerSetup is a StringsWatchHandler that is notified when containers
// are created on the given machine. It will set up the machine to be able
// to create containers and start a suitable provisioner.
type ContainerSetup struct {
	runner                worker.Runner
	supportedContainers   []instance.ContainerType
	imageURLGetter        container.ImageURLGetter
	provisioner           *apiprovisioner.State
	machine               *apiprovisioner.Machine
	config                agent.Config
	initLock              *fslock.Lock
	addressableContainers bool

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
func NewContainerSetupHandler(params ContainerSetupParams) worker.StringsWatchHandler {
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
func (cs *ContainerSetup) Handle(containerIds []string) (resultError error) {
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

	// In order to guarantee stable statically assigned IP
	// addresses for LXC containers, we need to stop and disable
	// the dnsmasq service the LXC package runs on the 10.0.3.0/24
	// range. If that is not done, statically assigned IPs for
	// containers will be overridden with 10.0.3.x addresses
	// periodically as DHCP leases expire.
	if cs.addressableContainers && containerType == instance.LXC {
		if err := disableLXCDNSMasq(); err != nil {
			return errors.Trace(err)
		}
		logger.Infof("disabled dnsmasq for LXC containers")
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

	// Enable IP forwarding and ARP proxying if needed.
	if ipfwd := managerConfig.PopValue(container.ConfigIPForwarding); ipfwd != "" {
		if err := setIPAndARPForwarding(true); err != nil {
			return nil, nil, nil, errors.Trace(err)
		}
		cs.addressableContainers = true
		logger.Infof("enabled IP forwarding and ARP proxying for containers")
	}

	switch containerType {
	case instance.LXC:
		series, err := cs.machine.Series()
		if err != nil {
			return nil, nil, nil, err
		}

		initialiser = lxc.NewContainerInitialiser(series)
		broker, err = NewLxcBroker(cs.provisioner, cs.config, managerConfig, cs.imageURLGetter)
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
		broker, err = NewKvmBroker(cs.provisioner, cs.config, managerConfig)
		if err != nil {
			logger.Errorf("failed to create new kvm broker")
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
	if params.IsCodeNotImplemented(err) {
		// We currently don't support upgrading;
		// revert to the old configuration.
		managerConfigResult.ManagerConfig = container.ManagerConfig{container.ConfigName: container.DefaultNamespace}
	}
	if err != nil {
		return nil, err
	}
	// If a namespace is specified, that should instead be used as the config name.
	if namespace := agentConfig.Value(agent.Namespace); namespace != "" {
		managerConfigResult.ManagerConfig[container.ConfigName] = namespace
	}
	managerConfig := container.ManagerConfig(managerConfigResult.ManagerConfig)

	return managerConfig, nil
}

// Override for testing.
var (
	StartProvisioner = startProvisionerWorker

	sysctlConfig = "/etc/sysctl.conf"
)

const (
	ipForwardSysctlKey = "net.ipv4.ip_forward"
	arpProxySysctlKey  = "net.ipv4.conf.all.proxy_arp"
)

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
		return NewContainerProvisioner(containerType, provisioner, config, broker, toolsFinder), nil
	})
}

// setIPAndARPForwarding enables or disables IP and ARP forwarding on
// the machine. This is needed when the machine needs to host
// addressable containers.
var setIPAndARPForwarding = func(enabled bool) error {
	val := "0"
	if enabled {
		val = "1"
	}

	runCmds := func(keyAndVal string) (err error) {

		defer errors.DeferredAnnotatef(&err, "cannot set %s", keyAndVal)

		commands := []string{
			// Change it immediately:
			fmt.Sprintf("sysctl -w %s", keyAndVal),

			// Change it also on next boot:
			fmt.Sprintf("echo '%s' | tee -a %s", keyAndVal, sysctlConfig),
		}
		for _, cmd := range commands {
			result, err := exec.RunCommands(exec.RunParams{Commands: cmd})
			if err != nil {
				return errors.Trace(err)
			}
			logger.Debugf(
				"command %q returned: code: %d, stdout: %q, stderr: %q",
				cmd, result.Code, string(result.Stdout), string(result.Stderr),
			)
			if result.Code != 0 {
				return errors.Errorf("unexpected exit code %d", result.Code)
			}
		}
		return nil
	}

	err := runCmds(fmt.Sprintf("%s=%s", ipForwardSysctlKey, val))
	if err != nil {
		return err
	}
	return runCmds(fmt.Sprintf("%s=%s", arpProxySysctlKey, val))
}

// disableLXCDNSMasq discovers the location of the init job "lxc-net"
// installed by the LXC package and modifies the config to disable
// launching dnsmasq for the default DHCP range for LXC containers
// (10.0.3.0/24). It also stops the lxc-net service if running and
// restarts it after disabling dnsmasq.
func disableLXCDNSMasq() (err error) {
	logger.Tracef("trying to discover lxc-net init job")
	var svc service.Service
	svc, err = service.DiscoverService("lxc-net", common.Conf{})
	if err != nil {
		return errors.Annotate(err, "cannot find lxc-net init job")
	}
	logger.Tracef("found service %q (running: %v, installed: %v)", svc.Name(), svc.Running(), svc.Installed())

	defer func() {
		// If we modified the config successfully, make sure we
		// restart the service.
		if err != nil {
			return
		}
		// Restart the service for the new config to take effect. For
		// various reasons the init system may take a short time to
		// realise that the service has been updated then stopped, so
		// retry a few times until Start() succeeds.
		var errStart error
		for attempt := service.InstallStartRetryAttempts.Start(); attempt.Next(); {
			// Stop it first - it should be running, so if not - ignore
			// the error.
			if err0 := svc.Stop(); err0 != nil {
				logger.Tracef("cannot stop service %q: %v (ignoring)", svc.Name(), err0)
			}
			if errStart = svc.Start(); errStart == nil {
				break
			} else {
				logger.Tracef("trying to start service %q: %v (retrying)", svc.Name(), errStart)
			}
		}
		if errStart != nil {
			err = errors.Annotatef(errStart,
				"cannot restart service %q after disabling dnsmasq", svc.Name(),
			)
			return
		}
		logger.Tracef("service %q restarted", svc.Name())
	}()

	// Find out the config file location.
	initName, ok := service.VersionInitSystem(version.Current)
	if !ok {
		return errors.NotFoundf("init system on local host")
	}
	if initName != service.InitSystemUpstart {
		// TODO(dimitern): Handle systemd here as well in a follow-up.
		logger.Tracef("init system is %q, not changing lxc-net service", initName)
		return nil
	}
	ext := ".conf" // for upstart
	confFile := filepath.Join(svc.Conf().InitDir, svc.Name()+ext)

	// Scan the config file to discover the line starting dnsmasq.
	var data []byte
	data, err = ioutil.ReadFile(confFile)
	if err != nil {
		return errors.Annotatef(err, "cannot read config from %q", confFile)
	}
	input := bytes.NewBuffer(data)
	var output bytes.Buffer
	scanner := bufio.NewScanner(input)
	changed := false
	for scanner.Scan() {
		originalLine := scanner.Text()
		line := strings.TrimSpace(originalLine)

		if changed {
			// We already changed the line we needed, just ignore the
			// rest.
			output.WriteString(originalLine + "\n")
			continue
		}

		// Skip all lines except those which contain the expected
		// command and arguments. Skipped lines go unchanged into
		// output.
		if !strings.HasPrefix(line, "dnsmasq ") {
			output.WriteString(originalLine + "\n")
			continue
		}
		if !strings.Contains(line, "--bind-interfaces") {
			output.WriteString(originalLine + "\n")
			continue
		}
		if !strings.Contains(line, "--dhcp-range") {
			output.WriteString(originalLine + "\n")
			continue
		}
		if !strings.Contains(line, "LXC_DHCP_RANGE") {
			output.WriteString(originalLine + "\n")
			continue
		}
		// We found it, replace "dnsmasq" in the beginning with
		// "true", which effectively disables the command in the least
		// intrusive way.
		cmd := strings.Replace(originalLine, "dnsmasq ", "true ", 1)
		output.WriteString("\t# dnsmasq for $LXC_DHCP_RANGE disabled by Juju.\n")
		output.WriteString(cmd + "\n")
		changed = true
	}
	if err = scanner.Err(); err != nil {
		return errors.Annotatef(err, "cannot process config from %q", confFile)
	}

	if !changed {
		logger.Tracef("dnsmasq not found and not disabled in %q", confFile)
		return nil
	}

	// Reset the original file and overwrite it atomically.
	if err = utils.AtomicWriteFile(confFile, output.Bytes(), 0644); err != nil {
		return errors.Annotatef(err, "cannot write new config %q for service %q", confFile, svc.Name())
	}
	logger.Tracef("successfully disabled dnsmasq in %q", confFile)
	return nil
}
