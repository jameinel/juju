// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package lxd

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/juju/errors"
	"github.com/lxc/lxd/shared/api"

	"github.com/juju/juju/network"
)

const (
	nic            = "nic"
	nicTypeBridged = "bridged"
	nicTypeMACVLAN = "macvlan"
)

// LocalBridgeName returns the name of the local LXD network bridge.
func (s *Server) LocalBridgeName() string {
	return s.localBridgeName
}

// EnsureIPv4 retrieves the network for the input name and checks its IPv4
// configuration. If none is detected, it is set to "auto".
// The boolean return indicates if modification was necessary.
func (s *Server) EnsureIPv4(netName string) (bool, error) {
	var modified bool

	net, eTag, err := s.GetNetwork(netName)
	if err != nil {
		return false, errors.Trace(err)
	}

	cfg, ok := net.Config["ipv4.address"]
	if !ok || cfg == "none" {
		if net.Config == nil {
			net.Config = make(map[string]string, 2)
		}
		net.Config["ipv4.address"] = "auto"
		net.Config["ipv4.nat"] = "true"

		if err := s.UpdateNetwork(netName, net.Writable(), eTag); err != nil {
			return false, errors.Trace(err)
		}
		modified = true
	}

	return modified, nil
}

// VerifyNetworkDevice attempts to ensure that there is a network usable by LXD
// and that there is a NIC device with said network as its parent.
// If there are no NIC devices, and this server is *not* in cluster mode,
// an attempt is made to create an new device in the input profile, with the
// with the default LXD bridge as its parent.
func (s *Server) VerifyNetworkDevice(profile *api.Profile, eTag string) error {
	nics := getProfileNICs(profile)

	if len(nics) == 0 {
		if s.networkAPISupport && !s.clustered {
			return errors.Annotate(s.ensureDefaultNetworking(profile, eTag), "ensuring default bridge config")
		}
		return errors.Errorf("profile %q does not have any devices configured with type %q", profile.Name, nic)
	}

	if s.networkAPISupport {
		return errors.Annotatef(s.verifyNICsWithAPI(nics), "profile %q", profile.Name)
	}

	return errors.Annotatef(s.verifyNICsWithConfigFile(nics, ioutil.ReadFile), "profile %q", profile.Name)
}

// ensureDefaultNetworking ensures that the default LXD bridge exists,
// that it is not configured to use IPv6, and that a NIC device exists in
// the input profile.
// An error is returned if the bridge exists with IPv6 configuration.
// If the bridge does not exist, it is created.
func (s *Server) ensureDefaultNetworking(profile *api.Profile, eTag string) error {
	net, _, err := s.GetNetwork(network.DefaultLXDBridge)
	if err != nil {
		if !IsLXDNotFound(err) {
			return errors.Trace(err)
		}
		req := api.NetworksPost{
			Name:    network.DefaultLXDBridge,
			Type:    "bridge",
			Managed: true,
			NetworkPut: api.NetworkPut{Config: map[string]string{
				"ipv4.address": "auto",
				"ipv4.nat":     "true",
				"ipv6.address": "none",
				"ipv6.nat":     "false",
			}},
		}
		err := s.CreateNetwork(req)
		if err != nil {
			return errors.Trace(err)
		}
		net, _, err = s.GetNetwork(network.DefaultLXDBridge)
		if err != nil {
			return errors.Trace(err)
		}
	} else {
		if err := verifyNoIPv6(net); err != nil {
			return errors.Trace(err)
		}
	}

	s.localBridgeName = network.DefaultLXDBridge

	nicName := generateNICDeviceName(profile)
	if nicName == "" {
		return errors.Errorf("failed to generate a unique device name for profile %q", profile.Name)
	}

	// Add the new device with the bridge as its parent.
	nicType := nicTypeMACVLAN
	if net.Type == "bridge" {
		nicType = nicTypeBridged
	}
	profile.Devices[nicName] = map[string]string{
		"type":    nic,
		"nictype": nicType,
		"parent":  network.DefaultLXDBridge,
	}

	if err := s.UpdateProfile(profile.Name, profile.Writable(), eTag); err != nil {
		return errors.Trace(err)
	} else {
		logger.Debugf("created new nic device %q in profile %q", nicName, profile.Name)
		return nil
	}
}

// verifyNICsWithAPI uses the LXD network API to check if one of the input NIC
// devices is suitable for LXD to work with Juju.
func (s *Server) verifyNICsWithAPI(nics map[string]map[string]string) error {
	checked := make([]string, 0, len(nics))
	for name, nic := range nics {
		checked = append(checked, name)

		if !isValidNICType(nic) {
			continue
		}

		netName := nic["parent"]
		if netName == "" {
			continue
		}

		net, _, err := s.GetNetwork(netName)
		if err != nil {
			return errors.Annotatef(err, "retrieving network %q", netName)
		}
		if err := verifyNoIPv6(net); err != nil {
			continue
		}

		logger.Infof("found usable network device %q with parent %q", name, netName)
		s.localBridgeName = netName
		return nil
	}

	return errors.Errorf("no network device found with nictype %q or %q, and without IPv6 configured."+
		"\n\tthe following devices were checked: %v", nicTypeBridged, nicTypeMACVLAN, checked)
}

// verifyNICsWithConfigFile is recruited for legacy LXD installations.
// It checks the LXD bridge configuration file and ensure that one of the input
// devices is suitable for LXD to work with Juju.
func (s *Server) verifyNICsWithConfigFile(
	nics map[string]map[string]string, reader func(string) ([]byte, error),
) error {
	netName, err := checkBridgeConfigFile(reader)
	if err != nil {
		return errors.Trace(err)
	}

	checked := make([]string, 0, len(nics))
	for name, nic := range nics {
		checked = append(checked, name)

		if nic["parent"] != netName {
			continue
		}
		if !isValidNICType(nic) {
			continue
		}

		logger.Infof("found usable network device %q with parent %q", name, netName)
		s.localBridgeName = netName
		return nil
	}

	return errors.Errorf("no network device found with nictype %q or %q that uses the configured bridge in %s"+
		"\n\tthe following devices were checked: %v", nicTypeBridged, nicTypeMACVLAN, BridgeConfigFile, checked)
}

// generateNICDeviceName attempts to generate a new NIC device name that is not
// already in the input profile. If none can be determined in a reasonable
// search space, an empty name is returned. This should never really happen,
// but the name generation aborts to be safe from (theoretical) integer overflow.
func generateNICDeviceName(profile *api.Profile) string {
	template := "eth%d"
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf(template, i)
		unique := true
		for d := range profile.Devices {
			if d == name {
				unique = false
				break
			}
		}
		if unique {
			return name
		}
	}
	return ""
}

// getProfileNICs iterates over the devices in the input profile and returns
// any that are of type "nic".
func getProfileNICs(profile *api.Profile) map[string]map[string]string {
	nics := make(map[string]map[string]string, len(profile.Devices))
	for k, v := range profile.Devices {
		if v["type"] == nic {
			nics[k] = v
		}
	}
	return nics
}

// verifyNoIPv6 checks that the input network has no IPv6 configuration.
// An error is returned when it does.
func verifyNoIPv6(net *api.Network) error {
	if !net.Managed {
		return nil
	}
	cfg, ok := net.Config["ipv6.address"]
	if !ok {
		return nil
	}
	if cfg == "none" {
		return nil
	}

	return errors.Errorf("juju does not support IPv6. Disable IPv6 in LXD via:\n"+
		"\tlxc network set %s ipv6.address none\n"+
		"and run the command again", net.Name)
}

func isValidNICType(nic map[string]string) bool {
	return nic["nictype"] == nicTypeBridged || nic["nictype"] == nicTypeMACVLAN
}

const BridgeConfigFile = "/etc/default/lxd-bridge"

func checkBridgeConfigFile(reader func(string) ([]byte, error)) (string, error) {
	bridgeConfig, err := reader(BridgeConfigFile)
	if os.IsNotExist(err) {
		return "", bridgeConfigError("no config file found at " + BridgeConfigFile)
	} else if err != nil {
		return "", errors.Trace(err)
	}

	foundSubnetConfig := false
	bridgeName := ""
	for _, line := range strings.Split(string(bridgeConfig), "\n") {
		if strings.HasPrefix(line, "USE_LXD_BRIDGE=") {
			b, err := strconv.ParseBool(strings.Trim(line[len("USE_LXD_BRIDGE="):], " \""))
			if err != nil {
				logger.Debugf("unable to parse bool, skipping USE_LXD_BRIDGE check: %s", err)
				continue
			}
			if !b {
				return "", bridgeConfigError(fmt.Sprintf("%s has USE_LXD_BRIDGE set to false", BridgeConfigFile))
			}
		} else if strings.HasPrefix(line, "LXD_BRIDGE=") {
			bridgeName = strings.Trim(line[len("LXD_BRIDGE="):], " \"")
			if bridgeName == "" {
				return "", bridgeConfigError(fmt.Sprintf("%s has no LXD_BRIDGE set", BridgeConfigFile))
			}
		} else if strings.HasPrefix(line, "LXD_IPV4_ADDR=") {
			contents := strings.Trim(line[len("LXD_IPV4_ADDR="):], " \"")
			if len(contents) > 0 {
				foundSubnetConfig = true
			}
		} else if strings.HasPrefix(line, "LXD_IPV6_ADDR=") {
			contents := strings.Trim(line[len("LXD_IPV6_ADDR="):], " \"")
			if len(contents) > 0 {
				return "", ipv6BridgeConfigError(BridgeConfigFile)
			}
		}
	}

	if !foundSubnetConfig {
		return "", bridgeConfigError(bridgeName + " has no ipv4 or ipv6 subnet enabled")
	}
	return bridgeName, nil
}

func bridgeConfigError(err string) error {
	return errors.Errorf("%s\nIt looks like your LXD bridge has not yet been configured. Configure it via:\n\n"+
		"\tsudo dpkg-reconfigure -p medium lxd\n\n"+
		"and run the command again.", err)
}

func ipv6BridgeConfigError(fileName string) error {
	return errors.Errorf("%s has IPv6 enabled.\nJuju doesn't currently support IPv6.\n"+
		"Disable IPv6 via:\n\n"+
		"\tsudo dpkg-reconfigure -p medium lxd\n\n"+
		"and run the command again.", fileName)
}
