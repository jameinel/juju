// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package all

import (
	"reflect"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/names"

	"github.com/juju/juju/api/base"
	jujuServerClient "github.com/juju/juju/apiserver/client"
	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/process"
	"github.com/juju/juju/process/api/client"
	"github.com/juju/juju/process/api/server"
	"github.com/juju/juju/process/context"
	"github.com/juju/juju/process/plugin"
	procstate "github.com/juju/juju/process/state"
	"github.com/juju/juju/state"
	"github.com/juju/juju/worker/uniter/runner"
	"github.com/juju/juju/worker/uniter/runner/jujuc"
)

type workloadProcesses struct{}

func (c workloadProcesses) registerForServer() error {
	c.registerHookContext()
	c.registerState()
	return nil
}

func (c workloadProcesses) registerForClient() error {
	return nil
}

func (c workloadProcesses) registerHookContext() {
	if !markRegistered(process.ComponentName, "hook-context") {
		return
	}

	runner.RegisterComponentFunc(process.ComponentName,
		func(caller base.APICaller) (jujuc.ContextComponent, error) {
			facadeCaller := base.NewFacadeCallerForVersion(caller, process.ComponentName, 0)
			hctxClient := client.NewHookContextClient(facadeCaller)
			// TODO(ericsnow) Pass the unit's tag through to the component?
			component, err := context.NewContextAPI(hctxClient)
			if err != nil {
				return nil, errors.Trace(err)
			}
			return component, nil
		},
	)

	c.registerHookContextCommands()
	c.registerHookContextFacade()
}

func (c workloadProcesses) registerHookContextFacade() {

	newHookContextApi := func(st *state.State, unit *state.Unit) (interface{}, error) {
		if st == nil {
			return nil, errors.NewNotValid(nil, "st is nil")
		}

		up, err := st.UnitProcesses(unit.UnitTag())
		if err != nil {
			return nil, errors.Trace(err)
		}
		return server.NewHookContextAPI(up), nil
	}

	common.RegisterHookContextFacade(
		process.ComponentName,
		0,
		newHookContextApi,
		reflect.TypeOf(&server.HookContextAPI{}),
	)
}

type workloadProcessesHookContext struct {
	jujuc.Context
}

// Component implements context.HookContext.
func (c workloadProcessesHookContext) Component(name string) (context.Component, error) {
	found, err := c.Context.Component(name)
	if err != nil {
		return nil, errors.Trace(err)
	}
	compCtx, ok := found.(context.Component)
	if !ok && found != nil {
		return nil, errors.Errorf("wrong component context type registered: %T", found)
	}
	return compCtx, nil
}

func (workloadProcesses) registerHookContextCommands() {
	if !markRegistered(process.ComponentName, "hook-context-commands") {
		return
	}

	jujuc.RegisterCommand("register", func(ctx jujuc.Context) cmd.Command {
		compCtx := workloadProcessesHookContext{ctx}
		cmd, err := context.NewProcRegistrationCommand(compCtx)
		if err != nil {
			// TODO(ericsnow) Return an error instead.
			panic(err)
		}
		return cmd
	})

	jujuc.RegisterCommand("launch", func(ctx jujuc.Context) cmd.Command {
		compCtx := workloadProcessesHookContext{ctx}
		cmd, err := context.NewProcLaunchCommand(plugin.Find, plugin.Plugin.Launch, compCtx)
		if err != nil {
			// TODO(ericsnow) Return an error instead.
			panic(err)
		}
		return cmd
	})
}

func (c workloadProcesses) registerState() {
	// TODO(ericsnow) Use a more general registration mechanism.
	//state.RegisterMultiEnvCollections(persistence.Collections...)

	newUnitProcesses := func(persist state.Persistence, unit names.UnitTag) (state.UnitProcesses, error) {
		return procstate.NewUnitProcesses(persist, unit), nil
	}
	state.SetProcessesComponent(newUnitProcesses)
}

func (c workloadProcesses) registerStatusAPI() {

	statusAdapter := func(st *state.State, unitTag names.UnitTag) (map[string]string, error) {
		unitTagToProcessList := func(unitTag names.UnitTag) ([]process.Info, error) {
			unitProcesses, err := st.UnitProcesses(unitTag)
			if err != nil {
				return nil, err
			}

			return unitProcesses.List()
		}

		return server.BuildStatus(unitTagToProcessList, unitTag)
	}

	jujuServerClient.RegisterStatusProviderForUnits(server.StatusType, statusAdapter)

	// TODO: uncomment when we move the status stuff out of the cmd/juju/commands package
	// since this would cause an import cycle.
	//commands.RegisterUnitComponentFormatter("processes", convertAPItoCLI)
}

// for now, since we're just using a map[string]string for our status, we can
// just return that map.  If we change the status to be strongly typed, we'd do
// the adaptation here.
func convertAPItoCLI(apiObj interface{}) (cliObj interface{}) {
	return apiObj
}
