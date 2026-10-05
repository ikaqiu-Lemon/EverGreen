# Note Segmentation Workspace Contract

- Date: 2026-10-04
- Status: frozen for implementation
- Owner: `evergreen/note_segmentation_workspace`
- Supersedes for new writes:
  - candidate drafts embedded in the `## 提取结果` section of `n-*`
  - treating one Markdown file as both the learning Note and the K/O review workspace
- Preserves:
  - Markdown and Git authority
  - journal v1 atomic writes
  - deterministic materialization without model or network calls
  - existing Knowledge/Opinion schemas and directories

## 1. Problem and decision

The current Storage v3 Note stores two representations in one file:

1. a complete learning Note under `## 整理正文`;
2. a second candidate representation under `## 提取结果`.

That makes one `n-*` appear to contain two articles and forces K/O review
syntax into the user's normal reading and editing surface.

The new canonical chain is:

```text
s-* source
  -> n-* learning Note
  -> ns-* segmentation workspace
  -> k-* Knowledge / o-* Opinion
```

The two editable Markdown files have disjoint authority:

| File | Human purpose | Authoritative fields |
| --- | --- | --- |
| `n-*` | read and improve one coherent Note | learning body, source/agent blocks, open questions, user additions |
| `ns-*` | review and improve the K/O partition | candidate boundaries, kind, title, slug, payload, coverage and output mapping |

Neither file is a cache of the other. `ns-*` is generated from a specific
hash of `n-*`, then becomes an independently editable review workspace.
Changing `n-*` never silently overwrites `ns-*`; it only makes the workspace
stale until an explicit rebase is applied.

## 2. Identity and layout

Add a fifth learning artifact ID:

```text
ns-<yyyymmdd>-<slug>
```

- prefix: `ns-`
- model type: `NoteSegmentationID`
- one canonical workspace per Note
- default ID: replace the parent Note's leading `n-` with `ns-`
- directory:

```text
domains/<domain>/note-segments/<ns-id>.md
```

The directory and prefix jointly identify the artifact type. A workspace is
not returned as a normal Note and does not participate in Knowledge/Opinion
search.

The canonical frontmatter is:

```yaml
---
id: ns-20261004-example
note: n-20261004-example
note_hash: sha256:<64 lowercase hex>
title: Example
created_at: '2026-10-04'
updated_at: '2026-10-04T10:00:00+08:00'
---
```

Required keys are `id`, `note`, `note_hash`, `created_at`, and `updated_at`.
`title` and `tags` are optional. There is no lifecycle `status`.

The parent Note remains:

```yaml
---
id: n-20261004-example
source: s-20261004-example
...
---
```

`n-*` does not point back to `ns-*`; the child owns the relationship. This
keeps the learning Note free of segmentation metadata and prevents a missing
workspace from making the Note structurally invalid.

## 3. Canonical Markdown shapes

### 3.1 Learning Note

New `plan_version: 3` Notes have exactly three canonical H2 sections:

```markdown
## 整理正文

<source and agent review blocks in original order>

## 存疑与待验证

## 用户补充
```

They contain no:

- Evergreen machine anchors of any family, including `eg:nr:*`;
- candidate H3 attributes;
- `[Knowledge Candidate]` / `[Opinion Candidate]` labels;
- candidate coverage matrix;
- K/O output list;
- `## 提取结果` section.

Legacy `n-*` files containing `eg:nr:1` review anchors remain readable only
for compatibility and migration. The canonical v3 writer never emits machine
metadata into `n-*`.

### 3.2 Segmentation workspace

An `ns-*` file has two canonical H2 sections:

```markdown
## 划分结果

<candidate blocks and coverage>

## 用户补充
```

`划分结果` is the primary content, not a tail appended after another copy of
the Note. Every candidate appears exactly once. `用户补充` is user-owned and
never written by the automatic path.

Canonical candidates use:

- one `eg:nb:1` note-block manifest before all candidates;
- `eg:cd:2` candidate anchors;
- `eg:cc:2` coverage anchors;
- the existing H3/L2 structural boundary syntax;
- visible `[Knowledge Candidate]` / `[Opinion Candidate]` labels;
- existing Knowledge/Opinion H4 templates and logical slug rules.

The v2 candidate anchor replaces `source_refs` with `note_refs`:

```json
{
  "note_refs": ["B1", "B2"],
  "rel": "support",
  "reason": "the referenced Note blocks support this candidate",
  "tags": ["example"],
  "output": ""
}
```

`eg:cd:1` remains the legacy embedded-Note protocol and is never emitted into
new `ns-*` files.

## 4. Whole-Note reference and coverage

During `write_note`, the validator assigns every ordered input block a
version-local reference:

```text
B1, B2, ... Bn
```

The order includes both source and agent blocks. The complete ordered
vocabulary and its role/provenance/content digest are stored in one
`eg:nb:1 <base64url(JSON)>` manifest in `ns-*`; no block identifier is written
to `n-*`. These references are valid only together with the `note_hash`
recorded by `ns-*`; they are not stable IDs across Note edits.

Each candidate lists one or more `note_refs`. Coverage partitions the full
set `{B1..Bn}` with no gaps:

- `candidate`: represented by one or more candidate keys;
- `note_only`: intentionally retained only in the Note;
- `unresolved`: requires review and blocks `materialize --all`.

Every Note block appears in exactly one coverage module. Coverage may map one
module to multiple candidates, but duplicate module references or overlapping
block coverage fail closed.

This makes "partition the whole Note" a machine-checkable invariant rather
than an assertion based only on original Source line ranges.

## 5. Generation contract

`plan_version: 3` is the canonical write format.

`write_note` continues to accept ordered Note `blocks[]` and omissions. Its
candidate fields become segmentation inputs:

- `candidate_drafts[].note_refs`
- `candidate_coverage[].note_refs`
- optional `segmentation_id`

One validated `write_note` operation creates, in one journal-v1 write-set:

1. the pure `n-*`;
2. the corresponding `ns-*`;
3. the inbox update.

The validator renders the final `n-*` bytes first, computes their content
hash, and writes that exact hash into the `ns-*` frontmatter. No model or
network call occurs inside `eg`.

`plan_version: 2` preserves its existing embedded-candidate behavior for
compatibility. It is readable but no longer emitted by the current SKILL main
path.

## 6. User editing and authority

Both files may be edited by the user:

- edits to `n-*` change the learning Note;
- edits to `ns-*` change the segmentation and future K/O payloads.

There is no automatic reverse synchronization from `ns-*` to `n-*`.
Candidate text may intentionally become more self-contained than the Note
paragraphs from which it was derived.

Direct `ns-*` edits must still satisfy its parser:

- candidate kind and visible label agree;
- candidate H4 template is valid;
- `note_refs` exist in the recorded parent Note version;
- coverage is complete and non-overlapping;
- output mappings do not drift.

## 7. Freshness and explicit rebase

Workspace freshness is derived:

```text
fresh := ns.note_hash == content_hash(current n bytes)
```

If false:

- `candidate show` succeeds and reports `stale: true`;
- `candidate apply` without explicit rebase fails with zero writes;
- `materialize` fails with zero writes;
- index/context expose the stale state and continue treating Markdown as
  authority.

Candidate review schema advances to version 2:

```json
{
  "schema_version": 2,
  "note": "n-20261004-example",
  "note_path": "domains/demo/notes/n-20261004-example.md",
  "note_hash": "sha256:<current note>",
  "workspace": "ns-20261004-example",
  "workspace_path": "domains/demo/note-segments/ns-20261004-example.md",
  "workspace_hash": "sha256:<current workspace>",
  "workspace_note_hash": "sha256:<frontmatter note_hash>",
  "stale": false,
  "candidates": [],
  "coverage": []
}
```

`candidate apply --rebase --user-request` is the only operation that may
advance `workspace_note_hash` to the current `note_hash`.

Rebase is full-state and optimistic:

1. `note_path`, `note_hash`, `workspace_path`, and `workspace_hash` must all
   match disk;
2. the submitted candidates and coverage must validate against the
   `{B1..Bn}` vocabulary stored in the workspace's `eg:nb:1` manifest;
3. the complete submitted workspace becomes the target state;
4. bytes outside the managed `划分结果` region, including `用户补充`, remain
   unchanged;
5. one journal-v1 transaction and one Git commit update the workspace.

The CLI never regenerates or merges semantic text. The review spec starts from
the current workspace, so user edits are preserved unless the submitting
caller explicitly changes or removes them. If old and new Note content cannot
be reconciled confidently, the caller keeps the affected coverage as
`unresolved`; materialization remains blocked. This is the fail-closed conflict
state.

For a fresh workspace, normal `candidate apply --user-request` remains valid
and does not require `--rebase`. Supplying `--rebase` when already fresh is
rejected as a usage error.

## 8. Materialization

The user-facing selector remains:

```text
eg materialize --note <n-id> (--candidate <cand-key> | --all) --user-request
```

The command resolves the Note's single `ns-*` child and treats that workspace
as the only candidate authority.

Before creating K/O it verifies:

- parent Note and workspace both parse;
- workspace `note` points to the requested Note;
- workspace `note_hash` equals current Note content hash;
- candidate and coverage invariants hold;
- existing output mappings and target artifacts have not drifted.

K/O `sources[]` gains optional `segmentation`:

```yaml
sources:
  - source: s-20261004-example
    note: n-20261004-example
    segmentation: ns-20261004-example
    rel: support
    reason: ...
```

Existing artifacts without `segmentation` remain valid. New workspace
materialization always writes it, making the complete provenance chain
explicit.

Materialization copies exact H4 payload bytes from `ns-*`; it never reads
candidate payload from `n-*` and never calls a model.

Mapped candidates remain in `ns-*` with their `output` fields so the user's
reviewed text and audit trail are not destroyed. Final coverage may coexist
with mapped candidates. Mapped candidate payloads are immutable under normal
review apply; further revisions require a new candidate key/output.

## 9. Legacy migration

Add:

```text
eg candidate migrate --note <n-id> --user-request
```

It accepts either a legacy, unmaterialized `n-*` containing `eg:cd:1`
candidates and draft coverage, or an already split `n-* + ns-*` pair whose
Note still contains legacy `eg:nr:1` block anchors.

In one journal-v1 transaction it:

1. removes the complete `提取结果` section from `n-*`;
2. preserves every other Note byte and user-owned section;
3. creates `ns-*` with equivalent candidates and coverage;
4. converts legacy `source_refs` to `note_refs` by matching the referenced
   source blocks in `整理正文`;
5. records the hash of the target pure Note;
6. commits both files once.

For an already split pair whose `n-*` still contains `eg:nr:1`,
the same command atomically strips those anchors, inserts the equivalent
`eg:nb:1` manifest into the existing `ns-*`, preserves candidate and user
bytes, and advances `note_hash` only when the workspace was fresh before
migration.

Ambiguous references, conflicting block metadata, non-empty candidate
outputs in a legacy embedded Note, finalized legacy coverage, or any B3
mismatch fail with zero writes. Repeating a completed migration is a no-op.

Already-materialized legacy Notes remain readable through the old path and are
not automatically rewritten.

## 10. Query, index, and export

- `context --note <n-id>` returns the Note plus its workspace ID/path,
  freshness, candidates and coverage.
- Draft candidate sidecars project `ns-*`, not `n-*`; their schema advances
  independently.
- Missing/stale/corrupt sidecars still fall back to authoritative Markdown.
- Normal Note scans and user-facing Note lists include only `n-*`.
- `plain export` exports pure `n-*`, K/O and other user artifacts normally.
  It excludes `note-segments/` by default to avoid recreating duplicate
  articles in the plain reading tree.

## 11. Authorization and atomicity

- automatic `plan_version: 3 write_note` may create the initial `n-* + ns-*`;
- `candidate show` is read-only;
- `candidate apply`, `candidate migrate`, rebase and materialize require
  `--user-request`;
- B3 optimistic concurrency applies independently to both Note and workspace;
- any multi-file migration or materialization uses one journal-v1 write-set;
- Git failure preserves published Markdown and reports exit 4;
- lock/precheck failure reports exit 5 with zero authoritative writes.

## 12. Acceptance matrix

1. A new source produces one pure `n-*` and one linked `ns-*`.
2. The Note contains no Evergreen machine anchors, candidate labels,
   candidate attributes, candidate coverage or duplicate extraction prose.
3. Workspace coverage accounts for every source and agent block in the Note.
4. A user may edit either file without the other being silently rewritten.
5. A Note edit makes the workspace stale and blocks materialization.
6. Explicit rebase preserves submitted workspace edits and updates only the
   managed segmentation region plus `note_hash`.
7. Unresolved rebase conflicts remain visible and block `--all`.
8. Materialization creates exact K/O payloads with `s → n → ns → k/o`
   provenance and is idempotent.
9. Legacy unmaterialized embedded candidates migrate atomically; existing
   materialized Notes and K/O remain readable.
10. Focused unit/E2E, manifest, lint, public/sensitive and applicable aggregate
    gates pass without skip, allowlist or weakened assertions.
