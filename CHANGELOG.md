# Changelog

All notable changes to Evergreen will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and published versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- Public project documentation, contribution guidance, security policy, and
  automated CI and release workflows.
- Opinion as a first-class learning entity (`o-*`) under
  `domains/<domain>/opinions/`, alongside Source/Note/Knowledge, with a
  five-section template and a user-initiated `validation` lifecycle
  (`pending`/`validated`/`rejected`). Knowledge and Opinion are separate: stable
  definitions/steps/conditions/data are Knowledge; evaluations, causal or
  predictive claims and trade-offs are Opinion.
- Canonical ChangePlan operations `create_knowledge` / `append_knowledge` /
  `create_opinion` / `append_opinion` on the main pipeline (now nine canonical
  ops). `write_note` gains the canonical `blocks[]` / `omissions[]` /
  `extraction_coverage[]` shape.
- `eg search --kind knowledge|opinion|all`: search defaults to `--kind
  knowledge`; `opinion` or `all` widen the result set.
- Storage v3 Note candidates with deterministic Knowledge/Opinion
  materialization through `eg materialize`, plus read-only plain Markdown
  egress through `eg export --plain`.
- Rebuildable `.index/blocks/<n-id>.json` candidate sidecars with deterministic
  build/sync, strict Markdown reconciliation, and scan fallback for `eg context`.

### Changed

- ChangePlan `plan_version` is now `2` for current plans; the supported set is
  `{1, 2}`. This is an **additive schema change with compatibility**, not a
  break: a `plan_version: 1` plan is still accepted and flagged with a single
  `I1`.
- **Breaking (derived storage schema only):** the derived index
  `schema_version` is now `2`. There is no incremental migration — a
  `schema_version` mismatch discards and rebuilds the local `.index/` database
  from Markdown. No authoritative data migration is required, because Markdown
  remains the source of truth and the index is a rebuildable accelerator. Table
  count stays at six; Knowledge and Opinion share `cards`/`cards_fts` via a
  `kind` column plus opinion `validation`.
- **Breaking:** `eg context` now returns `draft_candidates`,
  `knowledge_candidates`, and `opinion_candidates`; the deprecated legacy
  `candidates` alias and its unconditional deprecation `I1` are removed in
  `0.8.0-m8`.
- The Knowledge template is now three sections (知识内容 / 条件与边界 / 用户补充);
  the legacy v1 sections 解释与依据 / 理解自检 are preserved verbatim as
  compatibility sections, never rewritten. The legacy v1 Note sections
  (材料提炼 / Agent 分析 / 产出知识卡) are likewise mapped/preserved for existing
  vaults and are no longer the current fixed template.
- Prepared the existing `0.6.0-m6` implementation for a public source release.
- Bumped `modernc.org/sqlite` to `1.58.0`, which raises the minimum required
  Go toolchain to `1.25`.
- Bumped the pinned GitHub Actions (`checkout`, `setup-go`, `setup-python`) to
  their current major versions.

### Compatibility

- `plan_version: 1` plans are still accepted and flagged with a single `I1`
  migration info.
- `create_card` / `append_card` remain accepted as aliases, normalized to
  `create_knowledge` / `append_knowledge` with an `I1`; new plans should use the
  canonical ops.
- `plan_version: 1` and the `create_card` / `append_card` aliases remain
  supported as described above; this compatibility does not restore the
  removed `eg context` `candidates` field.

### Fixed

- Retried a transient Darwin `openat` failure when concurrent writers create
  the vault lock file for the first time.

## 0.6.0-m6

- Added atomic multi-file transactions, a local write lock, crash recovery,
  strict pre-write validation, and exit code `5`.
- Added a rebuildable SQLite/FTS5 index with scan fallback.
- Added reconciliation, lifecycle, proposal, review, and relationship commands.
