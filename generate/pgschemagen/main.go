// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/juju/juju/core/version"
	"github.com/juju/juju/domain/schema"
)

func main() {
	dqliteDir := flag.String("dqlite", "./domain/schema/dqlite", "authoritative dqlite schema directory")
	destDir := flag.String("dest", "./domain/schema/postgresql", "generated PostgreSQL schema directory")
	check := flag.Bool("check", false, "compare contents and file sets without writing")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "pgschemagen: unexpected positional arguments")
		os.Exit(1)
	}
	namespaces := []namespace{{
		name:           "controller",
		sources:        schema.ControllerPatchSourcesForVersion(version.Current),
		postPatchFiles: schema.ControllerPostPatchFiles(),
	}, {
		name:           "model",
		sources:        schema.ModelPatchSourcesForVersion(version.Current),
		postPatchFiles: schema.ModelPostPatchFiles(),
	}}
	if err := run(namespaces, *dqliteDir, *destDir, *check); err != nil {
		fmt.Fprintln(os.Stderr, "pgschemagen:", err)
		os.Exit(1)
	}
}
