// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package postgresql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

// DriverName is the name the driver is registered under with database/sql.
// Most callers should use Open or NewConnector rather than sql.Open.
const DriverName = "juju-postgresql"

// IsolationLevel is the transaction isolation level applied to every
// connection, as a server setting. See the SERIALIZABLE decision (D7) in the
// PostgreSQL proof-of-concept plan.
const IsolationLevel = "serializable"

func init() {
	sql.Register(DriverName, &pgDriver{})
}

// Option customises the connection configuration. Options are applied after
// the URL is parsed.
type Option func(*pgx.ConnConfig) error

// WithSearchPath sets the schemas in the connection's search_path, in order.
// Each name is quoted, so names such as UUIDs with hyphens are safe.
func WithSearchPath(schemas ...string) Option {
	return func(cfg *pgx.ConnConfig) error {
		quoted := make([]string, len(schemas))
		for i, s := range schemas {
			quoted[i] = QuoteIdentifier(s)
		}
		cfg.RuntimeParams["search_path"] = strings.Join(quoted, ", ")
		return nil
	}
}

// WithTimeZoneUTC configures connections to interpret zone-less timestamps as
// UTC.
func WithTimeZoneUTC() Option {
	return func(cfg *pgx.ConnConfig) error {
		cfg.RuntimeParams["timezone"] = "UTC"
		return nil
	}
}

// WithRootCAs verifies the server certificate against the PEM CA
// certificates, held in memory rather than in an sslrootcert file. An empty
// PEM is a no-op. The URL must use sslmode=verify-ca or sslmode=verify-full:
// the other modes either skip verification or allow plaintext, so a CA would
// silently be ignored.
func WithRootCAs(caCertPEM string) Option {
	return func(cfg *pgx.ConnConfig) error {
		if caCertPEM == "" {
			return nil
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
			return errors.New("parsing database CA certificate: no PEM certificates found")
		}

		tlsConfigs := []*tls.Config{cfg.TLSConfig}
		for _, fallback := range cfg.Fallbacks {
			tlsConfigs = append(tlsConfigs, fallback.TLSConfig)
		}
		for _, tlsConfig := range tlsConfigs {
			// A nil config is a plaintext attempt. A config that skips
			// verification without a verify callback does not verify.
			if tlsConfig == nil ||
				(tlsConfig.InsecureSkipVerify && tlsConfig.VerifyPeerCertificate == nil) {
				return errors.New("a database CA certificate requires sslmode=verify-ca or sslmode=verify-full")
			}
		}
		for _, tlsConfig := range tlsConfigs {
			// Update in place: pgx's verify-ca callback reads RootCAs from
			// this config when the connection is made.
			roots := pool
			if tlsConfig.RootCAs != nil {
				roots = tlsConfig.RootCAs.Clone()
				roots.AppendCertsFromPEM([]byte(caCertPEM))
			}
			tlsConfig.RootCAs = roots
		}
		return nil
	}
}

// NewConnector returns a driver.Connector for the PostgreSQL URL (or key/value
// DSN) that rewrites sqlair and "?" placeholders and runs with
// default_transaction_isolation=serializable.
//
// The session role is never reset by this driver. The PostgreSQL charm
// switches the login user to its owner role at login, and that is lost by
// RESET ROLE and DISCARD ALL.
func NewConnector(url string, opts ...Option) (driver.Connector, error) {
	cfg, err := parseConfig(url, opts...)
	if err != nil {
		return nil, err
	}
	return &connector{
		inner: stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(scanTimesInUTC)),
	}, nil
}

// scanTimesInUTC makes the connection return timestamptz values in UTC rather
// than in time.Local, as the dqlite driver does.
func scanTimesInUTC(_ context.Context, c *pgx.Conn) error {
	c.TypeMap().RegisterType(&pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	})
	return nil
}

// Open returns a *sql.DB that uses the driver for the given PostgreSQL URL.
func Open(url string, opts ...Option) (*sql.DB, error) {
	c, err := NewConnector(url, opts...)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(c), nil
}

func parseConfig(url string, opts ...Option) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		// The pgx error is already redacted of the password.
		return nil, fmt.Errorf("parsing postgresql connection config: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["default_transaction_isolation"] = IsolationLevel
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("configuring postgresql connection: %w", err)
		}
	}
	return cfg, nil
}

// QuoteIdentifier quotes a PostgreSQL identifier.
func QuoteIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

type pgDriver struct{}

// Open implements driver.Driver.
func (d *pgDriver) Open(name string) (driver.Conn, error) {
	c, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}

// OpenConnector implements driver.DriverContext.
func (d *pgDriver) OpenConnector(name string) (driver.Connector, error) {
	return NewConnector(name)
}

type connector struct {
	inner driver.Connector
}

// Connect implements driver.Connector.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	inner, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{Conn: inner.(*stdlib.Conn)}, nil
}

// Driver implements driver.Connector.
func (c *connector) Driver() driver.Driver {
	return &pgDriver{}
}

// conn wraps a pgx stdlib connection, rewriting placeholders in queries.
// Everything else (Close, Ping, BeginTx, ResetSession, CheckNamedValue) is
// promoted from the pgx connection.
type conn struct {
	*stdlib.Conn
}

// Prepare implements driver.Conn.
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// PrepareContext implements driver.ConnPrepareContext.
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	rewritten, params := rewrite(query)
	inner, err := c.Conn.PrepareContext(ctx, rewritten)
	if err != nil {
		return nil, err
	}
	return &stmt{Stmt: inner, params: params}, nil
}

// ExecContext implements driver.ExecerContext.
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	rewritten, params := rewrite(query)
	bound, err := bind(params, args)
	if err != nil {
		return nil, err
	}
	return c.Conn.ExecContext(ctx, rewritten, bound)
}

// QueryContext implements driver.QueryerContext.
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rewritten, params := rewrite(query)
	bound, err := bind(params, args)
	if err != nil {
		return nil, err
	}
	return c.Conn.QueryContext(ctx, rewritten, bound)
}

// stmt wraps a pgx prepared statement, mapping arguments onto the rewritten
// query's parameters.
type stmt struct {
	driver.Stmt
	params []param
}

// NumInput implements driver.Stmt.
func (s *stmt) NumInput() int {
	if s.params == nil {
		return s.Stmt.NumInput()
	}
	return len(s.params)
}

// Exec implements driver.Stmt.
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(args))
}

// Query implements driver.Stmt.
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(args))
}

// ExecContext implements driver.StmtExecContext.
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	bound, err := bind(s.params, args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, bound)
}

// QueryContext implements driver.StmtQueryContext.
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	bound, err := bind(s.params, args)
	if err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, bound)
}

func valuesToNamed(args []driver.Value) []driver.NamedValue {
	named := make([]driver.NamedValue, len(args))
	for i, v := range args {
		named[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return named
}

// bind arranges args to match the $N order of a rewritten query. Named
// arguments are matched by name and "?" arguments by their order among the
// unnamed arguments. With no params the query was not rewritten and args are
// passed through untouched.
func bind(params []param, args []driver.NamedValue) ([]driver.NamedValue, error) {
	if params == nil {
		return args, nil
	}
	if len(params) != len(args) {
		return nil, fmt.Errorf("query has %d parameters but %d arguments were supplied", len(params), len(args))
	}

	named := make(map[string]driver.Value, len(args))
	var positional []driver.Value
	for _, a := range args {
		if a.Name != "" {
			named[a.Name] = a.Value
		} else {
			positional = append(positional, a.Value)
		}
	}

	bound := make([]driver.NamedValue, len(params))
	for i, p := range params {
		var v driver.Value
		if p.name != "" {
			var ok bool
			if v, ok = named[p.name]; !ok {
				return nil, fmt.Errorf("missing argument for named parameter %q", p.name)
			}
		} else {
			if p.position > len(positional) {
				return nil, fmt.Errorf("missing argument for positional parameter %d", p.position)
			}
			v = positional[p.position-1]
		}
		bound[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return bound, nil
}
