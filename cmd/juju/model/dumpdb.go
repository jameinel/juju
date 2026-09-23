// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package model

import (
	"fmt"

	"github.com/juju/cmd/v3"
	"github.com/juju/errors"
	"github.com/juju/gnuflag"
	"github.com/juju/names/v5"

	jujucmd "github.com/juju/juju/cmd"
	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/cmd/output"
)

// NewDumpDBCommand returns a fully constructed dump-db command.
func NewDumpDBCommand() cmd.Command {
	return modelcmd.Wrap(&dumpDBCommand{})
}

type dumpDBCommand struct {
	modelcmd.ModelCommandBase
	out                    cmd.Output
	api                    DumpDBAPI
	sanitize               bool
	keepLowSensitivityData bool
}

const dumpDBHelpDoc = `
dump-db returns all that is stored in the database for the specified model.

Use --sanitize to redact known sensitive fields before sharing the output.
This is a best-effort redaction; review the dump before sharing it.
Use --keep-low-sensitivity-data with --sanitize to retain action logs and
status information.

Examples:

    juju dump-db
    juju dump-db -m mymodel

See also:
    models
`

// Info implements Command.
func (c *dumpDBCommand) Info() *cmd.Info {
	return jujucmd.Info(&cmd.Info{
		Name:    "dump-db",
		Purpose: "Displays the mongo documents for of the model.",
		Doc:     dumpDBHelpDoc,
	})
}

// SetFlags implements Command.
func (c *dumpDBCommand) SetFlags(f *gnuflag.FlagSet) {
	c.ModelCommandBase.SetFlags(f)
	c.out.AddFlags(f, "yaml", output.DefaultFormatters)
	f.BoolVar(&c.sanitize, "sanitize", false, "redact known sensitive fields from the dump")
	f.BoolVar(&c.keepLowSensitivityData, "keep-low-sensitivity-data", false, "retain action logs and status information when sanitizing")
}

// Init implements Command.
func (c *dumpDBCommand) Init(args []string) error {
	if c.keepLowSensitivityData && !c.sanitize {
		return errors.New("--keep-low-sensitivity-data requires --sanitize")
	}
	return cmd.CheckEmpty(args)
}

// These fields mirror scripts/sanitise-db.py. Unlike that script, dump-db
// contains only the current model's documents, not the global txn collection.
var dumpDBSensitiveFields = map[string][]string{
	"users":              {"secretkey", "passwordhash", "passwordsalt"},
	"units":              {"passwordhash"},
	"machines":           {"passwordhash"},
	"applications":       {"metric-credentials", "passwordhash"},
	"models":             {"passwordhash", "sla"},
	"controllerNodes":    {"password-hash"},
	"settings":           {"settings"},
	"controllers":        {"settings", "cert", "privatekey", "caprivatekey", "sharedsecret", "systemidentity", "key", "local-users-key", "local-users-thirdparty-key", "external-users-thirdparty-key", "offers-thirdparty-key"},
	"actions":            {"parameters", "message", "results"},
	"cloudCredentials":   {"attributes"},
	"dockerResources":    {"password"},
	"sshrequests":        {"password"},
	"virtualhostkeys":    {"hostkey"},
	"autocertCache":      {"data"},
	"bakeryStorageItems": {"rootkey", "item"},
	"remoteEntities":     {"token", "macaroon"},
	"remoteApplications": {"macaroon"},
	"migrations":         {"target-password", "target-macaroons", "target-token"},
	"secretRevisions":    {"data"},
	"secretBackends":     {"config"},
}

var dumpDBLowSensitivityFields = map[string][]string{
	"actions":         {"messages"},
	"statuses":        {"statusinfo", "statusdata"},
	"statuseshistory": {"statusinfo", "statusdata"},
}

// DumpDBAPI specifies the used function calls of the ModelManager.
type DumpDBAPI interface {
	Close() error
	DumpModelDB(names.ModelTag) (map[string]interface{}, error)
}

func (c *dumpDBCommand) getAPI() (DumpDBAPI, error) {
	if c.api != nil {
		return c.api, nil
	}
	return c.ModelCommandBase.NewModelManagerAPIClient()
}

// Run implements Command.
func (c *dumpDBCommand) Run(ctx *cmd.Context) error {
	client, err := c.getAPI()
	if err != nil {
		return errors.Trace(err)
	}
	defer client.Close()

	_, modelDetails, err := c.ModelCommandBase.ModelDetails()
	if err != nil {
		return errors.Annotate(err, "getting model details")
	}

	modelTag := names.NewModelTag(modelDetails.ModelUUID)
	results, err := client.DumpModelDB(modelTag)
	if err != nil {
		return err
	}
	if c.sanitize {
		if err := sanitizeDBDump(results, !c.keepLowSensitivityData); err != nil {
			return errors.Trace(err)
		}
	}

	return c.out.Write(ctx, results)
}

// sanitizeDBDump redacts only fields present in each document, preserving the
// rest of the dump (including document identifiers) for diagnostics.
func sanitizeDBDump(dump map[string]interface{}, includeLowSensitivity bool) error {
	for collection, value := range dump {
		fields := dumpDBSensitiveFields[collection]
		if includeLowSensitivity {
			fields = append(append([]string(nil), fields...), dumpDBLowSensitivityFields[collection]...)
		}
		if len(fields) == 0 {
			continue
		}
		redact := func(doc map[string]interface{}) {
			for _, field := range fields {
				if _, ok := doc[field]; ok {
					doc[field] = "REDACTED"
				}
			}
		}
		switch docs := value.(type) {
		case map[string]interface{}:
			redact(docs) // models is a single document.
		case []interface{}:
			for _, value := range docs {
				doc, ok := value.(map[string]interface{})
				if !ok {
					return fmt.Errorf("unexpected document in %q collection: %T", collection, value)
				}
				redact(doc)
			}
		case []map[string]interface{}:
			for _, doc := range docs {
				redact(doc)
			}
		default:
			return fmt.Errorf("unexpected %q collection: %T", collection, value)
		}
	}
	return nil
}
