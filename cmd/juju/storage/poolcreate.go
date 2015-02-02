// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storage

import (
	"fmt"

	"github.com/juju/cmd"
	"github.com/juju/utils/keyvalues"
	"launchpad.net/gnuflag"
)

const PoolCreateCommandDoc = `
Create or define a storage pool.

A pool describes the underlying characteristics of the storage,
such as the credentials, location of storage and performance or
durability characteristics of that storage
(tagged disks/volume groups in MAAS,
EBS with ssd/magnetic/iops characteristics).

For many providers, there will be a shared resource
where storage can be requested (e.g. EBS in amazon).
Creating pools there maps provider specific settings
into named resources that can be used during deployment.

Pools defined at the environment level are easily reused across services.

options:
    -e, --environment (= "")
        juju environment to operate in
    -o, --output (= "")
        specify an output file
    -t
        pool type
    <name>
        pool name
    [<key>=<value>]*
`

// PoolCreateCommand lists storage pools.
type PoolCreateCommand struct {
	PoolCommandBase
	pname string
	// TODO(anastasiamac 2015-01-29) type will need to become optional
	// if type is unspecified, use the environment's default provider type
	ptype string
	pcfg  map[string]interface{}
}

// Init implements Command.Init.
func (c *PoolCreateCommand) Init(args []string) (err error) {
	if len(args) == 0 {
		return fmt.Errorf("no provider type for pool specified")
	}

	if len(args) == 1 {
		return fmt.Errorf("no pool name specified")
	}

	if len(args) == 2 {
		return fmt.Errorf("no pool config specified")
	}

	c.ptype = args[0]
	c.pname = args[1]

	options, err := keyvalues.Parse(args[2:], true)
	if err != nil {
		return err
	}

	c.pcfg = make(map[string]interface{})
	for key, value := range options {
		c.pcfg[key] = value
	}
	return nil
}

// Info implements Command.Info.
func (c *PoolCreateCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "create-pool",
		Purpose: "create storage pool",
		Doc:     PoolCreateCommandDoc,
	}
}

// SetFlags implements Command.SetFlags.
func (c *PoolCreateCommand) SetFlags(f *gnuflag.FlagSet) {
	c.StorageCommandBase.SetFlags(f)
}

// Run implements Command.Run.
func (c *PoolCreateCommand) Run(ctx *cmd.Context) (err error) {
	api, err := getPoolCreateAPI(c)
	if err != nil {
		return err
	}
	defer api.Close()

	return api.CreatePool(c.pname, c.ptype, c.pcfg)
}

var (
	getPoolCreateAPI = (*PoolCreateCommand).getPoolCreateAPI
)

// PoolCreateAPI defines the API methods that pool create command uses.
type PoolCreateAPI interface {
	Close() error
	CreatePool(pname, ptype string, pconfig map[string]interface{}) error
}

func (c *PoolCreateCommand) getPoolCreateAPI() (PoolCreateAPI, error) {
	return c.NewStorageAPI()
}
