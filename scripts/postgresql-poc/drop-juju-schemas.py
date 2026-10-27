#!/usr/bin/env python3
# Copyright 2026 Canonical Ltd.
# Licensed under the AGPLv3, see LICENCE file for details.

"""Inspect and drop Juju schemas in the PostgreSQL proof-of-concept database.

Repeated PoC bootstraps and broken test runs leave schemas in the shared
database. This tool lists them and drops the ones you name, so that the
database can be reused. With no options it only lists them.

It recognises these schemas:

  juju_controller          a PoC controller's controller database.
  juju_<model uuid>        a model database of that controller.
  juju_<anything else>     listed as unknown; only --schema selects one.
  juju-test-<pid>-<n>...   a test schema made by the PostgreSQL test harness
                           (internal/database/postgresql/testing). Its
                           comment records the test process and test name.

Every other schema is listed and never dropped.

To drop schemas, give one of:

  --controller UUID        juju_controller and every juju_<model uuid>
                           schema, once juju_controller shows that it
                           belongs to the controller with this UUID.
  --schema NAME            the named juju_* schemas, or one test schema.
                           Repeat it to name several juju_* schemas. Use it
                           when a bootstrap failed before recording the
                           controller.
  --stale-test-schemas     every test schema whose process has gone.

Before dropping, it checks:

  - For juju_* schemas, that every LXD instance, in any project, of the
    database's controller or of any of its models is stopped. A database
    belongs to one controller, so every juju_<model uuid> schema in it
    counts, not only the targets. lxc must work. Database sessions are
    listed, but the server hides their details from this user, so they
    cannot be attributed and do not block.
  - For a test schema, that the process named in the schema has gone from
    this host, or that its PID now belongs to a process started after the
    schema was made. Run the tool on the host that ran the tests.
  - That nothing outside the targets depends on them, such as a view in
    another schema that reads one of their tables.

It then shows the plan and asks you to type "drop"; --yes skips only that
question. Without a terminal and without --yes it refuses. Each target is
dropped with CASCADE in its own transaction, which repeats the dependency
check for the targets not yet dropped. A model schema alone takes about 4,400
locks, and the server's lock table, shared by every session, holds only a
few such schemas. juju_controller goes last, so an interrupted --controller
or --stale-test-schemas run can be repeated as it is; with --schema, name the
schemas that remain.

Nothing runs the tool automatically, for example after a failed bootstrap,
and it has no option to drop every schema: a bootstrap that fails because
juju_controller exists may have found another controller's database.

The database URL is read from JUJU_PG_POC_DB_URL, or else from
~/.config/juju-pg-poc/db-url. psql gets it in PG* environment variables, so
the password never appears in a command line, and it is never printed.

The inventory goes to stdout. When dropping, the plan, status and question go
to stderr, and the names of the dropped schemas to stdout. See
docs/implementation-plans/postgresql/PLAN.md (Stage 1).
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import unquote, urlsplit

URL_ENV = "JUJU_PG_POC_DB_URL"
URL_FILE = Path.home() / ".config" / "juju-pg-poc" / "db-url"
APP_NAME = "drop-juju-schemas"

JUJU_PREFIX = "juju_"
CONTROLLER_SCHEMA = "juju_controller"
TEST_SCHEMA_RE = re.compile(r"^juju-test-([0-9]+)-[0-9]+(-.*)?$")
TEST_COMMENT_RE = re.compile(r"^pid=([0-9]+) started=([0-9]+) test=(.*)$", re.DOTALL)
UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

# A test process records its start time a little after it starts, and the
# boot time used to work out a process's start time can drift.
START_TOLERANCE = 60

CONFIRM_WORD = "drop"
# Longer than the statement timeout below.
PSQL_TIMEOUT = 360
LXC_TIMEOUT = 60

# Timeouts for every session, so that a lock held elsewhere fails the tool
# instead of hanging it. pg_total_relation_size takes a lock on each table.
SESSION_SETUP = """\
SET lock_timeout = '15s';
SET statement_timeout = '5min';
SET client_min_messages = warning;
"""

# libpq connection parameters that psql takes from environment variables.
# The URL's other query parameters are server settings, as for the Go driver.
LIBPQ_ENV = {
    "host": "PGHOST",
    "hostaddr": "PGHOSTADDR",
    "port": "PGPORT",
    "dbname": "PGDATABASE",
    "user": "PGUSER",
    "password": "PGPASSWORD",
    "passfile": "PGPASSFILE",
    "options": "PGOPTIONS",
    "service": "PGSERVICE",
    "connect_timeout": "PGCONNECT_TIMEOUT",
    "client_encoding": "PGCLIENTENCODING",
    "target_session_attrs": "PGTARGETSESSIONATTRS",
    "sslmode": "PGSSLMODE",
    "sslrootcert": "PGSSLROOTCERT",
    "sslcert": "PGSSLCERT",
    "sslkey": "PGSSLKEY",
    "sslcrl": "PGSSLCRL",
    "sslcrldir": "PGSSLCRLDIR",
    "sslsni": "PGSSLSNI",
    "ssl_min_protocol_version": "PGSSLMINPROTOCOLVERSION",
    "ssl_max_protocol_version": "PGSSLMAXPROTOCOLVERSION",
    "channel_binding": "PGCHANNELBINDING",
    "require_auth": "PGREQUIREAUTH",
    "requirepeer": "PGREQUIREPEER",
    "gssencmode": "PGGSSENCMODE",
    "krbsrvname": "PGKRBSRVNAME",
    "gsslib": "PGGSSLIB",
}
# Parameters the tool sets itself.
IGNORED_PARAMS = {"application_name", "fallback_application_name"}


class CleanupError(Exception):
    pass


def log(msg):
    print(msg, file=sys.stderr, flush=True)


def main(argv=None):
    args = parse_args(argv)
    try:
        pg = Postgres(args.psql, read_url())
        inventory = load_inventory(pg)
        if args.controller or args.schema or args.stale_test_schemas:
            drop(pg, inventory, args)
        else:
            lxd = None
            if inventory.lxd_uuids():
                try:
                    lxd = lxd_instances(args.lxc)
                except CleanupError as e:
                    lxd = e
            print_inventory(inventory, lxd)
    except CleanupError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1
    return 0


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    target = parser.add_mutually_exclusive_group()
    target.add_argument(
        "--controller",
        metavar="UUID",
        type=str.lower,
        help="drop the controller and model schemas of this controller",
    )
    target.add_argument(
        "--schema",
        metavar="NAME",
        action="append",
        help="drop this juju_* schema (repeatable) or this one test schema",
    )
    target.add_argument(
        "--stale-test-schemas",
        action="store_true",
        help="drop every test schema whose process has gone",
    )
    parser.add_argument(
        "--yes", action="store_true", help="drop without asking to confirm"
    )
    parser.add_argument(
        "--psql", default="psql", help="psql client to use (default: %(default)s)"
    )
    parser.add_argument(
        "--lxc", default="lxc", help="LXD client to use (default: %(default)s)"
    )
    args = parser.parse_args(argv)
    if args.controller and not UUID_RE.match(args.controller):
        parser.error(f"--controller {args.controller!r} is not a UUID")
    if args.yes and not (args.controller or args.schema or args.stale_test_schemas):
        parser.error("--yes needs --controller, --schema or --stale-test-schemas")
    return args


def read_url():
    url = os.environ.get(URL_ENV, "").strip()
    if url:
        return url
    try:
        url = URL_FILE.read_text().strip()
    except FileNotFoundError:
        raise CleanupError(f"set {URL_ENV} or write the URL to {URL_FILE}") from None
    except OSError as e:
        raise CleanupError(f"reading {URL_FILE}: {e.strerror}") from None
    if not url:
        raise CleanupError(f"{URL_FILE} is empty")
    return url


class Postgres:
    """Runs psql against the database in a URL, without exposing the URL."""

    def __init__(self, psql, url):
        self.psql = psql
        self.env = connection_env(url)
        host = self.env.get("PGHOST") or self.env.get("PGHOSTADDR") or "localhost"
        if ":" in host:
            host = f"[{host}]"
        self.address = f"{host}:{self.env.get('PGPORT', '5432')}"

    def query_json(self, sql, **variables):
        """Runs SQL whose last statement returns one JSON value."""
        out = self.run(sql, **variables).strip()
        try:
            return json.loads(out.splitlines()[-1])
        except (IndexError, ValueError):
            raise CleanupError("psql returned no JSON") from None

    def run(self, sql, timeout=PSQL_TIMEOUT, **variables):
        """Runs SQL in one session and returns psql's output.

        Variables are psql variables; SQL interpolates them as :'name'.
        """
        cmd = [self.psql, "-X", "-q", "-A", "-t", "-w", "-v", "ON_ERROR_STOP=1"]
        for name, value in variables.items():
            cmd += ["-v", f"{name}={value}"]
        try:
            result = subprocess.run(
                cmd,
                check=False,
                input=SESSION_SETUP + sql,
                capture_output=True,
                text=True,
                env=self.env,
                timeout=timeout,
            )
        except FileNotFoundError:
            raise CleanupError(f"psql client {self.psql!r} not found") from None
        except subprocess.TimeoutExpired:
            raise CleanupError(f"psql did not finish in {timeout}s") from None
        if result.returncode != 0:
            raise CleanupError(
                f"psql failed ({result.returncode}): {result.stderr.strip()}"
            )
        return result.stdout


def connection_env(url):
    """Returns an environment that connects psql to the database in a URL.

    No inherited PG* variable is kept, so only the URL picks the database.
    """
    parts = urlsplit(url)
    if parts.scheme not in ("postgres", "postgresql"):
        raise CleanupError("the database URL must start with postgres://")
    userinfo, _, hostport = parts.netloc.rpartition("@")
    if "," in hostport:
        raise CleanupError("the database URL names several hosts; give one")
    try:
        port = parts.port
    except ValueError:
        raise CleanupError("the database URL has an invalid port") from None

    params = {}
    if parts.hostname:
        params["host"] = unquote(parts.hostname)
    if port:
        params["port"] = str(port)
    if userinfo:
        user, has_password, password = userinfo.partition(":")
        if user:
            params["user"] = unquote(user)
        if has_password:
            params["password"] = unquote(password)
    if parts.path.lstrip("/"):
        params["dbname"] = unquote(parts.path.lstrip("/"))

    settings = []
    for item in parts.query.split("&"):
        if not item:
            continue
        name, has_value, value = item.partition("=")
        if not has_value:
            raise CleanupError("the database URL has a query parameter without =")
        name, value = unquote(name), unquote(value)
        if name in IGNORED_PARAMS:
            continue
        if name in LIBPQ_ENV:
            params[name] = value
        else:
            settings.append(pg_option(name, value))
    if settings:
        params["options"] = " ".join(filter(None, [params.get("options"), *settings]))

    env = {k: v for k, v in os.environ.items() if not k.startswith("PG")}
    for name, value in params.items():
        env[LIBPQ_ENV[name]] = value
    env.setdefault("PGCONNECT_TIMEOUT", "10")
    env["PGAPPNAME"] = APP_NAME
    return env


def pg_option(name, value):
    """Returns a PGOPTIONS argument setting a server setting.

    libpq splits PGOPTIONS at spaces; a backslash escapes the next character.
    """
    escaped = re.sub(r"([\\ ])", r"\\\1", f"{name}={value}")
    return f"-c {escaped}"


@dataclass
class TestInfo:
    pid: int
    started: int = None
    test: str = None
    state: str = None

    def stale(self):
        return self.state in ("gone", "reused")


@dataclass
class Schema:
    name: str
    kind: str
    tables: int
    size: int
    known_tables: list
    comment: str = None
    patches: int = None
    model: dict = None
    test: TestInfo = None

    def model_uuid(self):
        return self.name[len(JUJU_PREFIX) :] if self.kind == "model" else None


@dataclass
class Inventory:
    database: str
    host: str
    user: str
    server_version: str
    schemas: list
    sessions: list
    controllers: list = field(default_factory=list)
    models: list = field(default_factory=list)
    namespaces: list = None
    details_error: str = None

    def by_kind(self, *kinds):
        return [s for s in self.schemas if s.kind in kinds]

    def find(self, name):
        return next((s for s in self.schemas if s.name == name), None)

    def lxd_uuids(self):
        """Returns the controller and model UUIDs of the database's schemas."""
        uuids = {c["uuid"] for c in self.controllers}
        for s in self.by_kind("model"):
            uuids.add(s.model_uuid())
            if s.model and s.model.get("controller_uuid"):
                uuids.add(s.model["controller_uuid"])
        return uuids


def schema_kind(name):
    if name == CONTROLLER_SCHEMA:
        return "controller"
    if name.startswith(JUJU_PREFIX):
        return "model" if UUID_RE.match(name[len(JUJU_PREFIX) :]) else "unknown"
    if TEST_SCHEMA_RE.match(name):
        return "test"
    return "other"


CATALOG_SQL = """\
SELECT jsonb_build_object(
    'database', current_database(),
    'user', session_user,
    'server_version', current_setting('server_version'),
    'schemas', (
        SELECT coalesce(jsonb_agg(jsonb_build_object(
            'name', n.nspname,
            'comment', obj_description(n.oid, 'pg_namespace'),
            'tables', (
                SELECT count(*) FROM pg_class c
                WHERE c.relnamespace = n.oid AND c.relkind IN ('r', 'p')
            ),
            'size', (
                SELECT coalesce(sum(pg_total_relation_size(c.oid)), 0)
                FROM pg_class c
                WHERE c.relnamespace = n.oid AND c.relkind IN ('r', 'm')
            ),
            'known_tables', (
                SELECT coalesce(jsonb_agg(c.relname ORDER BY c.relname), '[]')
                FROM pg_class c
                WHERE c.relnamespace = n.oid AND c.relkind IN ('r', 'p')
                AND c.relname IN
                    ('schema', 'controller', 'model', 'life', 'namespace_list')
            )
        ) ORDER BY n.nspname), '[]')
        FROM pg_namespace n
        WHERE NOT starts_with(n.nspname, 'pg_')
        AND n.nspname <> 'information_schema'
    ),
    'sessions', (
        SELECT coalesce(jsonb_agg(jsonb_build_object(
            'pid', a.pid,
            'user', a.usename,
            'application', a.application_name,
            'state', a.state
        ) ORDER BY a.pid), '[]')
        FROM pg_stat_activity a
        WHERE a.datname = current_database()
        AND a.backend_type = 'client backend'
        AND a.pid <> pg_backend_pid()
    )
)::text;
"""


def load_inventory(pg):
    data = pg.query_json(CATALOG_SQL)
    schemas = []
    for row in data["schemas"]:
        s = Schema(
            name=row["name"],
            kind=schema_kind(row["name"]),
            tables=row["tables"],
            size=int(row["size"]),
            known_tables=row["known_tables"],
            comment=row["comment"],
        )
        if s.kind == "test":
            s.test = test_info(s)
        schemas.append(s)
    inventory = Inventory(
        database=data["database"],
        host=pg.address,
        user=data["user"],
        server_version=data["server_version"],
        schemas=schemas,
        sessions=data["sessions"],
    )
    sql = details_sql(inventory.by_kind("controller", "model", "unknown"))
    if sql:
        try:
            add_details(inventory, pg.query_json(sql))
        except CleanupError as e:
            inventory.details_error = str(e)
    return inventory


def details_sql(schemas):
    """Returns a query for what the Juju schemas record, or None."""
    parts = []
    for s in schemas:
        fields = []
        if "schema" in s.known_tables:
            fields.append(
                f"'patches', (SELECT count(*) FROM {quote_ident(s.name)}.schema)"
            )
        if s.kind == "model" and "model" in s.known_tables:
            fields.append(
                f"'model', (SELECT to_jsonb(m) FROM {quote_ident(s.name)}.model m "
                "LIMIT 1)"
            )
        if s.kind == "controller":
            q = quote_ident(s.name)
            if "controller" in s.known_tables:
                fields.append(
                    "'controllers', (SELECT coalesce(jsonb_agg(jsonb_build_object("
                    "'uuid', c.uuid, 'model_uuid', c.model_uuid, "
                    "'target_version', c.target_version) ORDER BY c.uuid), '[]') "
                    f"FROM {q}.controller c)"
                )
            if {"model", "life"} <= set(s.known_tables):
                fields.append(
                    "'models', (SELECT coalesce(jsonb_agg(jsonb_build_object("
                    "'uuid', m.uuid, 'name', m.name, 'qualifier', m.qualifier, "
                    "'activated', m.activated, 'life', l.value) ORDER BY m.uuid), "
                    f"'[]') FROM {q}.model m LEFT JOIN {q}.life l ON l.id = m.life_id)"
                )
            if "namespace_list" in s.known_tables:
                fields.append(
                    "'namespaces', (SELECT coalesce(jsonb_agg(namespace "
                    f"ORDER BY namespace), '[]') FROM {q}.namespace_list)"
                )
        if fields:
            parts.append(
                f"SELECT {quote_literal(s.name)} AS name, "
                f"jsonb_build_object({', '.join(fields)}) AS info"
            )
    if not parts:
        return None
    union = "\nUNION ALL ".join(parts)
    return f"SELECT coalesce(jsonb_object_agg(d.name, d.info), '{{}}')::text\nFROM (\n{union}\n) d;\n"


def add_details(inventory, details):
    for name, info in details.items():
        s = inventory.find(name)
        s.patches = info.get("patches")
        s.model = info.get("model")
        if s.kind == "controller":
            inventory.controllers = info.get("controllers", [])
            inventory.models = info.get("models", [])
            inventory.namespaces = info.get("namespaces")


def quote_ident(name):
    return '"' + name.replace('"', '""') + '"'


def quote_literal(value):
    return "'" + value.replace("'", "''") + "'"


def test_info(s):
    info = TestInfo(pid=int(TEST_SCHEMA_RE.match(s.name).group(1)))
    m = TEST_COMMENT_RE.match(s.comment or "")
    if m and int(m.group(1)) == info.pid:
        info.started = int(m.group(2))
        info.test = m.group(3)
    info.state = process_state(info.pid, info.started)
    return info


def process_state(pid, started):
    """Returns "running", "gone" or "reused" for a test schema's process.

    "reused" means the PID belongs to a process that started after the test
    process did. When it cannot tell, it answers "running".
    """
    try:
        stat = Path(f"/proc/{pid}/stat").read_text()
    except FileNotFoundError:
        return "gone"
    except OSError:
        return "running"
    if started is None:
        return "running"
    begun = process_start(stat)
    if begun is not None and begun > started + START_TOLERANCE:
        return "reused"
    return "running"


def process_start(stat):
    """Returns the Unix time a process started, from its /proc/<pid>/stat."""
    try:
        # The command name can hold spaces and parentheses; the fields after
        # it start with field 3, and the start time is field 22.
        ticks = int(stat.rpartition(")")[2].split()[19])
        boot = next(
            int(line.split()[1])
            for line in Path("/proc/stat").read_text().splitlines()
            if line.startswith("btime ")
        )
        return boot + ticks / os.sysconf("SC_CLK_TCK")
    except (IndexError, ValueError, StopIteration, OSError):
        return None


@dataclass
class Instance:
    name: str
    project: str
    status: str
    controller_uuid: str
    model_uuid: str

    def describe(self):
        tags = []
        if self.controller_uuid:
            tags.append(f"controller {self.controller_uuid}")
        if self.model_uuid:
            tags.append(f"model {self.model_uuid}")
        return f"{self.name} (project {self.project}): {self.status}, {', '.join(tags)}"


def lxd_instances(lxc):
    """Returns the LXD instances in every project."""
    cmd = [lxc, "list", "--all-projects", "--format=json"]
    try:
        result = subprocess.run(
            cmd, check=False, capture_output=True, text=True, timeout=LXC_TIMEOUT
        )
    except FileNotFoundError:
        raise CleanupError(f"lxc client {lxc!r} not found") from None
    except subprocess.TimeoutExpired:
        raise CleanupError(f"lxc list did not finish in {LXC_TIMEOUT}s") from None
    if result.returncode != 0:
        raise CleanupError(
            f"lxc list failed ({result.returncode}): {result.stderr.strip()}"
        )
    try:
        items = json.loads(result.stdout)
    except ValueError:
        raise CleanupError("lxc list returned invalid JSON") from None
    instances = []
    for item in items:
        config = item.get("config") or {}
        instances.append(
            Instance(
                name=item.get("name"),
                project=item.get("project") or "default",
                status=item.get("status"),
                controller_uuid=config.get("user.juju-controller-uuid"),
                model_uuid=config.get("user.juju-model-uuid"),
            )
        )
    return instances


def matching_instances(instances, uuids):
    return [i for i in instances if i.controller_uuid in uuids or i.model_uuid in uuids]


def print_inventory(inv, lxd):
    print(
        f"Database {inv.database} at {inv.host} as {inv.user}, "
        f"PostgreSQL {inv.server_version}"
    )

    print("\nController and model schemas:")
    juju = inv.by_kind("controller", "model", "unknown")
    if not juju:
        print("  none")
    for s in juju:
        print(f"  {s.name}: {s.kind}, {marker(s)}, {contents(s)}")
        if s.kind == "controller" and not inv.details_error:
            print_controller(inv)
        elif s.model:
            m = s.model
            print(
                f"    model {m.get('qualifier')}/{m.get('name')}, "
                f"controller {m.get('controller_uuid')}"
            )
    if inv.details_error:
        print(f"  could not read the Juju schemas: {inv.details_error}")

    print("\nTest schemas:")
    tests = inv.by_kind("test")
    if not tests:
        print("  none")
    for s in tests:
        t = s.test
        started = format_time(t.started) if t.started else "unknown"
        state = {"reused": "gone, PID reused"}.get(t.state, t.state)
        print(f"  {s.name}: process {t.pid} {state}, {contents(s)}")
        print(f"    started {started}, test {t.test or 'unknown'}")

    others = [s.name for s in inv.by_kind("other")]
    print(f"\nOther schemas, never dropped: {', '.join(others) or 'none'}")

    print("\nOther sessions on this database:")
    print_sessions(inv.sessions, "  ")

    uuids = inv.lxd_uuids()
    if uuids:
        print("\nLXD instances of this database's controller and models:")
        if isinstance(lxd, CleanupError):
            print(f"  unknown: {lxd}")
        else:
            matches = matching_instances(lxd, uuids)
            for i in matches:
                print(f"  {i.describe()}")
            if not matches:
                print("  none")


def print_controller(inv):
    for c in inv.controllers:
        print(
            f"    controller {c['uuid']}, version {c['target_version']}, "
            f"controller model {c['model_uuid']}"
        )
    if not inv.controllers:
        print("    no controller recorded")
    schemas = {s.model_uuid() for s in inv.by_kind("model")}
    for m in inv.models:
        notes = [m.get("life") or "unknown life"]
        if not m.get("activated"):
            notes.append("not activated")
        if m["uuid"] not in schemas:
            notes.append("no schema")
        if inv.namespaces is not None and m["uuid"] not in inv.namespaces:
            notes.append("not in namespace_list")
        print(
            f"    model {m['uuid']} {m.get('qualifier')}/{m.get('name')}: "
            f"{', '.join(notes)}"
        )


def print_sessions(sessions, indent, file=sys.stdout):
    if not sessions:
        print(f"{indent}none", file=file)
    for a in sessions:
        details = [f"user {a['user']}"]
        if a.get("application"):
            details.append(f"application {a['application']!r}")
        if a.get("state"):
            details.append(a["state"])
        print(f"{indent}pid {a['pid']}: {', '.join(details)}", file=file)


def marker(s):
    """Describes whether the schema table records any applied DDL patch."""
    if not s.patches:
        return "not initialised"
    return f"initialised, {plural(s.patches, 'patch', 'patches')}"


def contents(s):
    return f"{plural(s.tables, 'table')}, {pretty_size(s.size)}"


def plural(n, one, many=None):
    return f"{n} {one if n == 1 else many or one + 's'}"


def pretty_size(n):
    for unit in ("bytes", "kB", "MB", "GB"):
        if n < 10 * 1024 or unit == "GB":
            return f"{n} {unit}"
        n = (n + 512) // 1024


def format_time(t):
    return time.strftime("%Y-%m-%d %H:%M:%S %Z", time.localtime(t))


def drop(pg, inv, args):
    if args.controller:
        targets = controller_targets(inv, args.controller)
    elif args.schema:
        targets = named_targets(inv, args.schema)
    else:
        targets = inv.by_kind("test")
        stale = [s for s in targets if s.test.stale()]
        for s in targets:
            if not s.test.stale():
                log(f"Keeping {s.name}: process {s.test.pid} is running")
        targets = stale
        if not targets:
            log("No stale test schemas")
            return
    # juju_controller goes last, so that --controller still finds the
    # controller if a drop fails part way through.
    targets.sort(key=lambda s: (s.kind == "controller", s.name))
    names = [s.name for s in targets]

    log(f"Plan: drop {plural(len(names), 'schema')} from {inv.database} at {inv.host}:")
    for s in targets:
        log(f"  {s.name}: {s.kind}, {contents(s)}")
    if targets[0].kind == "test":
        for s in targets:
            check_test_process(s)
    else:
        check_lxd(inv, args.lxc)
        log("Other sessions on this database, not attributable:")
        print_sessions(inv.sessions, "  ", file=sys.stderr)
    deps = pg.query_json(
        DEPENDENTS_JSON_SQL.format(targets=":'targets'::jsonb"),
        targets=json.dumps(names),
    )
    if deps:
        raise CleanupError(
            "objects outside the targets depend on them; drop or change them "
            "first:\n  " + "\n  ".join(deps)
        )
    log("Nothing outside the targets depends on them")

    confirm(len(names), args.yes)
    for i, name in enumerate(names):
        try:
            pg.run(DROP_SQL, targets=json.dumps(names[i:]), schema=name)
        except CleanupError as e:
            dropped = ", ".join(names[:i]) or "none"
            raise CleanupError(
                f"dropping {name}: {e}\nDropped: {dropped}\n"
                f"Not dropped: {', '.join(names[i:])}"
            ) from None
        print(name, flush=True)
    left = pg.query_json(REMAINING_SQL, targets=json.dumps(names))
    if left:
        raise CleanupError(f"schemas still exist after the drop: {', '.join(left)}")
    log(f"Dropped {plural(len(names), 'schema')}")


def controller_targets(inv, uuid):
    if not inv.find(CONTROLLER_SCHEMA):
        raise CleanupError(f"there is no {CONTROLLER_SCHEMA} schema")
    if inv.details_error:
        raise CleanupError(f"cannot read {CONTROLLER_SCHEMA}: {inv.details_error}")
    recorded = [c["uuid"] for c in inv.controllers]
    if not recorded:
        raise CleanupError(
            f"{CONTROLLER_SCHEMA} records no controller; name the schemas with --schema"
        )
    if uuid not in recorded:
        raise CleanupError(
            f"{CONTROLLER_SCHEMA} belongs to controller {', '.join(recorded)}, "
            f"not {uuid}"
        )
    unknown = inv.by_kind("unknown")
    if unknown:
        log(
            "Not dropping schemas with unknown names; use --schema: "
            + ", ".join(s.name for s in unknown)
        )
    return inv.by_kind("controller", "model")


def named_targets(inv, names):
    targets = []
    for name in dict.fromkeys(names):
        s = inv.find(name)
        if not s:
            raise CleanupError(f"there is no schema {name!r}")
        if s.kind == "other":
            raise CleanupError(f"{name} is not a Juju or test schema")
        targets.append(s)
    tests = [s for s in targets if s.kind == "test"]
    if tests and len(targets) > 1:
        raise CleanupError(
            "name one test schema, or only juju_* schemas; "
            "use --stale-test-schemas for several test schemas"
        )
    return targets


def check_test_process(s):
    t = s.test
    if t.state == "running":
        raise CleanupError(f"{s.name}: process {t.pid} is still running")
    if t.state == "reused":
        log(f"  {s.name}: PID {t.pid} now belongs to a later process")
    else:
        log(f"  {s.name}: process {t.pid} has gone")


def check_lxd(inv, lxc):
    uuids = inv.lxd_uuids()
    if not uuids:
        log(
            "WARNING: the database records no controller or model UUID, so "
            "no LXD instance can be checked"
        )
        return
    try:
        instances = lxd_instances(lxc)
    except CleanupError as e:
        raise CleanupError(
            f"cannot check that the controller is stopped: {e}"
        ) from None
    matches = matching_instances(instances, uuids)
    running = [i for i in matches if i.status != "Stopped"]
    if running:
        raise CleanupError(
            "stop or remove these LXD instances of the database's controller "
            "and models first:\n  " + "\n  ".join(i.describe() for i in running)
        )
    log(
        f"LXD: none of {len(matches)} instances of the database's controller "
        f"and models is running ({len(uuids)} UUIDs checked)"
    )
    for i in matches:
        log(f"  {i.describe()}")


def confirm(count, assume_yes):
    if assume_yes:
        return
    if not sys.stdin.isatty():
        raise CleanupError("no terminal to confirm on; use --yes to drop without one")
    print(
        f'Type "{CONFIRM_WORD}" to drop {plural(count, "schema")}: ',
        end="",
        file=sys.stderr,
        flush=True,
    )
    if sys.stdin.readline().strip() != CONFIRM_WORD:
        raise CleanupError("not confirmed; nothing was dropped")


# Lists the objects outside the target schemas that depend directly on an
# object inside them, as the PostgreSQL test harness does for one schema.
# {targets} is a jsonb array of schema names.
DEPENDENTS_SQL = """\
WITH target AS (
    SELECT oid FROM pg_namespace
    WHERE nspname IN (SELECT jsonb_array_elements_text({targets}))
),
inside_rel AS (
    SELECT c.oid FROM pg_class c JOIN target t ON c.relnamespace = t.oid
),
inside_opf AS (
    SELECT f.oid FROM pg_opfamily f JOIN target t ON f.opfnamespace = t.oid
),
inside (classid, objid) AS (
    SELECT 'pg_namespace'::regclass, oid FROM target
    UNION ALL SELECT 'pg_class'::regclass, oid FROM inside_rel
    UNION ALL SELECT 'pg_opfamily'::regclass, oid FROM inside_opf
    UNION ALL SELECT 'pg_type'::regclass, x.oid
        FROM pg_type x JOIN target t ON x.typnamespace = t.oid
    UNION ALL SELECT 'pg_proc'::regclass, x.oid
        FROM pg_proc x JOIN target t ON x.pronamespace = t.oid
    UNION ALL SELECT 'pg_constraint'::regclass, x.oid
        FROM pg_constraint x JOIN target t ON x.connamespace = t.oid
    UNION ALL SELECT 'pg_collation'::regclass, x.oid
        FROM pg_collation x JOIN target t ON x.collnamespace = t.oid
    UNION ALL SELECT 'pg_conversion'::regclass, x.oid
        FROM pg_conversion x JOIN target t ON x.connamespace = t.oid
    UNION ALL SELECT 'pg_operator'::regclass, x.oid
        FROM pg_operator x JOIN target t ON x.oprnamespace = t.oid
    UNION ALL SELECT 'pg_opclass'::regclass, x.oid
        FROM pg_opclass x JOIN target t ON x.opcnamespace = t.oid
    UNION ALL SELECT 'pg_statistic_ext'::regclass, x.oid
        FROM pg_statistic_ext x JOIN target t ON x.stxnamespace = t.oid
    UNION ALL SELECT 'pg_ts_config'::regclass, x.oid
        FROM pg_ts_config x JOIN target t ON x.cfgnamespace = t.oid
    UNION ALL SELECT 'pg_ts_dict'::regclass, x.oid
        FROM pg_ts_dict x JOIN target t ON x.dictnamespace = t.oid
    UNION ALL SELECT 'pg_ts_parser'::regclass, x.oid
        FROM pg_ts_parser x JOIN target t ON x.prsnamespace = t.oid
    UNION ALL SELECT 'pg_ts_template'::regclass, x.oid
        FROM pg_ts_template x JOIN target t ON x.tmplnamespace = t.oid
    UNION ALL SELECT 'pg_default_acl'::regclass, x.oid
        FROM pg_default_acl x JOIN target t ON x.defaclnamespace = t.oid
    UNION ALL SELECT 'pg_trigger'::regclass, x.oid
        FROM pg_trigger x JOIN inside_rel r ON x.tgrelid = r.oid
    UNION ALL SELECT 'pg_rewrite'::regclass, x.oid
        FROM pg_rewrite x JOIN inside_rel r ON x.ev_class = r.oid
    UNION ALL SELECT 'pg_attrdef'::regclass, x.oid
        FROM pg_attrdef x JOIN inside_rel r ON x.adrelid = r.oid
    UNION ALL SELECT 'pg_policy'::regclass, x.oid
        FROM pg_policy x JOIN inside_rel r ON x.polrelid = r.oid
    UNION ALL SELECT 'pg_amop'::regclass, x.oid
        FROM pg_amop x JOIN inside_opf f ON x.amopfamily = f.oid
    UNION ALL SELECT 'pg_amproc'::regclass, x.oid
        FROM pg_amproc x JOIN inside_opf f ON x.amprocfamily = f.oid
)
SELECT dep FROM (
    SELECT DISTINCT
        pg_describe_object(d.classid, d.objid, d.objsubid) || ' depends on ' ||
        pg_describe_object(d.refclassid, d.refobjid, d.refobjsubid) AS dep
    FROM pg_depend d
    JOIN inside ref ON ref.classid = d.refclassid AND ref.objid = d.refobjid
    WHERE d.deptype <> 'i'
    AND NOT EXISTS (
        SELECT 1 FROM inside dep
        WHERE dep.classid = d.classid AND dep.objid = d.objid
    )
) deps
WHERE dep IS NOT NULL
"""

DEPENDENTS_JSON_SQL = (
    "SELECT coalesce(jsonb_agg(dep ORDER BY dep), '[]')::text FROM (\n"
    + DEPENDENTS_SQL
    + ") d;\n"
)

# Drops :schema, after checking that nothing outside :targets, the targets
# not yet dropped, depends on them. psql does not interpolate variables
# inside dollar quotes, so the check reads the targets from a
# transaction-local setting.
DROP_SQL = (
    """\
BEGIN;
SELECT set_config('juju_poc_cleanup.targets', :'targets', true);
DO $$
DECLARE
    outside text;
BEGIN
    SELECT string_agg(dep, chr(10) ORDER BY dep) INTO outside FROM (
"""
    + DEPENDENTS_SQL.format(
        targets="current_setting('juju_poc_cleanup.targets')::jsonb"
    )
    + """\
    ) d;
    IF outside IS NOT NULL THEN
        RAISE EXCEPTION 'objects outside the targets depend on them'
            USING DETAIL = outside;
    END IF;
END
$$;
DROP SCHEMA IF EXISTS :"schema" CASCADE;
COMMIT;
"""
)

REMAINING_SQL = """\
SELECT coalesce(jsonb_agg(nspname ORDER BY nspname), '[]')::text
FROM pg_namespace
WHERE nspname IN (SELECT jsonb_array_elements_text(:'targets'::jsonb));
"""


if __name__ == "__main__":
    sys.exit(main())
