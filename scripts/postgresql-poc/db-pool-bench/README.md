# Database pool benchmark (experiment)

This branch is an experiment and is not part of the PostgreSQL PoC stack.
It tested whether leasing PostgreSQL test databases from a pool speeds up
the unit-test suite. It did not, so the pool is not pursued.

## Setup

One data-integrator per database, co-located with `postgresql`: `di01`
to `di08`, databases `juju-01` to `juju-08`. Each relation user may
connect only to its own database and has no `CREATEDB`, so a pool of N
needs N data-integrators.

Write one URL per line (from each `get-credentials` action) to
`~/.config/juju-pg-poc/db-pool-<n>` with mode 0600. `pool-sample.sh`
uses the first line of `~/.config/juju-pg-poc/db-pool`.

Set `JUJU_POSTGRESQL_TEST_URL_POOL` to the pool file. Each test process
takes the lowest free slot with a non-blocking `flock` on
`<file>.slot-<n>.lock` and uses `urls[slot % N]` for its lifetime.

```
scripts/postgresql-poc/db-pool-bench/pool-bench.sh /tmp/pool-bench "1:8 4:8 8:8"
```

## Results (2026-11-05)

138 packages (`packages.txt`), untagged `go test -count=1 -p 8`, warm
build cache, 28-CPU host, PostgreSQL 16 with the `unit-test` profile.

| N | Wall time | Summed package time | `domain/application/state` |
|---:|---:|---:|---:|
| 1 | 591 s | 4309 s | 560 s |
| 4 | 566 s | 4036 s | 505 s |
| 8 | 612 s | 4078 s | 501 s |

Every run: 99 packages pass and 36 fail with known PostgreSQL
incompatibilities. Wall time is bound by `domain/application/state`,
whose tests run serially in one process. Separate catalogs cut summed
package time by about 6%. Speed-ups must come from per-test setup cost
or from splitting the longest packages.
