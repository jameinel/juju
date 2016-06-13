// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provisioner

import (
	"bufio"
	"os"
	"strings"

	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/utils/arch"
	"github.com/juju/version"
	"gopkg.in/juju/names.v2"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/cloudconfig/instancecfg"
	"github.com/juju/juju/container"
	"github.com/juju/juju/container/lxc"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/network"
	"github.com/juju/juju/tools"
)

var lxcLogger = loggo.GetLogger("juju.provisioner.lxc")

type APICalls interface {
	ContainerConfig() (params.ContainerConfig, error)
	PrepareContainerInterfaceInfo(names.MachineTag) ([]network.InterfaceInfo, error)
	GetContainerInterfaceInfo(names.MachineTag) ([]network.InterfaceInfo, error)
	ReleaseContainerAddresses(names.MachineTag) error
}

// Override for testing.
var NewLxcBroker = newLxcBroker

func newLxcBroker(api APICalls,
	agentConfig agent.Config,
	managerConfig container.ManagerConfig,
	imageURLGetter container.ImageURLGetter,
	defaultMTU int,
) (environs.InstanceBroker, error) {
	manager, err := lxc.NewContainerManager(managerConfig, imageURLGetter)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return &lxcBroker{
		manager:     manager,
		api:         api,
		agentConfig: agentConfig,
		defaultMTU:  defaultMTU,
	}, nil
}

type lxcBroker struct {
	manager     container.Manager
	api         APICalls
	agentConfig agent.Config
	defaultMTU  int
}

// StartInstance is specified in the Broker interface.
func (broker *lxcBroker) StartInstance(args environs.StartInstanceParams) (*environs.StartInstanceResult, error) {
	// TODO: refactor common code out of the container brokers.
	machineId := args.InstanceConfig.MachineId
	lxcLogger.Infof("starting lxc container for machineId: %s", machineId)

	// Default to using the host network until we can configure.
	bridgeDevice := broker.agentConfig.Value(agent.LxcBridge)
	if bridgeDevice == "" {
		bridgeDevice = container.DefaultLxcBridge
	}

	config, err := broker.api.ContainerConfig()
	if err != nil {
		lxcLogger.Errorf("failed to get container config: %v", err)
		return nil, err
	}

	preparedInfo, err := prepareOrGetContainerInterfaceInfo(
		broker.api,
		machineId,
		bridgeDevice,
		true, // allocate if possible, do not maintain existing.
		args.NetworkInfo,
		lxcLogger,
	)
	if err != nil {
		// It's not fatal (yet) if we couldn't pre-allocate addresses for the
		// container.
		logger.Warningf("failed to prepare container %q network config: %v", machineId, err)
	} else {
		args.NetworkInfo = preparedInfo

	}
	network := container.BridgeNetworkConfig(bridgeDevice, broker.defaultMTU, args.NetworkInfo)

	// The provisioner worker will provide all tools it knows about
	// (after applying explicitly specified constraints), which may
	// include tools for architectures other than the host's. We
	// must constrain to the host's architecture for LXC.
	archTools, err := matchHostArchTools(args.Tools)
	if err != nil {
		return nil, errors.Trace(err)
	}

	series := archTools.OneSeries()
	args.InstanceConfig.MachineContainerType = instance.LXC
	if err := args.InstanceConfig.SetTools(archTools); err != nil {
		return nil, errors.Trace(err)
	}

	storageConfig := &container.StorageConfig{
		AllowMount: config.AllowLXCLoopMounts,
	}

	if err := instancecfg.PopulateInstanceConfig(
		args.InstanceConfig,
		config.ProviderType,
		config.AuthorizedKeys,
		config.SSLHostnameVerification,
		config.Proxy,
		config.AptProxy,
		config.AptMirror,
		config.EnableOSRefreshUpdate,
		config.EnableOSUpgrade,
	); err != nil {
		lxcLogger.Errorf("failed to populate machine config: %v", err)
		return nil, err
	}

	inst, hardware, err := broker.manager.CreateContainer(
		args.InstanceConfig, args.Constraints,
		series, network, storageConfig, args.StatusCallback,
	)
	if err != nil {
		lxcLogger.Errorf("failed to start container: %v", err)
		return nil, err
	}
	lxcLogger.Infof("started lxc container for machineId: %s, %s, %s", machineId, inst.Id(), hardware.String())
	return &environs.StartInstanceResult{
		Instance:    inst,
		Hardware:    hardware,
		NetworkInfo: network.Interfaces,
	}, nil
}

// MaintainInstance ensures the container's host has the required iptables and
// routing rules to make the container visible to both the host and other
// machines on the same subnet.
func (broker *lxcBroker) MaintainInstance(args environs.StartInstanceParams) error {
	machineID := args.InstanceConfig.MachineId

	// Default to using the host network until we can configure.
	bridgeDevice := broker.agentConfig.Value(agent.LxcBridge)
	if bridgeDevice == "" {
		bridgeDevice = container.DefaultLxcBridge
	}

	// There's no InterfaceInfo we expect to get below.
	_, err := prepareOrGetContainerInterfaceInfo(
		broker.api,
		machineID,
		bridgeDevice,
		false, // maintain, do not allocate.
		args.NetworkInfo,
		lxcLogger,
	)
	return err
}

// StopInstances shuts down the given instances.
func (broker *lxcBroker) StopInstances(ids ...instance.Id) error {
	// TODO: potentially parallelise.
	for _, id := range ids {
		lxcLogger.Infof("stopping lxc container for instance: %s", id)
		if err := broker.manager.DestroyContainer(id); err != nil {
			lxcLogger.Errorf("container did not stop: %v", err)
			return err
		}
		releaseContainerAddresses(broker.api, id, broker.manager.Namespace(), lxcLogger)
	}
	return nil
}

// AllInstances only returns running containers.
func (broker *lxcBroker) AllInstances() (result []instance.Instance, err error) {
	return broker.manager.ListContainers()
}

type hostArchToolsFinder struct {
	f ToolsFinder
}

// FindTools is defined on the ToolsFinder interface.
func (h hostArchToolsFinder) FindTools(v version.Number, series, _ string) (tools.List, error) {
	// Override the arch constraint with the arch of the host.
	return h.f.FindTools(v, series, arch.HostArch())
}

// resolvConf is the full path to the resolv.conf file on the local
// system. Defined here so it can be overriden for testing.
var resolvConf = "/etc/resolv.conf"

// localDNSServers parses the /etc/resolv.conf file (if available) and
// extracts all nameservers addresses, and the default search domain
// and returns them.
func localDNSServers() ([]network.Address, string, error) {
	file, err := os.Open(resolvConf)
	if os.IsNotExist(err) {
		return nil, "", nil
	} else if err != nil {
		return nil, "", errors.Annotatef(err, "cannot open %q", resolvConf)
	}
	defer file.Close()

	var addresses []network.Address
	var searchDomain string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			// Skip comments.
			continue
		}
		if strings.HasPrefix(line, "nameserver") {
			address := strings.TrimPrefix(line, "nameserver")
			// Drop comments after the address, if any.
			if strings.Contains(address, "#") {
				address = address[:strings.Index(address, "#")]
			}
			address = strings.TrimSpace(address)
			addresses = append(addresses, network.NewAddress(address))
		}
		if strings.HasPrefix(line, "search") {
			searchDomain = strings.TrimPrefix(line, "search")
			// Drop comments after the domain, if any.
			if strings.Contains(searchDomain, "#") {
				searchDomain = searchDomain[:strings.Index(searchDomain, "#")]
			}
			searchDomain = strings.TrimSpace(searchDomain)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, "", errors.Annotatef(err, "cannot read DNS servers from %q", resolvConf)
	}
	return addresses, searchDomain, nil
}

func prepareOrGetContainerInterfaceInfo(
	api APICalls,
	machineID string,
	bridgeDevice string,
	allocateOrMaintain bool,
	startingNetworkInfo []network.InterfaceInfo,
	log loggo.Logger,
) ([]network.InterfaceInfo, error) {
	maintain := !allocateOrMaintain

	if maintain {
		log.Debugf("not running maintenance for machine %q", machineID)
		return nil, nil
	}

	log.Debugf("using multi-bridge networking for container %q", machineID)

	// In case we're running on MAAS 1.8+ with devices support, we'll still
	// call PrepareContainerInterfaceInfo(), but we'll ignore a NotSupported
	// error if we get it (which means we're not using MAAS 1.8+).
	containerTag := names.NewMachineTag(machineID)
	preparedInfo, err := api.PrepareContainerInterfaceInfo(containerTag)
	if err != nil {
		return nil, errors.Trace(err)
	}
	log.Tracef("PrepareContainerInterfaceInfo returned %+v", preparedInfo)

	dnsServersFound := false
	for _, info := range preparedInfo {
		if len(info.DNSServers) > 0 {
			dnsServersFound = true
			break
		}
	}
	if !dnsServersFound {
		logger.Warningf("no DNS settings found, discovering the host settings")
		dnsServers, searchDomain, err := localDNSServers()
		if err != nil {
			return nil, errors.Trace(err)
		}

		// Since the result is sorted, the first entry is the primary NIC.
		preparedInfo[0].DNSServers = dnsServers
		preparedInfo[0].DNSSearchDomains = []string{searchDomain}
		logger.Debugf(
			"setting DNS servers %+v and domains %+v on container interface %q",
			preparedInfo[0].DNSServers, preparedInfo[0].DNSSearchDomains, preparedInfo[0].InterfaceName,
		)
	}

	return preparedInfo, nil
}

func releaseContainerAddresses(
	api APICalls,
	instanceID instance.Id,
	namespace instance.Namespace,
	log loggo.Logger,
) {
	containerTag, err := namespace.MachineTag(string(instanceID))
	if err != nil {
		// Not a reason to cause StopInstances to fail though..
		log.Warningf("unexpected container tag %q: %v", instanceID, err)
		return
	}
	err = api.ReleaseContainerAddresses(containerTag)
	switch {
	case err == nil:
		log.Infof("released all addresses for container %q", containerTag.Id())
	case errors.IsNotSupported(err):
		log.Warningf("not releasing all addresses for container %q: %v", containerTag.Id(), err)
	default:
		log.Warningf(
			"unexpected error trying to release container %q addreses: %v",
			containerTag.Id(), err,
		)
	}
}

// matchHostArchTools filters the given list of tools to the host architecture.
func matchHostArchTools(allTools tools.List) (tools.List, error) {
	arch := arch.HostArch()
	archTools, err := allTools.Match(tools.Filter{Arch: arch})
	if err == tools.ErrNoMatches {
		return nil, errors.Errorf(
			"need tools for arch %s, only found %s",
			arch, allTools.Arches(),
		)
	} else if err != nil {
		return nil, errors.Trace(err)
	}
	return archTools, nil
}
