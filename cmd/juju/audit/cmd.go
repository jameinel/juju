// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.
package audit

/*
The initial specs for the feature this command is associated with can be found
at https://github.com/CanonicalLtd/juju-specs/tree/master/audit-log
*/

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/juju/cmd/modelcmd"
	"launchpad.net/gnuflag"
)

const usageSummary = "Output audit messages for the given model."

var usageDetails = `
TODO(redir): Figure out what the usage details are: examples?, querying, filtering.
`[1:]

// APIClientBase represents the api client connection method for testing.
type APIClientBase interface {
	Connect(*cmd.Context) (LogLister, error)
}

// LogLister represents the audit api client interface for testing.
type LogLister interface {
	QueryResults([]LogFilter) ([]auditRecord, error)

	// Do we need this or is this a feature solely of the charmstore?
	Close() error
}

// NewAuditCommand shockingly returns a command to access a models audit log.
func NewAuditCommand(c APIClientBase) cmd.Command {
	return &auditCommand{APIClientBase: c}
}

type auditCommand struct {
	modelcmd.ModelCommandBase
	out cmd.Output

	APIClientBase

	CIDRBlock  string
	ModelName  string
	Operation  string
	OriginName string
	OriginType string
	Timestamp  string
}

// LogFilter holds a field and value to filter on.
type LogFilter struct {
	Field, Value string
}

// Info returns a pointer to a cmd.Info struct with auditCommand appropriate
// details.
func (c *auditCommand) Info() *cmd.Info {
	return &cmd.Info{
		Args:    "[options] <model name>",
		Name:    "audit",
		Purpose: usageSummary,
		Doc:     usageDetails,
	}
}

// Init implements cmd.Command, here ensuring we haven't received any
// unexpected positional arguments.
func (c *auditCommand) Init(args []string) error {
	if len(args) == 0 {
		return errors.New("missing model name")
	}
	c.ModelName = args[0]

	if err := cmd.CheckEmpty(args[1:]); err != nil {
		return errors.Trace(err)
	}
	return nil
}

// SetFlags implements cmd.Command, adding flags appropriate for auditCommand.
func (c *auditCommand) SetFlags(f *gnuflag.FlagSet) {
	c.out.AddFlags(f, "tabular", map[string]cmd.Formatter{
		"tabular": formatTabular,
		"yaml":    cmd.FormatYaml,
		"json":    cmd.FormatJson,
	})
	f.StringVar(&c.CIDRBlock, "ip", "0.0.0.0/0", "A CIDR block to filter on.")
	f.StringVar(&c.Operation, "operation", "", `The actual operation performed, e.g. "status"`)
	f.StringVar(&c.OriginName, "origin-name", "", "The name of the origin to filter on. E.g. model name, user name, action name")
	f.StringVar(&c.OriginType, "origin-type", "", "The type of origin to filter on. I.e. model, user, or action.")
	f.StringVar(&c.Timestamp, "timestamp", "", "A discrete timestamp with variable resolution filter. A range may also be specified via a dash symbol.")
}

// Run implements cmd.Command, executing the command.
func (c *auditCommand) Run(ctx *cmd.Context) error {
	apiClient, err := c.Connect(ctx)
	if err != nil {
		return errors.Trace(err)
	}
	defer func() {
		err = apiClient.Close()
		if err != nil {
			// TODO(redir): Do we care about this error?
		}
	}()

	// TODO(redir): Get the actual filters from args. s.filtersFromArgs() ([]LogFilter, error)
	records, err := apiClient.QueryResults([]LogFilter{})
	if err != nil {
		return errors.Trace(err)
	}
	return c.out.Write(ctx, records)
}

// formatTabular returns []bytes formatted as a tab separated table for
// cmd.Output to render appropriately.
func formatTabular(val interface{}) ([]byte, error) {
	records, ok := val.([]auditRecord)
	if !ok {
		return nil, errors.Errorf("expected value of type %T, got %T", records, val)
	}

	var out bytes.Buffer

	const (
		minwidth = 0
		tabwidth = 1
		padding  = 2
		padchar  = ' '
		flags    = 0
	)
	w := tabwriter.NewWriter(&out, minwidth, tabwidth, padchar, padchar, flags)
	fmt.Fprintf(&out, "DATE\tIP\tSOURCE\tOPERATION\n")
	for _, rec := range records {
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s\n", rec.Timestamp.String(), rec.IPAddress, strings.Join([]string{rec.Type, rec.Name}, ":"), rec.Operation)
	}
	err := w.Flush()
	if err != nil {
		return nil, errors.Errorf("failed to flush tabwriter buffer: %s", err)
	}
	return out.Bytes(), nil
}

// The following items probably implemented elsewhere and then used here?

// auditRecord represents the audit logs data model.
// TODO(redir): Do we want omitempty on these? Can any ever be empty?
type auditRecord struct {
	IPAddress net.IP    `json:"ipAddress"`
	ModelID   string    `json:"modelID"`
	Name      string    `json:"name"`
	Operation string    `json:"operation"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
}
