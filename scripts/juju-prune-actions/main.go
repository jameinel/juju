// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Build from the Juju source version matching the controller:
//
//	go build -o juju-prune-actions ./scripts/juju-prune-actions
//
// Run on a controller machine with access to its agent.conf:
//
//	sudo ./juju-prune-actions <model-uuid>
//
// Use -agent-config when more than one machine agent config is present.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/juju/names/v5"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/api"
	"github.com/juju/juju/api/client/action"
)

func main() {
	agentConfig := flag.String("agent-config", "", "path to the controller machine's agent.conf (discovered under /var/lib/juju/agents by default)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-agent-config path] <model-uuid>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 || !names.IsValidModel(flag.Arg(0)) {
		flag.Usage()
		os.Exit(2)
	}

	if err := prune(*agentConfig, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func prune(configPath, modelUUID string) error {
	if configPath == "" {
		matches, err := filepath.Glob("/var/lib/juju/agents/machine-*/agent.conf")
		if err != nil {
			return fmt.Errorf("finding controller agent config: %w", err)
		}
		if len(matches) != 1 {
			return fmt.Errorf("expected one machine agent config, found %d; specify -agent-config", len(matches))
		}
		configPath = matches[0]
	}

	conf, err := agent.ReadConfig(configPath)
	if err != nil {
		return fmt.Errorf("reading agent config: %w", err)
	}
	info, ok := conf.APIInfo()
	if !ok {
		return fmt.Errorf("agent config has no API connection information")
	}
	info.ModelTag = names.NewModelTag(modelUUID)

	conn, err := api.Open(info, api.DialOpts{Timeout: 15 * time.Second})
	if err != nil {
		return fmt.Errorf("connecting as controller agent: %w", err)
	}
	defer conn.Close()

	pruner := action.NewPruner(conn)
	cfg, err := pruner.ModelConfig()
	if err != nil {
		return fmt.Errorf("reading model config: %w", err)
	}
	age, sizeMB := cfg.MaxActionResultsAge(), cfg.MaxActionResultsSizeMB()
	fmt.Printf("Pruning actions for model %s (max age %s, max collection size %d MiB)\n",
		modelUUID, age, sizeMB)
	if err := pruner.Prune(age, int(sizeMB)); err != nil {
		return fmt.Errorf("pruning actions: %w", err)
	}
	fmt.Println("Action pruning completed")
	return nil
}
