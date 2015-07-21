// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package context

import (
	"path/filepath"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"gopkg.in/juju/charm.v5"
	"launchpad.net/gnuflag"

	"github.com/juju/juju/process"
)

// TODO(ericsnow) How to convert endpoints (charm.Process.Ports[].Name)
// into actual ports? For now we should error out with such definitions
// (and recommend overriding).

// baseCommand implements the common portions of the workload process
// hook env commands.
type baseCommand struct {
	cmd.CommandBase

	ctx     HookContext
	compCtx Component

	// Name is the name of the process in charm metadata.
	Name string
	// info is the process info for the named workload process.
	info *process.Info
	// ReadMetadata extracts charm metadata from the given file.
	ReadMetadata func(filename string) (*charm.Meta, error)
}

func newCommand(ctx HookContext) (*baseCommand, error) {
	compCtx, err := ContextComponent(ctx)
	if err != nil {
		// The component wasn't registered properly.
		return nil, errors.Trace(err)
	}
	return &baseCommand{
		ctx:          ctx,
		compCtx:      compCtx,
		ReadMetadata: readMetadata,
	}, nil
}

// Init implements cmd.Command.
func (c *baseCommand) Init(args []string) error {
	if len(args) == 0 {
		return errors.Errorf("missing process name")
	}
	return errors.Trace(c.init(args[0]))
}

func (c *baseCommand) init(name string) error {
	if name == "" {
		return errors.Errorf("got empty name")
	}
	c.Name = name

	// TODO(ericsnow) Pull the definitions from the metadata here...

	pInfo, err := c.compCtx.Get(c.Name)
	if err != nil && !errors.IsNotFound(err) {
		return errors.Trace(err)
	}
	c.info = pInfo

	return nil
}

func (c *baseCommand) defsFromCharm(ctx *cmd.Context) (map[string]charm.Process, error) {
	filename := filepath.Join(ctx.Dir, "metadata.yaml")
	meta, err := c.ReadMetadata(filename)
	if err != nil {
		return nil, errors.Trace(err)
	}
	defMap := make(map[string]charm.Process)
	for _, definition := range meta.Processes {
		// In the case of collision, use the first one.
		if _, ok := defMap[definition.Name]; ok {
			continue
		}
		defMap[definition.Name] = definition
	}
	return defMap, nil
}

func (c *baseCommand) registeredProcs(ids ...string) ([]process.Info, error) {
	if len(ids) == 0 {
		registered, err := c.compCtx.List()
		if err != nil {
			return nil, errors.Trace(err)
		}
		if len(registered) == 0 {
			return nil, nil
		}
		ids = registered
	}

	var procs []process.Info
	for _, id := range ids {
		proc, err := c.compCtx.Get(id)
		if errors.IsNotFound(err) {
			// This is an inconsequential race so we ignore it.
			continue
		}
		if err != nil {
			return nil, errors.Trace(err)
		}
		procs = append(procs, *proc)
	}
	return procs, nil
}

// registeringCommand is the base for commands that register a process
// that has been launched.
type registeringCommand struct {
	baseCommand

	// Details is the launch details returned from the process plugin.
	Details process.Details

	// Overrides overwrite the process definition.
	Overrides []string

	// Additions extend the process definition.
	Additions []string

	// UpdatedProcess stores the new process, if there were any overrides OR additions.
	UpdatedProcess *charm.Process

	// Definition is the file definition of the process.
	Definition cmd.FileVar
}

func newRegisteringCommand(ctx HookContext) (*registeringCommand, error) {
	base, err := newCommand(ctx)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return &registeringCommand{
		baseCommand: *base,
	}, nil
}

// SetFlags implements cmd.Command.
func (c *registeringCommand) SetFlags(f *gnuflag.FlagSet) {
	f.Var(&c.Definition, "definition", "process definition filename (use \"-\" for STDIN)")
	f.Var(cmd.NewAppendStringsValue(&c.Overrides), "override", "override process definition")
	f.Var(cmd.NewAppendStringsValue(&c.Additions), "extend", "extend process definition")
}

func (c *registeringCommand) init(name string) error {
	if err := c.baseCommand.init(name); err != nil {
		return errors.Trace(err)
	}

	if c.info != nil {
		return errors.Errorf("process %q already registered", c.Name)
	}

	if err := c.checkSpace(); err != nil {
		return errors.Trace(err)
	}

	// Either the named process must already be defined or the command
	// must have been run with the --definition option.
	if c.Definition.Path != "" {
		if c.info != nil {
			return errors.Errorf("process %q already defined", c.Name)
		}
	}

	return nil
}

// register updates the hook context with the information for the
// registered workload process. An error is returned if the process
// was already registered.
func (c *registeringCommand) register(ctx *cmd.Context) error {
	info, err := c.findValidInfo(ctx)
	if err != nil {
		return errors.Trace(err)
	}

	newProcess, err := c.parseUpdates(info.Process)
	if err != nil {
		return errors.Trace(err)
	}
	c.UpdatedProcess = newProcess
	info.Process = *newProcess

	info.Details = c.Details

	if err := c.compCtx.Set(c.Name, info); err != nil {
		return errors.Trace(err)
	}
	// TODO(ericsnow) flush here?
	return nil
}

func (c *registeringCommand) findValidInfo(ctx *cmd.Context) (*process.Info, error) {
	if c.info != nil {
		copied := *c.info
		return &copied, nil
	}

	var definition charm.Process
	if c.Definition.Path == "" {
		defs, err := c.defsFromCharm(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
		charmDef, ok := defs[c.Name]
		if !ok {
			return nil, errors.NotFoundf(c.Name)
		}
		definition = charmDef
	} else {
		// c.info must be nil at this point.
		data, err := c.Definition.Read(ctx)
		if err != nil {
			return nil, errors.Trace(err)
		}
		cliDef, err := parseDefinition(c.Name, data)
		if err != nil {
			return nil, errors.Trace(err)
		}
		definition = *cliDef
	}
	info := &process.Info{Process: definition}
	c.info = info

	// validate
	if err := info.Validate(); err != nil {
		return nil, errors.Trace(err)
	}
	if info.IsRegistered() {
		return nil, errors.Errorf("already registered")
	}
	return info, nil
}

// checkSpace ensures that the requested network space is available
// to the hook.
func (c *registeringCommand) checkSpace() error {
	// TODO(wwitzel3) implement this to ensure that the endpoints provided exist in this space
	return nil
}

func (c *registeringCommand) parseUpdates(definition charm.Process) (*charm.Process, error) {
	overrides, err := parseUpdates(c.Overrides)
	if err != nil {
		return nil, errors.Annotate(err, "override")
	}

	additions, err := parseUpdates(c.Additions)
	if err != nil {
		return nil, errors.Annotate(err, "extend")
	}

	newDefinition, err := definition.Apply(overrides, additions)
	if err != nil {
		return nil, errors.Trace(err)
	}

	return newDefinition, nil
}
