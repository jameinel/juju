// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// pgschemagen generates the PostgreSQL controller and model schemas from
// the dqlite schemas in domain/schema. It translates every dqlite patch,
// including the Go-built triggers, to one PostgreSQL patch file, applies
// any hand-written override diffs, then reorders statements so that foreign
// key targets and view sources exist when they are referenced.
//
// It is run by go generate in domain/schema.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	coreschema "github.com/juju/juju/core/database/schema"
	"github.com/juju/juju/domain/schema"
)

func main() {
	dqliteDir := flag.String("dqlite", "./dqlite", "directory of the dqlite schema")
	destDir := flag.String("dest", "./postgresql", "directory to write the PostgreSQL schema to")
	flag.Parse()

	if err := run(*dqliteDir, *destDir); err != nil {
		fmt.Fprintln(os.Stderr, "pgschemagen:", err)
		os.Exit(1)
	}
}

func run(dqliteDir, destDir string) error {
	for _, ns := range []struct {
		name string
		ddl  *coreschema.Schema
	}{
		{name: "controller", ddl: schema.ControllerDDL()},
		{name: "model", ddl: schema.ModelDDL()},
	} {
		out, err := generateNamespace(ns.name, ns.ddl, dqliteDir, destDir)
		if err != nil {
			return err
		}
		if err := writeOutput(filepath.Join(destDir, ns.name), out); err != nil {
			return err
		}
	}
	return nil
}

// generateNamespace generates the PostgreSQL schema for one namespace,
// without writing it.
func generateNamespace(name string, ddl *coreschema.Schema, dqliteDir, destDir string) (output, error) {
	sqlFiles, err := readSQLFiles(filepath.Join(dqliteDir, name, "sql"))
	if err != nil {
		return nil, err
	}
	overrides, err := readOverrides(filepath.Join(destDir, name, "overrides"))
	if err != nil {
		return nil, err
	}
	var patches []string
	for _, p := range ddl.Patches() {
		patches = append(patches, coreschema.Stmt(p))
	}
	return generate(namespace{name: name, patches: patches, sqlFiles: sqlFiles}, overrides)
}
