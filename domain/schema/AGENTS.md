# Juju Schema Rules for Coding Agents

These rules apply to `domain/schema/` and its sub-packages and supplement the
[domain guidance](../AGENTS.md).

## Schema Evolution

- Before editing an existing schema patch, check its release history and the
  supported upgrade path for the target release. Distinguish beta-only changes
  from patches shipped in release candidates or final releases.
- Preserve existing patch contents and ordering when supported installations
  must upgrade in place. Patch hashes form a chain; changing an applied patch
  or inserting one before it breaks that chain.
- Base schema files may change when no supported in-place upgrade depends on
  their existing contents. Do not require an incremental patch solely because
  a file existed in a beta. Check the applicable upgrade policy and migration
  path before deciding.
- For incremental changes within the same major.minor version, add a
  `.PATCH.sql` file under `dqlite/controller/sql/` or `dqlite/model/sql/`.
  Register it in `controllerPostPatchFilesByVersion` in [controller.go](controller.go) or
  `modelPostPatchFilesByVersion` in [model.go](model.go), at the version where
  it is first applied. File creation alone does not register a post-patch.
- Verify fresh database creation and supported cumulative in-place upgrades,
  including preservation or intentional transformation of existing data. Use
  the schema suites in [controller_schema_test.go](controller_schema_test.go)
  and [model_schema_test.go](model_schema_test.go).

## PostgreSQL Generation

- `dqlite/` is the source schema. `go generate ./domain/schema/...` runs
  `generate/pgschemagen` after the dqlite triggers are generated. It translates
  the complete patch list, including Go-built triggers, into checked-in files
  under `postgresql/{controller,model}/sql/`.
- Do not edit generated PostgreSQL SQL files. Improve the translator for
  general dialect rules; use an override for a patch-specific translation.
  PostgreSQL overrides do not change dqlite patches or their hashes.
- An override is `postgresql/<namespace>/overrides/NNNN-<slug>.diff`, a unified
  diff from the raw translation to the desired SQL. To start an override,
  create an empty `.diff` and rerun the generator. It writes the corresponding
  `NNNN-<slug>-auto.sql`; copy that file outside the generated tree, edit the
  copy, and produce a unified diff from `-auto.sql` to the edited copy. Commit
  both the `.diff` and `-auto.sql`, then regenerate.
- Override diffs apply before foreign-key and view ordering. Exact hunk context
  must match, although line offsets may change. Stale diffs and unsupported
  `.PATCH.sql` files fail generation. When a dqlite fix makes an override
  obsolete, remove its `.diff` and regenerate; stale generated files are removed.
- `ControllerDDL()` and `ModelDDL()` remain dqlite. Backend-aware callers use
  `ControllerDDLForBackend` and `ModelDDLForBackend`; unknown backends return an
  error. Runtime caller wiring is separate from schema generation.
- Run `go test ./generate/pgschemagen ./core/database/schema ./domain/schema/...`.
  The generator suite checks every generated file for freshness. Set
  `JUJU_POSTGRESQL_TEST_URL` to opt into the PostgreSQL schema suite. It creates
  and drops unique throwaway schemas, never `public` or `login_hook`.
- PostgreSQL constraint tag, space and zone tables have explicit identity
  `rowid` columns to preserve the insertion order used by the application
  constraint view. Include column lists in inserts into these tables.
