// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package machine

import (
	"github.com/juju/juju/worker/dependency"
)

// These define the names of the dependency.Manifolds we use in a machine agent.
// This block of manifold names define the workers we want to run once per machine
// (as opposed to those we run once per agent: see cmd/jujud/common).
var (
	MachinerName           = "machiner"            //                             worker/machiner
	StorageProvisionerName = "storage-provisioner" //                             worker/storageprovisioner
	LocalDiskManager       = "local-disk-manager"  //                             worker/diskmanager
	RebootHandlerName      = "reboot-handler"      //                             worker/reboot
)

// The following manifolds are run only when the machine has JobHostUnits.
var (
	UnitDeployerName = "unit-deployer" //                                         worker/deployer
)

// The following manifolds are run only when the machine has JobManageEnviron.
// They're pretty tangled and need to be teased apart with care.
var (
	JESStateConnectionName             = "jes-state-connection"         //        <inline, see apiconn>
	JESStateConnectionLeaderName       = "jes-state-connection-leader"  //        <inline>
	JESStateConnectionPeerGroupUpdater = "jes-state-peer-group-updater" //        worker/peergrouper
	JESSSHIdentityFileWriter           = "jes-ssh-identity-file-writer" //        <inline>
	JESLeaseManagerName                = "jes-lease-manager"            //        lease <needs work>
	JESAPIServerName                   = "jes-api-server"               //        apiserver
	JESCertificateUpdater              = "jes-certificate-updater"      //        worker/certupdater
)

// The following manifolds are run only when the machine has JobManageEnviron
// *and* the state-connection-leader worker is available, despite these ones
// not needing a state connection at all; a lease-based approach would almost
// certainly be superior to this mixing of concerns, but this is far from the
// worst feature of the current structure.
var (
	JESComputeProvisioner   = "jes-compute-provisioner"    //                     worker/provisioner
	JESStorageProvisioner   = "jes-storage-provisioner"    //                     worker/storageprovisioner
	JESFirewallProvisioner  = "jes-firewall-provisioner"   //                     worker/firewaller
	JESCharmRevisionUpdater = "jes-charm-revision-updater" //                     worker/charmrevisionworker
	JESMetricsManager       = "jes-metrics-manager"        //                     worker/metricworker
)

// TODO(fwereade): 2015-04-22
// The following manifolds are run only when the machine has JobManageEnviron
// *and* the state-connection-leader worker is available; they use the state
// connection directly but *absolutely* should not do so.
var (
	JESInstancePoller     = "jes-instance-poller"     //                          worker/instancepoller
	JESStateCleaner       = "jes-state-cleaner"       //                          worker/cleaner
	JESServiceScaler      = "jes-service-scaler"      //                          worker/minunitsworker
	JESTransactionResumer = "jes-transaction-resumer" //                          worker/resumer
)

// These manifolds have annoying special cases attached.
var (

	// NOTE: when the upgrade-steps-worker finishes, it should replace itself
	// with a degenerate, immortal, upgrade-steps-complete worker; and practically
	// everything should depend upon upgrade-steps-complete.
	// TODO(fwereade): 2015-04-21
	// It's not clear why we don't run the upgrade-steps worker for the unit...
	UpgradeStepsWorkerName   = "upgrade-steps-worker"   //                        <inline>
	UpgradeStepsCompleteName = "upgrade-steps-complete" //                        <missing>

	// This manifold should be run on every machine, and it somehow makes us
	// able to provision containers.
	// TODO(fwereade): this is terribly confused, and would adhere better to a
	// structure in which each container type started a worker that:
	//   * sets support for that worker, and exits nil if there's no support
	//   * replace itself with a provisioner for that container type, with the
	//     relevant instance broker set up to initialize itself lazily
	ContainerSetupHandlerName = "container-setup-handler" //                      worker/provisioner
	//                                                                            <inline>
	//                                                                            <unknown other gubbins>

	// It's not immediately clear when we run this; there's a bunch of inline
	// code that I couldn't immediately parse. Among other things, it depends
	// on JobManageNetworking...
	NetworkerName = "networker" //                                                worker/networker

	// This manifold should be run only when *not* running JobManageEnviron.
	// NOTE: JESPromotionHandler seems pretty awkward. Better to have a manifold
	// per job, enabled/disabled by a simple watcher-worker, and to put the
	// dependencies direct in the manifold where they belong...
	JESPromotionHandlerName = "jes-promotion-handler" //                          worker/conv2state (eww!!)

	// This manifold should be run only when *not* the bootstrap machine for
	// a *local* provider.
	// TODO(fwereade): 2015-04-21
	// This can be determined ahead of time, and should be set in agent config
	// from the very beginning.
	SSHAuthorizedKeysUpdaterName = "ssh-authorized-keys-updater" //               worker/authenticationworker

	// TODO(fwereade): 2015-04-21
	// This manifold should really not be run at all: when clearing up the agent
	// for a machine we can't decommission (ie a manual machine) we should ssh in
	// and clean it up manually, instead of triggering this terrifying suicide path.
	TerrifyinglyExtremeSuiciderName = "termination-worker" //                     worker/terminationworker
)

func Manifolds() dependency.Manifolds {
	return dependency.Manifolds{
	// ...erk.
	}
}
