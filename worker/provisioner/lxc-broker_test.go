// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provisioner_test

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"runtime"
	"time"

	"github.com/juju/errors"
	gitjujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils/arch"
	"github.com/juju/utils/series"
	"github.com/juju/utils/set"
	"github.com/juju/version"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/names.v2"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/cloudconfig/instancecfg"
	"github.com/juju/juju/constraints"
	"github.com/juju/juju/container"
	"github.com/juju/juju/container/lxc/mock"
	lxctesting "github.com/juju/juju/container/lxc/testing"
	containertesting "github.com/juju/juju/container/testing"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/filestorage"
	envtesting "github.com/juju/juju/environs/testing"
	"github.com/juju/juju/environs/tools"
	"github.com/juju/juju/instance"
	instancetest "github.com/juju/juju/instance/testing"
	jujutesting "github.com/juju/juju/juju/testing"
	"github.com/juju/juju/network"
	"github.com/juju/juju/state"
	"github.com/juju/juju/status"
	"github.com/juju/juju/storage"
	"github.com/juju/juju/storage/provider"
	coretesting "github.com/juju/juju/testing"
	coretools "github.com/juju/juju/tools"
	jujuversion "github.com/juju/juju/version"
	"github.com/juju/juju/worker/provisioner"
)

type lxcSuite struct {
	lxctesting.TestSuite
	events     chan mock.Event
	eventsDone chan struct{}
}

type lxcBrokerSuite struct {
	lxcSuite
	broker      environs.InstanceBroker
	namespace   instance.Namespace
	agentConfig agent.ConfigSetterWriter
	api         *fakeAPI
}

var _ = gc.Suite(&lxcBrokerSuite{})

func (s *lxcSuite) SetUpTest(c *gc.C) {
	s.TestSuite.SetUpTest(c)
	if runtime.GOOS == "windows" {
		c.Skip("Skipping lxc tests on windows")
	}
	s.events = make(chan mock.Event)
	s.eventsDone = make(chan struct{})
	go func() {
		defer close(s.eventsDone)
		for event := range s.events {
			c.Output(3, fmt.Sprintf("lxc event: <%s, %s>", event.Action, event.InstanceId))
		}
	}()
	s.TestSuite.ContainerFactory.AddListener(s.events)
}

func (s *lxcSuite) TearDownTest(c *gc.C) {
	close(s.events)
	<-s.eventsDone
	s.TestSuite.TearDownTest(c)
}

func (s *lxcBrokerSuite) SetUpTest(c *gc.C) {
	if runtime.GOOS == "windows" {
		c.Skip("Skipping lxc tests on windows")
	}
	s.lxcSuite.SetUpTest(c)
	var err error
	s.agentConfig, err = agent.NewAgentConfig(
		agent.AgentConfigParams{
			Paths:             agent.NewPathsWithDefaults(agent.Paths{DataDir: "/not/used/here"}),
			Tag:               names.NewMachineTag("1"),
			UpgradedToVersion: jujuversion.Current,
			Password:          "dummy-secret",
			Nonce:             "nonce",
			APIAddresses:      []string{"10.0.0.1:1234"},
			CACert:            coretesting.CACert,
			Model:             coretesting.ModelTag,
		})
	c.Assert(err, jc.ErrorIsNil)
	managerConfig := container.ManagerConfig{
		container.ConfigModelUUID: coretesting.ModelTag.Id(),
		"log-dir":                 c.MkDir(),
		"use-clone":               "false",
	}
	s.api = NewFakeAPI()
	s.broker, err = provisioner.NewLxcBroker(s.api, s.agentConfig, managerConfig, nil, 0)
	c.Assert(err, jc.ErrorIsNil)
	// Create the same namespace that the broker uses to ensure dirs on disk exist.
	s.namespace, err = instance.NewNamespace(coretesting.ModelTag.Id())
	c.Assert(err, jc.ErrorIsNil)
}

func (s *lxcBrokerSuite) instanceConfig(c *gc.C, machineId string) *instancecfg.InstanceConfig {
	machineNonce := "fake-nonce"
	// To isolate the tests from the host's architecture, we override it here.
	s.PatchValue(&arch.HostArch, func() string { return arch.AMD64 })
	apiInfo := jujutesting.FakeAPIInfo(machineId)
	instanceConfig, err := instancecfg.NewInstanceConfig(machineId, machineNonce, "released", "quantal", true, apiInfo)
	c.Assert(err, jc.ErrorIsNil)
	hostname, err := s.namespace.Hostname(machineId)
	c.Assert(err, jc.ErrorIsNil)
	// Ensure the <rootfs>/etc/network path exists.
	containertesting.EnsureLXCRootFSEtcNetwork(c, hostname)
	return instanceConfig
}

func (s *lxcBrokerSuite) startInstance(c *gc.C, machineId string, volumes []storage.VolumeParams) instance.Instance {
	instanceConfig := s.instanceConfig(c, machineId)
	cons := constraints.Value{}
	possibleTools := coretools.List{&coretools.Tools{
		Version: version.MustParseBinary("2.3.4-quantal-amd64"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-amd64.tgz",
	}, {
		// non-host-arch tools should be filtered out by StartInstance
		Version: version.MustParseBinary("2.3.4-quantal-arm64"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-arm64.tgz",
	}}
	callback := func(settableStatus status.Status, info string, data map[string]interface{}) error {
		return nil
	}
	result, err := s.broker.StartInstance(environs.StartInstanceParams{
		Constraints:    cons,
		Tools:          possibleTools,
		InstanceConfig: instanceConfig,
		Volumes:        volumes,
		StatusCallback: callback,
	})
	c.Assert(err, jc.ErrorIsNil)
	return result.Instance
}

func (s *lxcBrokerSuite) maintainInstance(c *gc.C, machineId string, volumes []storage.VolumeParams) {
	instanceConfig := s.instanceConfig(c, machineId)
	cons := constraints.Value{}
	possibleTools := coretools.List{&coretools.Tools{
		Version: version.MustParseBinary("2.3.4-quantal-amd64"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-amd64.tgz",
	}}
	callback := func(settableStatus status.Status, info string, data map[string]interface{}) error {
		return nil
	}
	err := s.broker.MaintainInstance(environs.StartInstanceParams{
		Constraints:    cons,
		Tools:          possibleTools,
		InstanceConfig: instanceConfig,
		Volumes:        volumes,
		StatusCallback: callback,
	})
	c.Assert(err, jc.ErrorIsNil)
}

func (s *lxcBrokerSuite) assertDefaultStorageConfig(c *gc.C, lxc instance.Instance) {
	config := filepath.Join(s.LxcDir, string(lxc.Id()), "config")
	AssertFileContents(c, gc.Not(jc.Contains), config, "lxc.aa_profile = lxc-container-default-with-mounting")
}

func (s *lxcBrokerSuite) assertDefaultNetworkConfig(c *gc.C, lxc instance.Instance) {
	lxc_conf := filepath.Join(s.ContainerDir, string(lxc.Id()), "lxc.conf")
	expect := []string{
		"lxc.network.type = veth",
		"lxc.network.link = lxcbr0",
	}
	AssertFileContains(c, lxc_conf, expect...)
}

func (s *lxcBrokerSuite) TestStartInstance(c *gc.C) {
	machineId := "1/lxc/0"
	lxc := s.startInstance(c, machineId, nil)
	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "PrepareContainerInterfaceInfo",
		Args:     []interface{}{names.NewMachineTag("1-lxc-0")},
	}})
	c.Assert(lxc.Id(), gc.Equals, instance.Id("juju-06f00d-1-lxc-0"))
	c.Assert(s.lxcContainerDir(lxc), jc.IsDirectory)
	s.assertInstances(c, lxc)
	s.assertDefaultNetworkConfig(c, lxc)
	s.assertDefaultStorageConfig(c, lxc)
}

func (s *lxcBrokerSuite) TestMaintainInstance(c *gc.C) {
	machineId := "1/lxc/0"
	lxc := s.startInstance(c, machineId, nil)
	s.api.ResetCalls()

	s.maintainInstance(c, machineId, nil)
	s.api.CheckCalls(c, []gitjujutesting.StubCall{})
	c.Assert(lxc.Id(), gc.Equals, instance.Id("juju-06f00d-1-lxc-0"))
	c.Assert(s.lxcContainerDir(lxc), jc.IsDirectory)
	s.assertInstances(c, lxc)
	s.assertDefaultNetworkConfig(c, lxc)
	s.assertDefaultStorageConfig(c, lxc)
}

func (s *lxcBrokerSuite) TestStartInstanceWithStorage(c *gc.C) {
	s.api.fakeContainerConfig.AllowLXCLoopMounts = true

	machineId := "1/lxc/0"
	lxc := s.startInstance(c, machineId, []storage.VolumeParams{{Provider: provider.LoopProviderType}})
	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "PrepareContainerInterfaceInfo",
		Args:     []interface{}{names.NewMachineTag("1-lxc-0")},
	}})
	c.Assert(lxc.Id(), gc.Equals, instance.Id("juju-06f00d-1-lxc-0"))
	c.Assert(s.lxcContainerDir(lxc), jc.IsDirectory)
	s.assertInstances(c, lxc)
	// Check storage config.
	config := filepath.Join(s.LxcDir, string(lxc.Id()), "config")
	AssertFileContents(c, jc.Contains, config, "lxc.aa_profile = lxc-container-default-with-mounting")
}

func (s *lxcBrokerSuite) TestStartInstanceLoopMountsDisallowed(c *gc.C) {
	s.api.fakeContainerConfig.AllowLXCLoopMounts = false
	machineId := "1/lxc/0"
	lxc := s.startInstance(c, machineId, []storage.VolumeParams{{Provider: provider.LoopProviderType}})
	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "PrepareContainerInterfaceInfo",
		Args:     []interface{}{names.NewMachineTag("1-lxc-0")},
	}})
	c.Assert(lxc.Id(), gc.Equals, instance.Id("juju-06f00d-1-lxc-0"))
	c.Assert(s.lxcContainerDir(lxc), jc.IsDirectory)
	s.assertInstances(c, lxc)
	s.assertDefaultStorageConfig(c, lxc)
}

func (s *lxcBrokerSuite) TestStartInstanceHostArch(c *gc.C) {
	instanceConfig := s.instanceConfig(c, "1/lxc/0")

	// Patch the host's arch, so the LXC broker will filter tools.
	s.PatchValue(&arch.HostArch, func() string { return arch.PPC64EL })
	possibleTools := coretools.List{&coretools.Tools{
		Version: version.MustParseBinary("2.3.4-quantal-amd64"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-amd64.tgz",
	}, {
		Version: version.MustParseBinary("2.3.4-quantal-ppc64el"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-ppc64el.tgz",
	}}
	callback := func(settableStatus status.Status, info string, data map[string]interface{}) error {
		return nil
	}

	_, err := s.broker.StartInstance(environs.StartInstanceParams{
		Constraints:    constraints.Value{},
		Tools:          possibleTools,
		InstanceConfig: instanceConfig,
		StatusCallback: callback,
	})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(instanceConfig.AgentVersion().Arch, gc.Equals, arch.PPC64EL)
}

func (s *lxcBrokerSuite) TestStartInstanceToolsArchNotFound(c *gc.C) {
	instanceConfig := s.instanceConfig(c, "1/lxc/0")

	// Patch the host's arch, so the LXC broker will filter tools.
	s.PatchValue(&arch.HostArch, func() string { return arch.PPC64EL })
	possibleTools := coretools.List{&coretools.Tools{
		Version: version.MustParseBinary("2.3.4-quantal-amd64"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-amd64.tgz",
	}}
	callback := func(settableStatus status.Status, info string, data map[string]interface{}) error {
		return nil
	}

	_, err := s.broker.StartInstance(environs.StartInstanceParams{
		Constraints:    constraints.Value{},
		Tools:          possibleTools,
		InstanceConfig: instanceConfig,
		StatusCallback: callback,
	})
	c.Assert(err, gc.ErrorMatches, "need tools for arch ppc64el, only found \\[amd64\\]")
}

func (s *lxcBrokerSuite) TestStartInstanceWithBridgeEnviron(c *gc.C) {
	s.agentConfig.SetValue(agent.LxcBridge, "br0")
	machineId := "1/lxc/0"
	lxc := s.startInstance(c, machineId, nil)
	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "PrepareContainerInterfaceInfo",
		Args:     []interface{}{names.NewMachineTag("1-lxc-0")},
	}})
	c.Assert(lxc.Id(), gc.Equals, instance.Id("juju-06f00d-1-lxc-0"))
	c.Assert(s.lxcContainerDir(lxc), jc.IsDirectory)
	s.assertInstances(c, lxc)
	// Uses default network config
	lxc_conf := filepath.Join(s.ContainerDir, string(lxc.Id()), "lxc.conf")
	expect := []string{
		"lxc.network.type = veth",
		"lxc.network.link = br0",
	}
	AssertFileContains(c, lxc_conf, expect...)
}

func (s *lxcBrokerSuite) TestStartInstancePopulatesNetworkInfo(c *gc.C) {
	fakeResolvConf := filepath.Join(c.MkDir(), "resolv.conf")
	err := ioutil.WriteFile(fakeResolvConf, []byte("nameserver ns1.dummy\nnameserver ns2.dummy\nsearch dummy\n"), 0644)
	c.Assert(err, jc.ErrorIsNil)
	s.PatchValue(provisioner.ResolvConf, fakeResolvConf)

	instanceConfig := s.instanceConfig(c, "42")
	possibleTools := coretools.List{&coretools.Tools{
		Version: version.MustParseBinary("2.3.4-quantal-amd64"),
		URL:     "http://tools.testing.invalid/2.3.4-quantal-amd64.tgz",
	}}
	callback := func(settableStatus status.Status, info string, data map[string]interface{}) error {
		return nil
	}

	result, err := s.broker.StartInstance(environs.StartInstanceParams{
		Constraints:    constraints.Value{},
		Tools:          possibleTools,
		InstanceConfig: instanceConfig,
		StatusCallback: callback,
	})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(result.NetworkInfo, gc.HasLen, 1)

	iface := result.NetworkInfo[0]
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(iface, jc.DeepEquals, network.InterfaceInfo{
		DeviceIndex:      0,
		CIDR:             "0.1.2.0/24",
		InterfaceName:    "dummy0",
		DNSServers:       network.NewAddresses("ns1.dummy", "ns2.dummy"),
		DNSSearchDomains: []string{"dummy"},
		MACAddress:       "aa:bb:cc:dd:ee:ff",
		Address:          network.NewAddress("0.1.2.3"),
		GatewayAddress:   network.NewAddress("0.1.2.1"),
	})
}

func (s *lxcBrokerSuite) TestStopInstance(c *gc.C) {
	lxc0 := s.startInstance(c, "1/lxc/0", nil)
	lxc1 := s.startInstance(c, "1/lxc/1", nil)
	lxc2 := s.startInstance(c, "1/lxc/2", nil)

	s.assertInstances(c, lxc0, lxc1, lxc2)
	err := s.broker.StopInstances(lxc0.Id())
	c.Assert(err, jc.ErrorIsNil)
	s.assertInstances(c, lxc1, lxc2)
	c.Assert(s.lxcContainerDir(lxc0), jc.DoesNotExist)
	c.Assert(s.lxcRemovedContainerDir(lxc0), jc.IsDirectory)

	err = s.broker.StopInstances(lxc1.Id(), lxc2.Id())
	c.Assert(err, jc.ErrorIsNil)
	s.assertInstances(c)
}

func (s *lxcBrokerSuite) TestAllInstances(c *gc.C) {
	lxc0 := s.startInstance(c, "1/lxc/0", nil)
	lxc1 := s.startInstance(c, "1/lxc/1", nil)
	s.assertInstances(c, lxc0, lxc1)

	err := s.broker.StopInstances(lxc1.Id())
	c.Assert(err, jc.ErrorIsNil)
	lxc2 := s.startInstance(c, "1/lxc/2", nil)
	s.assertInstances(c, lxc0, lxc2)
}

func (s *lxcBrokerSuite) assertInstances(c *gc.C, inst ...instance.Instance) {
	results, err := s.broker.AllInstances()
	c.Assert(err, jc.ErrorIsNil)
	instancetest.MatchInstances(c, results, inst...)
}

func (s *lxcBrokerSuite) lxcContainerDir(inst instance.Instance) string {
	return filepath.Join(s.ContainerDir, string(inst.Id()))
}

func (s *lxcBrokerSuite) lxcRemovedContainerDir(inst instance.Instance) string {
	return filepath.Join(s.RemovedDir, string(inst.Id()))
}

func (s *lxcBrokerSuite) TestLocalDNSServers(c *gc.C) {
	fakeConf := filepath.Join(c.MkDir(), "resolv.conf")
	s.PatchValue(provisioner.ResolvConf, fakeConf)

	// If config is missing, that's OK.
	dnses, dnsSearch, err := provisioner.LocalDNSServers()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(dnses, gc.HasLen, 0)
	c.Assert(dnsSearch, gc.Equals, "")

	// Enter some data in fakeConf.
	data := `
 anything else is ignored
  # comments are ignored
  nameserver  0.1.2.3  # that's parsed
search  foo.baz # comment ignored
# nameserver 42.42.42.42 - ignored as well
nameserver 8.8.8.8
nameserver example.com # comment after is ok
`
	err = ioutil.WriteFile(fakeConf, []byte(data), 0644)
	c.Assert(err, jc.ErrorIsNil)

	dnses, dnsSearch, err = provisioner.LocalDNSServers()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(dnses, jc.DeepEquals, network.NewAddresses(
		"0.1.2.3", "8.8.8.8", "example.com",
	))
	c.Assert(dnsSearch, gc.Equals, "foo.baz")
}

type lxcProvisionerSuite struct {
	CommonProvisionerSuite
	lxcSuite
	events    chan mock.Event
	namespace instance.Namespace
}

var _ = gc.Suite(&lxcProvisionerSuite{})

func (s *lxcProvisionerSuite) SetUpSuite(c *gc.C) {
	if runtime.GOOS == "windows" {
		c.Skip("Skipping lxc tests on windows")
	}
	s.CommonProvisionerSuite.SetUpSuite(c)
	s.lxcSuite.SetUpSuite(c)
}

func (s *lxcProvisionerSuite) TearDownSuite(c *gc.C) {
	s.lxcSuite.TearDownSuite(c)
	s.CommonProvisionerSuite.TearDownSuite(c)
}

func (s *lxcProvisionerSuite) SetUpTest(c *gc.C) {
	s.CommonProvisionerSuite.SetUpTest(c)
	s.lxcSuite.SetUpTest(c)

	s.events = make(chan mock.Event, 25)
	s.ContainerFactory.AddListener(s.events)
	// Create the same namespace that the broker uses to ensure dirs on disk exist.
	namespace, err := instance.NewNamespace(coretesting.ModelTag.Id())
	c.Assert(err, jc.ErrorIsNil)
	s.namespace = namespace
}

func (s *lxcProvisionerSuite) expectStarted(c *gc.C, machine *state.Machine) string {
	// This check in particular leads to tests just hanging
	// indefinitely quite often on i386.
	coretesting.SkipIfI386(c, "lp:1425569")

	var event mock.Event
	s.State.StartSync()
	select {
	case event = <-s.events:
		c.Assert(event.Action, gc.Equals, mock.Created)
		argsSet := set.NewStrings(event.TemplateArgs...)
		c.Assert(argsSet.Contains("imageURL"), jc.IsTrue)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout while waiting the mock container to get created")
	}

	select {
	case event = <-s.events:
		c.Assert(event.Action, gc.Equals, mock.Started)
		err := machine.Refresh()
		c.Assert(err, jc.ErrorIsNil)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout while waiting the mock container to start")
	}

	s.waitInstanceId(c, machine, instance.Id(event.InstanceId))
	return event.InstanceId
}

func (s *lxcProvisionerSuite) expectStopped(c *gc.C, instId string) {
	// This check in particular leads to tests just hanging
	// indefinitely quite often on i386.
	coretesting.SkipIfI386(c, "lp:1425569")

	s.State.StartSync()
	select {
	case event := <-s.events:
		c.Assert(event.Action, gc.Equals, mock.Stopped)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout while waiting the mock container to stop")
	}

	select {
	case event := <-s.events:
		c.Assert(event.Action, gc.Equals, mock.Destroyed)
		c.Assert(event.InstanceId, gc.Equals, instId)
	case <-time.After(coretesting.LongWait):
		c.Fatalf("timeout while waiting the mock container to get destroyed")
	}
}

func (s *lxcProvisionerSuite) expectNoEvents(c *gc.C) {
	select {
	case event := <-s.events:
		c.Fatalf("unexpected event %#v", event)
	case <-time.After(coretesting.ShortWait):
		return
	}
}

func (s *lxcProvisionerSuite) TearDownTest(c *gc.C) {
	close(s.events)
	s.lxcSuite.TearDownTest(c)
	s.CommonProvisionerSuite.TearDownTest(c)
}

func (s *lxcProvisionerSuite) newLxcProvisioner(c *gc.C) provisioner.Provisioner {
	parentMachineTag := names.NewMachineTag("0")
	agentConfig := s.AgentConfigForTag(c, parentMachineTag)
	managerConfig := container.ManagerConfig{
		container.ConfigModelUUID: coretesting.ModelTag.Id(),
		"log-dir":                 c.MkDir(),
		"use-clone":               "false",
	}
	broker, err := provisioner.NewLxcBroker(s.provisioner, agentConfig, managerConfig, &containertesting.MockURLGetter{}, 0)
	c.Assert(err, jc.ErrorIsNil)
	toolsFinder := (*provisioner.GetToolsFinder)(s.provisioner)
	w, err := provisioner.NewContainerProvisioner(instance.LXC, s.provisioner, agentConfig, broker, toolsFinder)
	c.Assert(err, jc.ErrorIsNil)
	return w
}

func (s *lxcProvisionerSuite) TestProvisionerStartStop(c *gc.C) {
	p := s.newLxcProvisioner(c)
	stop(c, p)
}

func (s *lxcProvisionerSuite) TestDoesNotStartEnvironMachines(c *gc.C) {
	p := s.newLxcProvisioner(c)
	defer stop(c, p)

	// Check that an instance is not provisioned when the machine is created.
	_, err := s.State.AddMachine(series.LatestLts(), state.JobHostUnits)
	c.Assert(err, jc.ErrorIsNil)

	s.expectNoEvents(c)
}

func (s *lxcProvisionerSuite) TestDoesNotHaveRetryWatcher(c *gc.C) {
	p := s.newLxcProvisioner(c)
	defer stop(c, p)

	w, err := provisioner.GetRetryWatcher(p)
	c.Assert(w, gc.IsNil)
	c.Assert(err, jc.Satisfies, errors.IsNotImplemented)
}

func (s *lxcProvisionerSuite) addContainer(c *gc.C) *state.Machine {
	template := state.MachineTemplate{
		Series: series.LatestLts(),
		Jobs:   []state.MachineJob{state.JobHostUnits},
	}
	container, err := s.State.AddMachineInsideMachine(template, "0", instance.LXC)
	c.Assert(err, jc.ErrorIsNil)
	return container
}

func (s *lxcProvisionerSuite) maybeUploadTools(c *gc.C) {
	// The default series tools are already uploaded
	// for amd64 in the base suite.
	if arch.HostArch() == arch.AMD64 {
		return
	}

	storageDir := c.MkDir()
	s.CommonProvisionerSuite.PatchValue(&tools.DefaultBaseURL, storageDir)
	stor, err := filestorage.NewFileStorageWriter(storageDir)
	c.Assert(err, jc.ErrorIsNil)

	defaultTools := version.Binary{
		Number: jujuversion.Current,
		Arch:   arch.HostArch(),
		Series: series.LatestLts(),
	}

	envtesting.AssertUploadFakeToolsVersions(c, stor, "devel", "devel", defaultTools)
	envtesting.AssertUploadFakeToolsVersions(c, stor, "released", "released", defaultTools)
}

func (s *lxcProvisionerSuite) TestContainerStartedAndStopped(c *gc.C) {
	coretesting.SkipIfI386(c, "lp:1425569")
	s.maybeUploadTools(c)

	p := s.newLxcProvisioner(c)
	defer stop(c, p)

	container := s.addContainer(c)
	hostname, err := s.namespace.Hostname(container.MachineTag().Id())
	c.Assert(err, jc.ErrorIsNil)
	containertesting.EnsureLXCRootFSEtcNetwork(c, hostname)
	instId := s.expectStarted(c, container)

	// ...and removed, along with the machine, when the machine is Dead.
	c.Assert(container.EnsureDead(), gc.IsNil)
	s.expectStopped(c, instId)
	s.waitRemoved(c, container)
}

func (s *lxcProvisionerSuite) TestLXCProvisionerObservesConfigChanges(c *gc.C) {
	p := s.newLxcProvisioner(c)
	defer stop(c, p)
	s.assertProvisionerObservesConfigChanges(c, p)
}

type fakeAPI struct {
	*gitjujutesting.Stub

	fakeContainerConfig params.ContainerConfig
	fakeInterfaceInfo   network.InterfaceInfo
}

var _ provisioner.APICalls = (*fakeAPI)(nil)

var fakeInterfaceInfo network.InterfaceInfo = network.InterfaceInfo{
	DeviceIndex:    0,
	MACAddress:     "aa:bb:cc:dd:ee:ff",
	CIDR:           "0.1.2.0/24",
	InterfaceName:  "dummy0",
	Address:        network.NewAddress("0.1.2.3"),
	GatewayAddress: network.NewAddress("0.1.2.1"),
}

var fakeContainerConfig = params.ContainerConfig{
	UpdateBehavior:          &params.UpdateBehavior{true, true},
	ProviderType:            "fake",
	AuthorizedKeys:          coretesting.FakeAuthKeys,
	SSLHostnameVerification: true,
}

func NewFakeAPI() *fakeAPI {
	return &fakeAPI{
		Stub:                &gitjujutesting.Stub{},
		fakeContainerConfig: fakeContainerConfig,
		fakeInterfaceInfo:   fakeInterfaceInfo,
	}
}

func (f *fakeAPI) ContainerConfig() (params.ContainerConfig, error) {
	f.MethodCall(f, "ContainerConfig")
	if err := f.NextErr(); err != nil {
		return params.ContainerConfig{}, err
	}
	return f.fakeContainerConfig, nil
}

func (f *fakeAPI) PrepareContainerInterfaceInfo(tag names.MachineTag) ([]network.InterfaceInfo, error) {
	f.MethodCall(f, "PrepareContainerInterfaceInfo", tag)
	if err := f.NextErr(); err != nil {
		return nil, err
	}
	return []network.InterfaceInfo{f.fakeInterfaceInfo}, nil
}

func (f *fakeAPI) GetContainerInterfaceInfo(tag names.MachineTag) ([]network.InterfaceInfo, error) {
	f.MethodCall(f, "GetContainerInterfaceInfo", tag)
	if err := f.NextErr(); err != nil {
		return nil, err
	}
	return []network.InterfaceInfo{f.fakeInterfaceInfo}, nil
}

func (f *fakeAPI) ReleaseContainerAddresses(tag names.MachineTag) error {
	f.MethodCall(f, "ReleaseContainerAddresses", tag)
	if err := f.NextErr(); err != nil {
		return err
	}
	return nil
}
