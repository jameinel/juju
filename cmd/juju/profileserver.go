// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"os"
	"os/signal"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"launchpad.net/gnuflag"

	"github.com/juju/juju/api/profile"
	"github.com/juju/juju/cmd/envcmd"
)

type ProfileServerCommand struct {
	envcmd.EnvCommandBase
	outputFile string
}

var profileServerDoc = `
This command will start a CPU profiler on the API server, when you want to stop
profiling send a ^C to signal to the server to stop and download the profile
into the named file.
`

func (c *ProfileServerCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "profile-server",
		Args:    "output",
		Purpose: "profile a running API server",
		Doc:     profileServerDoc,
		Aliases: []string{},
	}
}

func (c *ProfileServerCommand) SetFlags(f *gnuflag.FlagSet) {
}

func (c *ProfileServerCommand) Init(args []string) error {
	if len(args) == 0 {
		return errors.Errorf("must supply an output file")
	}
	c.outputFile = args[0]
	if err := cmd.CheckEmpty(args[1:]); err != nil {
		return err
	}
	return nil
}

// NewProfileClient returns a keymanager client for the root api endpoint
// that the environment command returns.
func (c *ProfileServerCommand) NewProfileClient() (*profile.Client, error) {
	root, err := c.NewAPIRoot()
	if err != nil {
		return nil, err
	}
	return profile.NewClient(root), nil
}

func (c *ProfileServerCommand) Run(ctx *cmd.Context) error {
	client, err := c.NewProfileClient()
	if err != nil {
		return err
	}
	defer client.Close()

	// We open the file early so that we don't fail after doing all that
	// profiling work
	outFile, err := os.Create(c.outputFile)
	if err != nil {
		return err
	}
	defer outFile.Close()
	if err := client.StartCPUProfile(); err != nil {
		return err
	}
	ctx.Stdout.Write([]byte("CPU profiling started. Press ^C to stop.\n"))
	stopCH := make(chan os.Signal, 1)
	signal.Notify(stopCH, os.Interrupt)
	<-stopCH
	signal.Stop(stopCH)
	if result, err := client.StopCPUProfile(); err != nil {
		// TODO: Do we want to delete the file if stop failed?
		return err
	} else {
		outFile.WriteString(result)
	}
	return nil
}
