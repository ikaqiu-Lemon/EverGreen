# Evergreen

Evergreen is a deterministic, local-first knowledge-base CLI distributed as a
single `eg` binary. It stores human-readable Markdown in a Git repository and
helps users and coding agents capture sources, build knowledge cards, maintain
relationships, review changes, and check repository consistency.

Evergreen does not call models, fetch URLs, send telemetry, or make runtime
network requests. Markdown is the authoritative data source; the SQLite index
is a local, rebuildable acceleration layer.

Current version: `0.6.0-m6`.

Module: `github.com/ikaqiu-Lemon/EverGreen`.

## Why Evergreen

- **Local-first**: vault content stays in a directory selected by the user.
- **Reviewable**: every authoritative change is represented as a Git commit.
- **Byte-preserving**: writes update bounded document spans instead of
  serializing complete YAML documents.
- **Agent-safe**: risky operations require explicit user authorization.
- **Recoverable**: multi-file writes use a local lock and transaction journal.
- **Deterministic**: commands have stable ordering, JSON envelopes, and exit
  codes.

## Requirements

- Go 1.24 or newer
- Git
- GNU Make
- Python 3 and PyYAML for the manifest-driven test suite

## Build

Clone the public repository, then build from its root:

```console
git clone https://github.com/ikaqiu-Lemon/EverGreen.git
cd EverGreen
```

```bash
make build
./bin/eg --version
./bin/eg --help
make test
make lint
```

`make build` creates the native `bin/eg` binary and cross-compiles release
artifacts for Linux and macOS. `make dist` writes `dist/SHA256SUMS` and
`dist/PROVENANCE.txt`; `make verify-dist-provenance` verifies that the
provenance file is bound to the current artifact hashes. See [INSTALL.md](INSTALL.md)
for installation, checksum verification, and a complete first-run example.

## Commands

The CLI exposes 22 top-level commands（顶层命令共 22 个）:

| Command | Purpose |
| --- | --- |
| `eg init` | Initialize a Git-backed vault |
| `eg config get`, `eg config set` | Read or update vault configuration |
| `eg capture` | Store source material and add it to the inbox |
| `eg context` | Produce deterministic context for a ChangePlan |
| `eg apply` | Validate and apply a ChangePlan |
| `eg search` | Search cards with stable sorting and pagination |
| `eg card show` | Show one card, its sources, and relationships |
| `eg rel` | Query relationships |
| `eg rel add`, `eg rel remove` | Add or remove a relationship |
| `eg report --last` | Replay the latest report from `eg apply`, `eg capture`, and other report-producing writes |
| `eg deprecate`, `eg restore` | Change a card's lifecycle state |
| `eg replaced-by` | Link a deprecated card to its replacement |
| `eg proposal` | Create, inspect, approve, or reject risky-operation proposals |
| `eg delete`, `eg undelete` | Apply or reverse a logical deletion |
| `eg mark-reviewed`, `eg unreviewed` | Record or inspect review state |
| `eg edit` | Replace an explicitly authorized card section |
| `eg reconcile` | Inspect and optionally repair repository-wide consistency |
| `eg check` | Run read-only structural checks |
| `eg index` | Build, rebuild, inspect, or synchronize the derived index |
| `eg bench` | Measure deterministic read and indexing workloads |

Run `eg <command> --help` for the authoritative argument list. Important query
flags include `--include-deleted`, `--include-deprecated`, `--limit`, and
`--offset`. The default `--limit` is 50; `--limit 0` disables truncation.

The proposal workflow consists of `eg proposal new`, `eg proposal list`,
`eg proposal show`, `eg proposal approve`, and `eg proposal reject`.

The four index operations are `eg index build`, `eg index rebuild`,
`eg index status`, and `eg index sync`.

`eg bench --json` reports `search_p95_ms`, `card_show_p95_ms`, `rel_p95_ms`,
`index_build_ms`, and `index_incremental_ms`.

## Data and safety model

A vault contains:

```text
evergreen.yml
SKILL.md
unprocessed.md
sources/
domains/<domain>/notes/
domains/<domain>/knowledge/
proposals/
```

Authoritative Markdown and frontmatter are committed to the vault's Git
history. `.eg/` contains local report state. `.index/` contains the
可重建派生物: a SQLite/FTS5 index implemented with `modernc.org/sqlite`, plus
runtime lock and transaction data. These runtime directories are not committed.

M6 已启用 `run.lock`、事务日志、崩溃恢复、块级安全合并和退出码 `5`。
Its reliability guarantees are:

- `run.lock` serializes local writers and reports contention as `W28`;
- `.index/txn/` is the transaction journal（事务日志）and records before-images;
- crash recovery（崩溃恢复）reports interrupted writes as `W26`;
- block-level safety checks（块级安全合并）report unsafe merges as `W27`;
- `--strict` pre-write validation rejects unsafe writes with `E15`;
- lock acquisition failures use `E16`;
- 退出码 `5` represents pre-write validation or lock failure.

存在未闭合事务时不可删 `.index/`. The transaction journal contains recovery
data that cannot be rebuilt from Markdown.

If a transaction is blocked（事务阻断态）, run `eg check --json` to identify
each affected `txn_id` and `.index/txn/<txn_id>` path. Back up the listed
transaction directory before manually removing a corrupt or redundant entry.

When the index is stale, missing, or corrupt, read commands fall back to
scanning authoritative Markdown and report `W22`, `W23`, or `W24` together with
`Q5`. Pagination truncation is reported as `W25`; `W21` remains 不分配.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Completed successfully |
| `1` | Invalid command or arguments |
| `2` | Validation failed |
| `3` | Some requested writes were skipped |
| `4` | Git commit or derived-index write failed |
| `5` | Strict precheck (`E15`) or local lock (`E16`) failed |
| `6` | Validation passed but explicit user confirmation is required |

JSON mode uses a stable envelope with `ok`, `data`, `warnings`, `exit_code`, and
`status`.

## Development

Architecture and package boundaries are documented in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Contribution requirements are in
[CONTRIBUTING.md](CONTRIBUTING.md).

```bash
make test-full
make test-race
```

## Platform status

Linux/amd64 is the original fully exercised platform. Linux/arm64,
Darwin/amd64, and Darwin/arm64 release binaries are cross-compiled. The Darwin
artifacts have not yet completed independent macOS runtime validation and must
be treated as **未经真机运行验证**.

The local `flock`-based lock is intended for processes on one machine. Atomicity
is not guaranteed on a network drive or synchronized filesystem（网络盘或同步盘）.

## Security and privacy

See [SECURITY.md](SECURITY.md) for private vulnerability reporting and
[PRIVACY.md](PRIVACY.md) for the local data-handling model.

Community participation is governed by
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

Evergreen is licensed under the [Apache License 2.0](LICENSE).
