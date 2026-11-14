// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
)

// Option configures a PostgreSQL connection.
type Option func(*pgx.ConnConfig) error

// WithSearchPath restricts name resolution to the supplied schemas.
func WithSearchPath(schemas ...string) Option {
	return func(config *pgx.ConnConfig) error {
		if len(schemas) == 0 {
			return errors.New("at least one search-path schema is required")
		}
		quoted := make([]string, len(schemas))
		for i, name := range schemas {
			if name == "" || strings.ContainsRune(name, 0) {
				return errors.New("invalid search-path schema name")
			}
			quoted[i] = pgx.Identifier{name}.Sanitize()
		}
		config.RuntimeParams["search_path"] = strings.Join(quoted, ", ")
		return nil
	}
}

// Open returns a database using serializable transactions and UTC timestamps.
// SQL is passed through unchanged except for positional and Sqlair parameters.
// Native PostgreSQL numbered parameters are accepted, but cannot be mixed
// with positional or Sqlair parameters in the same statement.
func Open(url string, options ...Option) (*sql.DB, error) {
	connector, err := NewConnector(url, options...)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}

// NewConnector accepts PostgreSQL URLs and keyword/value connection strings.
func NewConnector(url string, options ...Option) (driver.Connector, error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		// Malformed connection strings cannot always be safely redacted by pgx.
		return nil, errors.New("invalid PostgreSQL connection configuration (details omitted to protect credentials)")
	}
	config.RuntimeParams["default_transaction_isolation"] = "serializable"
	config.RuntimeParams["timezone"] = "UTC"
	for _, option := range options {
		if err := option(config); err != nil {
			return nil, fmt.Errorf("configuring PostgreSQL connection: %w", err)
		}
	}
	return &connector{
		inner: stdlib.GetConnector(*config, stdlib.OptionAfterConnect(scanTimesInUTC)),
	}, nil
}

func scanTimesInUTC(_ context.Context, conn *pgx.Conn) error {
	conn.TypeMap().RegisterType(&pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	})
	return nil
}

type connector struct {
	inner driver.Connector
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	inner, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: inner.(*stdlib.Conn)}, nil
}

func (c *connector) Driver() driver.Driver {
	return pgDriver{}
}

type pgDriver struct{}

func (pgDriver) Open(url string) (driver.Conn, error) {
	connector, err := NewConnector(url)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

type conn struct {
	*stdlib.Conn
}

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	query, params, err := rewrite(query)
	if err != nil {
		return nil, err
	}
	inner, err := c.Conn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &stmt{Stmt: inner, params: params}, nil
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	query, params, err := rewrite(query)
	if err != nil {
		return nil, err
	}
	args, err = bind(params, args)
	if err != nil {
		return nil, err
	}
	return c.Conn.ExecContext(ctx, query, args)
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	query, params, err := rewrite(query)
	if err != nil {
		return nil, err
	}
	args, err = bind(params, args)
	if err != nil {
		return nil, err
	}
	return c.Conn.QueryContext(ctx, query, args)
}

type stmt struct {
	driver.Stmt
	params []param
}

func (s *stmt) NumInput() int {
	if s.params == nil {
		return s.Stmt.NumInput()
	}
	return len(s.params)
}

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), namedValues(args))
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), namedValues(args))
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	args, err := bind(s.params, args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	args, err := bind(s.params, args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}

func namedValues(args []driver.Value) []driver.NamedValue {
	result := make([]driver.NamedValue, len(args))
	for i, value := range args {
		result[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return result
}

func bind(params []param, args []driver.NamedValue) ([]driver.NamedValue, error) {
	if params == nil {
		return args, nil
	}
	if len(params) != len(args) {
		return nil, fmt.Errorf("query has %d parameters but %d arguments were supplied", len(params), len(args))
	}
	named := make(map[string]driver.Value, len(args))
	var positional []driver.Value
	for _, arg := range args {
		if arg.Name == "" {
			positional = append(positional, arg.Value)
			continue
		}
		if _, exists := named[arg.Name]; exists {
			return nil, fmt.Errorf("duplicate argument for named parameter %q", arg.Name)
		}
		named[arg.Name] = arg.Value
	}
	result := make([]driver.NamedValue, len(params))
	for i, param := range params {
		var value driver.Value
		if param.name != "" {
			var ok bool
			value, ok = named[param.name]
			if !ok {
				return nil, fmt.Errorf("missing argument for named parameter %q", param.name)
			}
		} else {
			if param.position > len(positional) {
				return nil, fmt.Errorf("missing argument for positional parameter %d", param.position)
			}
			value = positional[param.position-1]
		}
		result[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return result, nil
}
