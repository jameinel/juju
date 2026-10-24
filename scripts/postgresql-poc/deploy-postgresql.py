#!/usr/bin/env python3
# Copyright 2026 Canonical Ltd.
# Licensed under the AGPLv3, see LICENCE file for details.

"""Deploy PostgreSQL for the Juju 4 PostgreSQL proof of concept.

Deploys postgresql (16/stable) and data-integrator (database-name=juju) into
a model on a Juju 3.6 controller, creating the model if needed, then prints a
PostgreSQL URL for the scoped data-integrator user. Re-running is safe.

With --tls-ca-file, self-signed-certificates is related to
postgresql:client-certificates, the CA certificate is written to the given
file and the URL uses sslmode=verify-full with sslrootcert set to the file's
absolute path. Otherwise the URL uses sslmode=disable. Once TLS is enabled the
server rejects unencrypted connections, so compare plain and TLS using
separate models.

Test suites create and drop many schemas, so postgresql is configured to keep
disk use down: pgaudit, connection logging and disconnection logging are off,
and wal_keep_size is 128 MB, Patroni's default (the charm keeps 4 GB of WAL by
default). The charm hard-codes log_statement=ddl, so DDL is still logged. All
charm storage is on the root filesystem, sized by --root-disk and placed in the
LXD storage pool --root-disk-source. Re-running applies the configuration to an
existing postgresql, but cannot change its constraints or storage.

Status messages go to stderr. The URL goes to stdout, or to --output (mode
0600). See docs/implementation-plans/postgresql/PLAN.md (Stage 1).

Teardown:
  juju destroy-model -c <controller> <model> --destroy-storage --no-prompt
"""

import argparse
import json
import os
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import quote

PG_APP = "postgresql"
PG_CHANNEL = "16/stable"
PG_SETTINGS = {
    "plugin-audit-enable": "false",
    "logging-log-connections": "false",
    "logging-log-disconnections": "false",
    # MB. Nothing reads the kept WAL without a replica. Patroni's default is
    # 128 and its minimum 16; the charm's default is 4096.
    "durability-wal-keep-size": "128",
}
# Rev 1218 builds some PostgreSQL parameters from both spellings of an option,
# and the deprecated underscored option's default wins, so set both.
PG_CONFIG = {
    name: value
    for option, value in PG_SETTINGS.items()
    for name in (option, option.replace("-", "_"))
}
PG_STORAGE = {name: "rootfs" for name in ("archive", "data", "logs", "temp")}
DEFAULT_ROOT_DISK = "20G"
DI_APP = "data-integrator"
DI_CHANNEL = "latest/stable"
TLS_APP = "self-signed-certificates"
TLS_CHANNEL = "1/stable"
DATABASE = "juju"
POLL_INTERVAL = 2


class DeployError(Exception):
    pass


def log(msg):
    print(f"{time.strftime('%H:%M:%S')} {msg}", file=sys.stderr, flush=True)


def main(argv=None):
    args = parse_args(argv)
    ca_file = Path(args.tls_ca_file).resolve() if args.tls_ca_file else None
    juju = Juju(args.juju, args.controller, args.model)
    constraints = f"root-disk={args.root_disk}"
    if args.root_disk_source:
        constraints += f" root-disk-source={args.root_disk_source}"
    try:
        deploy(juju, tls=ca_file is not None, constraints=constraints)
        wait_settled(juju, args.timeout)
        if ca_file:
            creds = wait_tls_credentials(juju, args.timeout)
            ca_file.write_text(creds["tls-ca"].rstrip("\n") + "\n")
            log(f"Wrote CA certificate to {ca_file}")
        else:
            creds = get_credentials(juju)
        url = build_url(creds, ca_file)
    except DeployError as e:
        print(f"ERROR: {e}", file=sys.stderr)
        return 1
    write_url(url, args.output)
    return 0


def parse_args(argv):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "-c", "--controller", required=True, help="Juju 3.6 controller to deploy into"
    )
    parser.add_argument("-m", "--model", required=True, help="model to deploy into")
    parser.add_argument(
        "-o", "--output", help="write the URL to this file (mode 0600), not stdout"
    )
    parser.add_argument(
        "-t",
        "--tls-ca-file",
        help="enable TLS and write the PostgreSQL CA certificate to this file",
    )
    parser.add_argument(
        "--root-disk",
        default=DEFAULT_ROOT_DISK,
        help="root disk size of the postgresql machine (default: %(default)s)",
    )
    parser.add_argument(
        "--root-disk-source",
        help="LXD storage pool for the postgresql root disk "
        "(default: the pool of the LXD default profile)",
    )
    parser.add_argument(
        "--juju", default="juju", help="Juju client to use (default: %(default)s)"
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=1800,
        help="seconds to wait for the model to settle (default: %(default)s)",
    )
    return parser.parse_args(argv)


class Juju:
    """Runs the Juju client against one model."""

    def __init__(self, binary, controller, model):
        self.binary = binary
        self.controller = controller
        self.model = model
        self.target = f"{controller}:{model}"

    def model_exists(self):
        return self._run("show-model", self.target, check=False) is not None

    def add_model(self):
        self._run("add-model", "-c", self.controller, self.model, "--no-switch")

    def status(self):
        return json.loads(self._run("status", "-m", self.target, "--format=json"))

    def deploy(self, app, channel, config=None, constraints=None, storage=None):
        args = ["deploy", "-m", self.target, app, "--channel", channel]
        for key, value in (config or {}).items():
            args += ["--config", f"{key}={value}"]
        if constraints:
            args += ["--constraints", constraints]
        for name, directive in (storage or {}).items():
            args += ["--storage", f"{name}={directive}"]
        self._run(*args)

    def integrate(self, endpoint, remote):
        self._run("integrate", "-m", self.target, endpoint, remote)

    def config_get(self, app, key):
        return self._run("config", "-m", self.target, app, key).strip()

    def config_set(self, app, config):
        pairs = [f"{key}={value}" for key, value in config.items()]
        self._run("config", "-m", self.target, app, *pairs)

    def run_action(self, unit, action):
        out = self._run("run", "-m", self.target, unit, action, "--format=json")
        return json.loads(out)

    def _run(self, *args, check=True):
        """Runs juju and returns its stdout, or None if it fails and not check.

        stdout is never included in errors: action results carry credentials.
        """
        cmd = [self.binary, *args]
        try:
            result = subprocess.run(cmd, capture_output=True, text=True)
        except FileNotFoundError as e:
            raise DeployError(f"juju client {self.binary!r} not found") from e
        if result.returncode == 0:
            return result.stdout
        if not check:
            return None
        raise DeployError(
            f"{' '.join(cmd[:2])} failed ({result.returncode}): {result.stderr.strip()}"
        )


def deploy(juju, tls, constraints):
    if juju.model_exists():
        log(f"Using existing model {juju.target}")
    else:
        log(f"Adding model {juju.target}")
        juju.add_model()

    status = juju.status()
    if has_app(status, PG_APP):
        log(f"Using existing application {PG_APP}; applying its configuration")
        juju.config_set(PG_APP, PG_CONFIG)
    else:
        log(f"Deploying {PG_APP} ({PG_CHANNEL})")
        juju.deploy(PG_APP, PG_CHANNEL, PG_CONFIG, constraints, PG_STORAGE)
    if has_app(status, DI_APP):
        configured = juju.config_get(DI_APP, "database-name")
        if configured != DATABASE:
            raise DeployError(
                f"{DI_APP} has database-name={configured!r}, expected {DATABASE!r}"
            )
        log(f"Using existing application {DI_APP}")
    else:
        ensure_app(juju, status, DI_APP, DI_CHANNEL, config={"database-name": DATABASE})
    ensure_relation(juju, status, PG_APP, "database", DI_APP, "postgresql")

    if tls:
        ensure_app(juju, status, TLS_APP, TLS_CHANNEL)
        ensure_relation(
            juju, status, PG_APP, "client-certificates", TLS_APP, "certificates"
        )
    elif has_relation(status, PG_APP, "client-certificates", TLS_APP):
        raise DeployError(f"{PG_APP} has TLS enabled; re-run with --tls-ca-file")


def ensure_app(juju, status, app, channel, config=None):
    if has_app(status, app):
        log(f"Using existing application {app}")
        return
    log(f"Deploying {app} ({channel})")
    juju.deploy(app, channel, config)


def ensure_relation(juju, status, app, endpoint, remote_app, remote_endpoint):
    if has_relation(status, app, endpoint, remote_app):
        return
    log(f"Integrating {app}:{endpoint} with {remote_app}:{remote_endpoint}")
    juju.integrate(f"{app}:{endpoint}", f"{remote_app}:{remote_endpoint}")


def has_app(status, app):
    return app in status.get("applications", {})


def has_relation(status, app, endpoint, remote_app):
    app_status = status.get("applications", {}).get(app, {})
    for rel in app_status.get("relations", {}).get(endpoint, []):
        # Older clients list related application names, newer ones objects.
        name = rel.get("related-application") if isinstance(rel, dict) else rel
        if name == remote_app:
            return True
    return False


def wait_settled(juju, timeout):
    log("Waiting for all applications to be active and idle")
    deadline = time.monotonic() + timeout
    while True:
        try:
            pending = pending_units(juju.status())
        except DeployError as e:
            pending = [str(e)]
        if not pending:
            log(f"Model {juju.target} is settled")
            return
        if time.monotonic() >= deadline:
            raise DeployError(f"timed out waiting for: {', '.join(pending)}")
        time.sleep(POLL_INTERVAL)


def pending_units(status):
    """Returns descriptions of units that are not active and idle."""
    pending = []
    for app, app_status in status.get("applications", {}).items():
        units = app_status.get("units") or {}
        if not units:
            pending.append(f"{app}: no units")
        for name, unit in units.items():
            workload = unit.get("workload-status", {}).get("current")
            agent = unit.get("juju-status", {}).get("current")
            if workload != "active" or agent != "idle":
                pending.append(f"{name}: {workload}/{agent}")
    return pending


def get_credentials(juju):
    results = juju.run_action(f"{DI_APP}/leader", "get-credentials")
    for task in results.values():
        creds = task.get("results", {}).get("postgresql")
        if creds:
            return creds
    raise DeployError("get-credentials returned no postgresql data")


def wait_tls_credentials(juju, timeout):
    """Returns credentials once the database publishes TLS and its CA."""
    deadline = time.monotonic() + timeout
    while True:
        creds = get_credentials(juju)
        if str(creds.get("tls")).lower() == "true" and creds.get("tls-ca"):
            return creds
        if time.monotonic() >= deadline:
            raise DeployError("timed out waiting for TLS in the relation data")
        log(f"Waiting for {PG_APP} to publish TLS to {DI_APP}")
        time.sleep(POLL_INTERVAL)


def build_url(creds, ca_file=None):
    missing = [
        k for k in ("username", "password", "endpoints", "database") if not creds.get(k)
    ]
    if missing:
        raise DeployError(f"get-credentials is missing {', '.join(missing)}")
    user = quote(creds["username"], safe="")
    password = quote(creds["password"], safe="")
    endpoint = creds["endpoints"].split(",")[0]
    database = quote(creds["database"], safe="")
    if ca_file:
        query = f"sslmode=verify-full&sslrootcert={quote(str(ca_file), safe='/')}"
    else:
        query = "sslmode=disable"
    return f"postgres://{user}:{password}@{endpoint}/{database}?{query}"


def write_url(url, path):
    if not path:
        print(url)
        return
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(url + "\n")
    log(f"Wrote db-url to {path}")


if __name__ == "__main__":
    sys.exit(main())
