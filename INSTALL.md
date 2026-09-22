# Install Evergreen

This guide installs and exercises Evergreen `eg` version `0.8.0-m8`.

## Requirements

- Go 1.25 or newer
- Git
- GNU Make
- Python 3 with PyYAML when running the manifest-driven tests

Offline CI/builds use `GOPROXY=off`; preload the Go module cache first (or ship a
complete `vendor/` directory). `tests/ci/pipeline.sh` checks this before running
lint or tests and reports a clear environment error when modules cannot be
resolved offline.

All build targets use `CGO_ENABLED=0`. The resulting binaries have no C runtime
dependency.

## Build from source

Clone the repository:

```console
git clone https://github.com/ikaqiu-Lemon/EverGreen.git
cd EverGreen
```

Then run:

```bash
make build
./bin/eg --version
make print-version
make test
make lint
```

`make build` creates `bin/eg` for the current platform and four binaries under
`dist/`. The version output contains the release version, source commit, build
time, and Go toolchain. `make dist` also writes `dist/PROVENANCE.txt`; its
`artifact checksums` section must match `dist/SHA256SUMS`, and `make
verify-dist-provenance` verifies both the provenance/checksum binding and every
artifact hash.

After the first release is published, Go users may alternatively install it
directly:

```console
go install github.com/ikaqiu-Lemon/EverGreen/cmd/eg@latest
```

## Build release artifacts

```bash
make release
cd dist
(command -v sha256sum >/dev/null && sha256sum -c SHA256SUMS) || shasum -a 256 -c SHA256SUMS
cd ..
mkdir -p bin
cp "dist/eg_$(go env GOOS)_$(go env GOARCH)" bin/eg
chmod +x bin/eg
./bin/eg --version
```

The release files are:

```text
dist/eg_linux_amd64
dist/eg_linux_arm64
dist/eg_darwin_amd64
dist/eg_darwin_arm64
dist/SHA256SUMS
dist/PROVENANCE.txt
```

`make dist` builds the same four platform binaries without deleting `bin/eg`.
`make clean` removes local build and release outputs; this is safe because
`dist/PROVENANCE.txt` is generated from the current build and can be recreated by
`make dist` or `make release`.

## First run

The vault is a separate Git repository containing your knowledge base.

```bash
VAULT="${VAULT:-$HOME/notes/evergreen}"
BODY="$(mktemp)"
mkdir -p "$VAULT"
export PATH="$PWD/bin:$PATH"
eg --vault "$VAULT" init --domain ai-infra
eg --vault "$VAULT" config set default_domain ai-infra
printf 'Transformer uses parallel attention instead of recurrence.\n' > "$BODY"
eg --vault "$VAULT" capture --url https://example.com/attention --title "Attention notes" --body-file "$BODY" --reason "first capture"
rm -f "$BODY"
eg --vault "$VAULT" search attention
```

The temporary source body is deliberately written outside the vault. `eg capture`
records its content into `sources/`; the input file itself should not be placed
inside the vault and should not enter the vault's Git history.

`eg capture` records the supplied URL and body; it does not fetch the URL.
Agent-assisted processing continues with `eg context`, a generated ChangePlan,
and `eg apply --plan`.

## Schema v2 summary

After installing, know these Schema v2 contract facts (the full walkthrough is
in [README.md](README.md); this is only the post-install
compatibility/validation digest):

- **ChangePlan `plan_version`.** Current plans use `plan_version: 2`; the
  supported set is `{1, 2}`. A `plan_version: 1` plan is still accepted for
  compatibility and flagged with a single `I1` migration info.
- **Index `schema_version`.** The derived index carries `schema_version = 2`
  and still holds six tables. There is no incremental migration: on a version
  mismatch the whole index is discarded and rebuilt from Markdown.
- **`eg context` candidates.** The context envelope returns
  `draft_candidates`, `knowledge_candidates`, and `opinion_candidates`.
  `draft_candidates` projects Note-embedded draft/materialized mappings without
  mixing them into already-materialized search candidates. Version `0.8.0-m8`
  removes the legacy `candidates` alias and its deprecation `I1`.
- **Storage v3 commands.** `eg materialize --note <n-id>
  (--candidate <cand-key> | --all) --user-request` copies exact candidate bytes
  into Knowledge/Opinion through journal v1. `eg export --plain --output <dir>`
  creates a read-only egress copy without Evergreen protocol anchors.
- **Candidate boundaries.** H3 is the default. The L2 fallback is an
  `eg:cd:1` anchor followed immediately by a fenced-div opener and an
  attribute-free ATX H3 title. The opener carries exactly one `cand-*` ID,
  `.eg-candidate`, and one kind class; L2 cannot nest, and its closing fence
  has no attributes and at least as many colons as the opener.
- **Four learning entities, two domain directories.** Source (`s-*`),
  Note (`n-*`), Knowledge (`k-*` under `domains/<d>/knowledge/`) and Opinion
  (`o-*` under `domains/<d>/opinions/`); `Proposal` (`p-*`) is a control plane,
  not a learning entity.
- **Canonical ops / alias migration.** The main pipeline has nine canonical
  ops including `create_knowledge` / `create_opinion`; `create_card` /
  `append_card` are compatibility aliases normalized to
  `create_knowledge` / `append_knowledge` with an `I1`.
- **Knowledge vs Opinion.** Stable definitions/steps/conditions/data are
  Knowledge (three sections); evaluations, causal/predictive claims and
  trade-offs are Opinion (five sections, `validation` defaults to `pending`).
  When unsure, prefer Opinion.
- **Search default.** `eg search` defaults to `--kind knowledge`; use
  `--kind opinion` or `--kind all` to widen the set.

## Review and logical deletion

```console
SRC=$(basename $(ls $VAULT/sources/*.md | head -1) .md)
PID=$(eg --vault $VAULT proposal new --type logical_delete --target $SRC --reason "duplicate source" --json | grep -oE 'p-[0-9]{8}-[0-9]{3}' | head -1)
eg --vault "$VAULT" mark-reviewed --target "$SRC"
eg --vault "$VAULT" unreviewed
eg --vault "$VAULT" proposal list --status pending
eg --vault "$VAULT" proposal show "$PID"
eg --vault "$VAULT" proposal approve "$PID" --confirm --user-request
eg --vault "$VAULT" delete --target "$SRC" --reason "duplicate source" --proposal "$PID" --confirm --user-request
eg --vault "$VAULT" search attention --include-deleted
eg --vault "$VAULT" undelete --target "$SRC" --reason "source is still needed"
```

Deletion is logical. Evergreen preserves the file and relationships and records
the deletion metadata. Approval and deletion require explicit user
authorization.

## Repository health and index

```bash
eg --vault "$VAULT" check
eg --vault "$VAULT" check --strict --json
eg --vault "$VAULT" reconcile --dry-run --json
eg --vault "$VAULT" reconcile
eg --vault "$VAULT" index build
eg --vault "$VAULT" index status --strict --json
eg --vault "$VAULT" index sync
eg --vault "$VAULT" index rebuild
```

The index is optional. `eg search`, `eg card show`, and `eg rel` fall back to
authoritative Markdown when the index is stale (`W22`), missing (`W23`), or
corrupt (`W24`); the fallback is reported as `Q5`. Pagination truncation is
reported as `W25`. These read commands accept `--limit` and `--offset`.
`eg bench --json` reports `search_p95_ms`,
`card_show_p95_ms`, `rel_p95_ms`, `index_build_ms`, and
`index_incremental_ms`.

### 退出码表

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Invalid command or arguments; no write |
| 2 | Validation failed; no write |
| 3 | Some requested writes were skipped |
| 4 | Git commit or derived-index write failed |
| 5 | M6 已启用 `ExitPrecheckOrLock`: strict precheck `E15` or local lock `E16`; zero authoritative writes（零权威写入） |
| 6 | `eg proposal approve` or `eg delete` needs explicit confirmation; 权威 Markdown 完全不变 |

Confirmation is evaluated in this order: 参数错 `1` → 校验失败 `2` → 仅缺确认 `6`.

M6 已启用 `run.lock`、事务日志、崩溃恢复、块级安全合并和退出码 `5`。
The active M6 transaction diagnostics are `E15`, `E16`, `W26`, `W27`, and
`W28`. They cover strict validation, lock failure, crash recovery（崩溃恢复）,
block-level safety（块级安全合并）, and lock contention. `.index/txn/` is the
transaction journal（事务日志）. `W21` remains unassigned（不分配）.

## Command roster

Evergreen exposes 25 top-level commands（顶层命令共 25 条）. Run `eg <command> --help`
for the authoritative flags and exit codes; the table below is the post-install
roster you can verify against `eg --help`.

| Command | Purpose |
| --- | --- |
| `eg init` | Initialize a Git-backed vault |
| `eg config` | Read/write `default_domain` / `domains` |
| `eg capture` | Record a source and register the inbox |
| `eg context` | Read-only processing context (`draft_candidates` + `knowledge_candidates` + `opinion_candidates` + base hashes) |
| `eg apply` | Apply a ChangePlan |
| `eg search` | Ranked, paginated card search |
| `eg card` | `eg card show <k-id>`: single card view |
| `eg rel` | Argument relationships (and `--replaced-by` pointers) |
| `eg report` | `eg report --last`: replay the latest write report |
| `eg deprecate` | Retire a card |
| `eg restore` | Reactivate a retired card |
| `eg replaced-by` | Point a retired card/opinion at its replacement |
| `eg proposal` | Proposal state machine (`new`/`list`/`show`/`approve`/`reject`) |
| `eg delete` | Logical deletion (needs an approved proposal + `--confirm`) |
| `eg undelete` | Reverse a logical deletion |
| `eg mark-reviewed` | Record review state |
| `eg unreviewed` | List unreviewed artifacts |
| `eg edit` | Replace an explicitly authorized card section |
| `eg reconcile` | Full-vault reconcile (R1–R7, may write) |
| `eg check` | Read-only structural check (exactly 8 checks, zero writes) |
| `eg index` | Build/rebuild/status/sync the derived index |
| `eg bench` | Read-only performance sampling (five metrics) |
| `eg opinion` | Opinion subsystem: `search`/`show` (read-only) + `validate`/`reject` (user-initiated validation lifecycle, `--user-request` mandatory, single-file `verb=process` commit) |
| `eg materialize` | User-initiated deterministic Note candidate materialization |
| `eg export` | `eg export --plain`: plain Markdown egress outside the vault |

## Runtime directories

- `.eg/` stores the latest report and is excluded through
  `.git/info/exclude`.
- `.index/` stores the derived database, deterministic per-Note candidate
  sidecars under `blocks/`, `run.lock`, and transaction state. The sidecars
  contain only Markdown-recomputable identities, spans, mappings, and hashes;
  unhealthy sidecars make candidate queries fall back to Markdown.
- Build output under `bin/` and `dist/` is ignored by the source repository.

An index database can be rebuilt. However, **存在未闭合事务时不可删
`.index/`**, because `.index/txn/` contains the before-images required for
recovery.

## Platform support

| Target | Status |
| --- | --- |
| `linux/amd64` | Fully exercised by the original acceptance suite |
| `linux/arm64` | 仅交叉编译，原生运行验证未做 |
| `eg_darwin_amd64` | 仅交叉编译产出，未经真机运行验证 |
| `eg_darwin_arm64` | 仅交叉编译产出，未经真机运行验证 |

本项目均**不**声称 darwin 产物通过了 macOS 真机验证。

The `run.lock` implementation provides local-process coordination. It does not
promise atomic locking semantics on a 网络盘 or 同步盘.
