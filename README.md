<div align="center">

# Evergreen

**A deterministic, local-first knowledge base for humans and coding agents — shipped as a single binary.**

Your notes stay as plain Markdown in your own Git repository. Every change is a reviewable commit.
No model calls. No network requests. No telemetry.

[![Release](https://img.shields.io/github/v/release/ikaqiu-Lemon/EverGreen?include_prereleases&sort=semver)](https://github.com/ikaqiu-Lemon/EverGreen/releases)
[![CI](https://github.com/ikaqiu-Lemon/EverGreen/actions/workflows/ci.yml/badge.svg)](https://github.com/ikaqiu-Lemon/EverGreen/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/dl/)
[![Offline](https://img.shields.io/badge/network-zero%20requests-success)](PRIVACY.md)

**English** · [简体中文](README.zh-CN.md)

</div>

---

## What is Evergreen?

Evergreen (`eg`) is a command-line knowledge base built around one idea: **the notes you own must
outlive the tool that writes them.**

Most AI note-taking tools put your knowledge inside a proprietary database, then let a model write
into it freely. Evergreen inverts both halves:

- **Markdown is the only source of truth.** Human-readable files with YAML frontmatter, in a Git
  repository you control. The SQLite index is a disposable accelerator that can be deleted and
  rebuilt at any time.
- **A model can never write directly.** All programmatic writes must go through a validated
  *ChangePlan*. Evergreen checks it, applies only the authorized byte spans, and records exactly one
  Git commit per operation.

The result is a knowledge base an agent can safely operate at scale, and a human can still read,
diff, review, and `git blame` five years from now.

```console
$ eg search compute
状态：completed（exit_code=0，ok=true）
search：query="compute"，命中 1 张卡（扫描 1 个 .md，跳过 0 个；只读，零写入零 commit）
分页：--limit=50 --offset=0，本页 1 条 / 共 1 条（截断=false）
k-20260915-bitter-lesson  General methods that scale with compute beat hand-coded knowledge  [ai-infra] ai,method  2026-09-15T14:06:50+08:00
```

> [!NOTE]
> **The human-readable renderer currently emits Chinese only.** English localization is not
> implemented yet, so the transcripts in this README are shown verbatim as the CLI prints them.
> Every command also accepts `--json`, which returns a language-neutral, stable envelope — that is
> the interface agents and scripts should use.

### Why not just a folder of Markdown files?

A plain folder has no notion of *why* a claim is believed, what it contradicts, or whether it is
still valid. Evergreen adds a small, closed set of structure on top of Markdown — sources,
evidence links, argument relationships, lifecycle state — and then **enforces** it, so an agent
cannot quietly produce an unsourced claim or a dangling reference.

### Why not a general agent memory / RAG store?

Those optimize for retrieval. Evergreen optimizes for **review**. Its guarantees are about what
gets written: byte-preserving edits, atomic multi-file transactions, explicit user authorization for
destructive operations, stable exit codes, and a full audit trail in Git.

---

## Table of contents

- [Highlights](#highlights)
- [How it works](#how-it-works)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Core concepts](#core-concepts)
  - [The vault](#the-vault)
  - [Four learning entities and a control plane](#four-learning-entities-and-a-control-plane)
  - [Entity templates](#entity-templates)
  - [Relationships](#relationships)
  - [The ChangePlan](#the-changeplan)
- [Using Evergreen with a coding agent](#using-evergreen-with-a-coding-agent)
- [Command reference](#command-reference)
- [Reading: sorting, pagination, and fallback](#reading-sorting-pagination-and-fallback)
- [Safety model](#safety-model)
- [Exit codes and diagnostics](#exit-codes-and-diagnostics)
- [The derived index](#the-derived-index)
- [Performance](#performance)
- [Development](#development)
- [Platform support](#platform-support)
- [FAQ](#faq)
- [Project information](#project-information)

---

## Highlights

| | |
| --- | --- |
| 🗂 **Local-first** | Your vault is a directory you choose. Nothing leaves the machine. |
| 📝 **Markdown is authoritative** | Every fact lives in readable `.md` with YAML frontmatter. The index is derived and disposable. |
| 🔍 **Reviewable by design** | One operation = one Git commit, with a typed verb (`capture`, `process`, `relate`, `delete`, …). |
| ✂️ **Byte-preserving writes** | Evergreen rewrites only the authorized span. It never re-serializes your YAML, so unknown keys, comments, and formatting survive. |
| 🤖 **Agent-safe** | A single validated write channel; destructive operations require an approved proposal *and* explicit user confirmation. |
| 🔒 **Crash-safe** | Vault-local `flock`, a transaction journal with before-images, and automatic recovery of interrupted writes. |
| 🎯 **Deterministic** | Total ordering on every list, stable JSON envelopes, and a fixed exit-code contract — diffable and scriptable. |
| 🚫 **Zero network** | No model calls, no URL fetching, no telemetry, no auto-update. `CGO_ENABLED=0`, no C runtime dependency. |

---

## How it works

Evergreen deliberately does **not** fetch or interpret content. Your agent does the semantics;
Evergreen owns the disk.

```text
   ┌──────────────────────── your agent (or you) ────────────────────────┐
   │  fetch page · clean text · decide meaning · decide relationships    │
   └───────────────┬─────────────────────────────────────┬───────────────┘
                   │ ① body text                         │ ③ ChangePlan (JSON)
                   ▼                                     ▼
        ┌──────────────────┐   ② context      ┌────────────────────────┐
        │   eg capture     │ ───────────────▶ │   eg apply --plan      │
        │ store raw source │  knowledge +     │  validate → authorize  │
        └────────┬─────────┘  opinion cands.  │  → write spans → commit│
                 │            + content hashes└───────────┬────────────┘
                 ▼                                        ▼
        ┌─────────────────────────────────────────────────────────────┐
        │   your vault = a Git repo of Markdown (source of truth)      │
        │   sources/ · <d>/notes/ · <d>/knowledge/ · <d>/opinions/     │
        └───────────────┬─────────────────────────────────┬───────────┘
                        │ rebuildable                     │ read-only
                        ▼                                 ▼
              ┌──────────────────┐          ┌──────────────────────────┐
              │ .index/ SQLite   │          │ eg search / card show /  │
              │ FTS5 + txn log   │ ───────▶ │ rel  (falls back to      │
              └──────────────────┘          │ Markdown if unhealthy)   │
                                            └──────────────────────────┘
```

Two properties fall out of this shape:

1. **The index can always be thrown away.** If it is missing, stale, or corrupt, reads transparently
   fall back to scanning Markdown and say so in `warnings[]`. "The index isn't built" is never a
   reason for a read to fail.
2. **A failed write leaves no half-state.** Multi-file writes are staged in a journal and committed
   atomically; the next write detects and recovers an interrupted transaction before proceeding.

---

## Installation

### Option 1 — Prebuilt binary (recommended)

Binaries for Linux and macOS are attached to each [release](https://github.com/ikaqiu-Lemon/EverGreen/releases).

```console
$ VER=v0.8.0-m8
$ OS=$(uname -s | tr '[:upper:]' '[:lower:]')            # linux | darwin
$ ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

$ curl -fsSLO "https://github.com/ikaqiu-Lemon/EverGreen/releases/download/$VER/eg_${OS}_${ARCH}"
$ curl -fsSLO "https://github.com/ikaqiu-Lemon/EverGreen/releases/download/$VER/SHA256SUMS"

# always verify before trusting the binary
$ grep "eg_${OS}_${ARCH}" SHA256SUMS | sha256sum -c -    # macOS: shasum -a 256 -c -
eg_linux_amd64: OK

$ chmod +x "eg_${OS}_${ARCH}"
$ sudo mv "eg_${OS}_${ARCH}" /usr/local/bin/eg
$ eg --version
eg 0.8.0-m8 (commit …, built …)
```

Each release also ships `PROVENANCE.txt`, which binds the release version and source commit to the
artifact checksums.

### Option 2 — `go install`

```console
$ go install github.com/ikaqiu-Lemon/EverGreen/cmd/eg@latest
```

### Option 3 — Build from source

```console
$ git clone https://github.com/ikaqiu-Lemon/EverGreen.git
$ cd EverGreen
$ make build          # native bin/eg + cross-compiled dist/
$ ./bin/eg --version
eg 0.8.0-m8 (commit …, built …)
```

**Requirements:** Go 1.25+, Git, GNU Make. Python 3 with PyYAML is needed only to run the
manifest-driven test suite. All builds use `CGO_ENABLED=0`.

See [INSTALL.md](INSTALL.md) for offline builds (`GOPROXY=off`), release packaging, and checksum
verification details.

---

## Quick start

A vault is just a Git repository holding your knowledge. Evergreen creates and manages it for you.

### 1. Create a vault

```console
$ export VAULT="$HOME/notes/evergreen"

$ eg --vault "$VAULT" init --domain ai-infra
$ eg --vault "$VAULT" config set default_domain ai-infra
```

`eg init` creates the skeleton and makes the first commit:

```console
状态：completed（exit_code=0，ok=true）
vault：/home/you/notes/evergreen
新建 7 项，已存在 0 项
commit：init(ai-infra): 初始化 vault 骨架
```

It also writes `SKILL.md` — the operating contract for agents — into the vault, byte-identical to
the copy embedded in the binary, so the tool and its rules are always the same version.

> **Tip:** once a vault exists you can drop `--vault`; Evergreen walks up from the current directory
> looking for `evergreen.yml`.

### 2. Capture a source

Evergreen never fetches URLs. You (or your agent) supply already-cleaned body text.

```console
$ cat > /tmp/body.txt <<'EOF'
Rich Sutton argues that general methods leveraging computation ultimately outperform methods that build in human domain knowledge.
Chess, Go, speech recognition and computer vision all showed the same pattern: handcrafted knowledge led early, then compute-driven methods overtook it.
The long-run bet is on scalable search and learning, not on encoding what humans already know.
EOF

$ eg capture \
    --url https://example.com/bitter-lesson \
    --title "The Bitter Lesson" \
    --body-file /tmp/body.txt \
    --reason "capture the long-term compute-vs-knowledge conclusion"
```

```console
状态：completed（exit_code=0，ok=true）
原文：s-20260915-the-bitter-lesson（sources/s-20260915-the-bitter-lesson.md）
commit：capture(ai-infra): 收录 s-20260915-the-bitter-lesson
数据：
  deduped: false
  has_note: false
  path: sources/s-20260915-the-bitter-lesson.md
  txn_id: t0000000000000001
```

`deduped: true` means the URL or title already existed — Evergreen reuses that source and appends
your `--reason` instead of creating a duplicate. Keep the input file **outside** the vault; `eg
capture` copies its content into `sources/`.

### 3. Get context for processing

```console
$ eg context --source s-20260915-the-bitter-lesson --json
```

This read-only call returns the source body, similar existing entities, and — most importantly —
`base`, a map of file → `content_hash`. `draft_candidates` reports candidate summaries embedded in
Notes, including their draft/materialized state, persistent output ID, and exact payload hash.
Already-materialized entities remain separate in `knowledge_candidates` (Knowledge `k-*`) and
`opinion_candidates` (Opinion `o-*`). Version 0.8.0-m8 removes the legacy `candidates` alias and its
deprecation `I1`. The two materialized candidate lists feed the plan's `base`:

```json
{
  "ok": true,
  "data": {
    "base": {
      "unprocessed.md": "sha256:49d70d685f07d2b18acfaa97893565bd6e7c252a6a0deb43ec1c94a35ae656f5"
    },
    "draft_candidates": [],
    "knowledge_candidates": [],
    "opinion_candidates": [],
    "cards": [],
    "notes": [],
    "proposals": [],
    "domain": "ai-infra",
    "default_domain": "ai-infra",
    "default_domain_fallback": true,
    "source": {
      "id": "s-20260915-the-bitter-lesson",
      "path": "sources/s-20260915-the-bitter-lesson.md",
      "title": "The Bitter Lesson",
      "url": "https://example.com/bitter-lesson",
      "saved_at": "2026-09-15T14:06:32+08:00",
      "body": "…"
    }
  },
  "warnings": [ … ],
  "exit_code": 0,
  "status": "completed"
}
```

Those hashes are an **optimistic-concurrency token**: copy them verbatim into your ChangePlan. If a
file changed underneath you, Evergreen skips that file instead of clobbering it.

Storage v3 plans save Knowledge/Opinion drafts inside the Note with
`write_note.candidate_drafts[]` and the separate `candidate_coverage[]` matrix. The agent may save
and revise those drafts, but it must not materialize them. Step 4 shows the explicit user
materialization after the candidate plan has been applied. A plain export is available independently:

```console
$ eg export --plain --output ../evergreen-plain
```

`eg materialize` performs no model or network call. It validates and copies exact candidate bytes,
writes `k-*` under `knowledge/` and `o-*` under `opinions/` (`validation: pending`), and commits the
targets plus the Note mapping in one journal-v1 transaction. `eg export --plain` is read-only with
respect to the vault and removes only Evergreen machine anchors, candidate attributes, and fenced-div
boundary lines from the exported Markdown.

Most candidates use the H3 boundary rendered by `candidate_drafts[]`. When heading structure cannot
express the boundary, Evergreen also reads this exact L2 fallback:

```markdown
<!-- eg:cd:1 <base64url(JSON)> -->
:::: {#cand-cross-section .eg-candidate .opinion}
### A claim spanning sections

#### 观点

The exact candidate payload.
::::
```

The anchor, opener, and title must be three adjacent lines. The opener has at least three colons and
exactly one `cand-*` ID plus `.eg-candidate` and either `.knowledge` or `.opinion`; the first inner
line is a non-empty attribute-free ATX H3 title. The closing fence has no attributes and at least as
many colons as the opener. L2 candidates cannot nest. Code fences take precedence, while headings,
lists, tables, and other Markdown inside the container remain candidate content. Materialization uses
the same H4 template mapping as H3 candidates, and plain export removes only the two fence lines while
retaining the title and payload.

### 4. Write through a ChangePlan

The captured source persists with a template blank as body line `L1`, so its three real content
lines are `L2`, `L3`, `L4`. Save this as `plan.json`, pasting in the `base` hash from step 3:

```json
{
  "plan_version": 2,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "save The Bitter Lesson as a review Note with one knowledge and one opinion candidate",
  "requirement_ids": ["EG-KNW-04"],
  "convergence": [],
  "base": { "unprocessed.md": "sha256:879b6ef5…" },
  "ops": [
    {
      "op": "write_note",
      "source": "s-20260915-the-bitter-lesson",
      "note_id": "n-20260915-bitter-lesson",
      "title": "The Bitter Lesson (Rich Sutton, 2019)",
      "blocks": [
        { "role": "source", "source_ref": "L2-L2", "heading": "Claim",   "body": "Rich Sutton argues that general methods leveraging computation ultimately outperform methods that build in human domain knowledge." },
        { "role": "source", "source_ref": "L3-L3", "heading": "Evidence", "body": "Chess, Go, speech recognition and computer vision all showed the same pattern: handcrafted knowledge led early, then compute-driven methods overtook it." },
        { "role": "source", "source_ref": "L4-L4", "heading": "Bet",      "body": "The long-run bet is on scalable search and learning, not on encoding what humans already know." },
        { "role": "agent",  "annotation": "summary", "body": "The definition and its scope are stable knowledge; the economic bet is a value judgement, so it becomes an opinion." }
      ],
      "omissions": [],
      "candidate_drafts": [
        {
          "key": "cand-general-methods",
          "kind": "knowledge",
          "title": "General methods scale with compute",
          "source_refs": ["L2-L2", "L3-L3"],
          "rel": "support",
          "reason": "the article grounds the claim in four domains of history",
          "tags": ["ai", "method"],
          "sections": [
            { "name": "知识内容", "body": "Methods built on search and learning can turn growing compute into capability.\n" },
            { "name": "条件与边界", "body": "The task must admit large-scale search or learning, and available compute must keep growing.\n" }
          ]
        },
        {
          "key": "cand-scale-bet",
          "kind": "opinion",
          "title": "Encoding human knowledge is a losing long-run bet",
          "source_refs": ["L4-L4"],
          "rel": "support",
          "reason": "the article's closing bet",
          "tags": ["ai", "research-direction"],
          "sections": [
            { "name": "观点", "body": "Investing in scalable search and learning beats investing in hand-coded domain knowledge.\n" },
            { "name": "论据与推理", "body": "Compute keeps getting cheaper, so scalable methods raise their ceiling over time.\n" },
            { "name": "待验证", "body": "Does this hold for tasks with scarce data or weak evaluation signals?\n" }
          ]
        }
      ],
      "candidate_coverage": [
        { "module": "knowledge", "source_refs": ["L2-L2", "L3-L3"], "summary": "Claim and its historical grounds become one reusable candidate.", "disposition": "candidate", "candidates": ["cand-general-methods"] },
        { "module": "opinion",   "source_refs": ["L4-L4"],          "summary": "The long-run bet is a value judgement, tracked as an opinion candidate.", "disposition": "candidate", "candidates": ["cand-scale-bet"] }
      ]
    }
  ]
}
```

`write_note` is the v2 canonical shape: `blocks[]` reproduce the source lines in order (each `source`
block cites the physical range it came from via `source_ref`), `omissions[]` records — explicitly,
even when empty — the lines you deliberately dropped. `candidate_drafts[]` stores complete
Knowledge/Opinion template payloads, while `candidate_coverage[]` classifies every source range as
`candidate`, `note_only`, or `unresolved`. The candidate path omits `output_cards` and final
`extraction_coverage`; it never predicts a `k-*` or `o-*` ID.

Always dry-run first — it runs full validation with **zero writes**:

```console
$ eg apply --plan plan.json --dry-run
```

```console
状态：completed（exit_code=0，ok=true）
--dry-run：零写入、零 commit，以下是将写入的清单
知识卡：新建 0 张，复用 0 张，补充 0 张
关系：材料 0 条，论证 0 条
写入文件 2 个：domains/ai-infra/notes/n-….md、unprocessed.md
commit：无（未产生 commit 或提交失败；磁盘保留当前状态，未做任何还原）
```

Then apply for real:

```console
$ eg apply --plan plan.json
```

```console
状态：completed（exit_code=0，ok=true）
材料笔记：n-20260915-bitter-lesson（领域 ai-infra，domains/ai-infra/notes/n-….md，重新加工 false）
知识卡：新建 0 张，复用 0 张，补充 0 张
关系：材料 0 条，论证 0 条
写入文件 2 个：domains/ai-infra/notes/n-….md、unprocessed.md
commit：842c8ddb03a2bbe2eab7c5a99f4495fe1979ba17
```

After reviewing or editing the candidate payloads, the user materializes them explicitly:

```console
$ eg materialize --note n-20260915-bitter-lesson --all --user-request
```

That transaction creates one `k-*` and one `o-*` (`validation: pending`) and records their persistent
mapping in the Note. Repeating the command on a later date is a no-op when the mappings and payloads
still match.

### 5. Read it back

```console
$ eg search compute                          # ranked, paginated Knowledge search (default --kind knowledge)
$ eg card show k-20260915-bitter-lesson      # one Knowledge card: three sections + sources + both link directions
$ eg opinion show o-20260915-scale-bet       # one Opinion: five sections + validation + relation groups
$ eg rel k-20260915-bitter-lesson            # argument relationships, outbound and inbound
$ eg report --last                           # replay the last write report, read-only
```

`eg rel` always reports both directions separately, and says which file each edge is stored in:

```console
$ eg rel k-20260915-bitter-lesson
状态：completed（exit_code=0，ok=true）
rel：k-20260915-bitter-lesson 的正向 0 条 / 反向 1 条（扫描 2 个 .md，跳过 0 个；只读，零写入零 commit）
正向关系 relations_out[]：无
反向关系：k-20260915-attention-parallelism --supports--> k-20260915-bitter-lesson  理由：…（来源卡：domains/ai-infra/knowledge/k-20260915-attention-parallelism.md）
分页：--limit=50 --offset=0，本页 关系条目 1 条 / 共 1 条（截断=false；limit 是本次返回条数的全局上限）
```

The card on disk is exactly what you would have written by hand — a Knowledge card has three fixed
sections:

```markdown
---
id: 'k-20260915-bitter-lesson'
title: 'General methods that scale with compute beat hand-coded knowledge'
status: 'active'
created_at: '2026-09-15'
updated_at: '2026-09-15T14:06:50+08:00'
sources:
  - source: 's-20260915-the-bitter-lesson'
    note: 'n-20260915-bitter-lesson'
    rel: 'support'
    reason: 'the article grounds the claim in four domains of history'
tags:
  - 'ai'
  - 'method'
---

## 知识内容

Methods built on search and learning that scale with compute outperform methods that encode human domain knowledge directly.

## 条件与边界

Assumes compute keeps growing and the task admits large-scale search or learning.

## 用户补充
```

### 6. Optional: build the index and check health

```console
$ eg index build     # build the SQLite/FTS5 accelerator
$ eg check           # read-only structural check (7 checks, zero writes)
$ eg reconcile       # full-vault reconciliation (R1–R7, may write)
```

---

## Core concepts

### The vault

```text
evergreen.yml                     # domains + default_domain (the only config)
SKILL.md                          # agent operating contract, written by eg init
unprocessed.md                    # inbox of captured-but-unprocessed sources
sources/                          # s-*  raw captured material
domains/<domain>/notes/           # n-*  material notes (faithful to the source)
domains/<domain>/knowledge/       # k-*  knowledge cards (stable, reusable claims)
domains/<domain>/opinions/        # o-*  opinions (evaluations, causal/predictive judgements)
proposals/                        # p-*  control-plane: risky-operation proposals (on demand)
.index/                           # derived SQLite + run.lock + txn journal  (via .gitignore)
.eg/                              # last-report state    (via .git/info/exclude)
```

Everything above the last two lines is committed to Git. `.index/` and `.eg/` are runtime state,
excluded from the repository.

A **domain** is a top-level partition (`ai-infra`, `product`, …). Evergreen will *never* pick one for
you: if `default_domain` is unset, every command except `init` and `config` exits `1` and tells you
to configure it.

### Four learning entities and a control plane

Evergreen models a source through a fixed **Source → Note → {Knowledge, Opinion}** chain. Four
entities carry what you learn; `Proposal` is a separate control-plane object, not a learning entity:

| Prefix | Entity | Role |
| --- | --- | --- |
| `s-` | **Source** | Raw captured material. Immutable evidence, one file per source. |
| `n-` | **Note** | The complete, order-faithful body of one source, with removable annotations placed inline right next to the text they comment on. |
| `k-` | **Knowledge** | A stable definition / composition / step / condition / datum. Split, dedupe, and tidy only — never a multi-hop inference. This is what search returns by default. |
| `o-` | **Opinion** | An evaluation, causal or predictive claim, or a trade-off. Carries its argument, counter-examples, and validation lifecycle. **When you cannot tell whether something is Knowledge or Opinion, prefer Opinion.** |

`Proposal` (`p-*`) lives at the repository root and drives risky operations pending human approval; it
is a control surface and must never be treated as one of the four learning entities.

IDs are stable, human-readable, and date-prefixed (`k-20260915-bitter-lesson`). Two hard rules,
enforced in code: an `s-` ID may never appear in `relations`, and a `k-`/`o-` ID may never appear in
`sources`.

### Entity templates

Each learning entity has a fixed, ordered set of sections, compared byte-for-byte on disk:

**Knowledge — three sections:**

| Section | Contains | Who may write it |
| --- | --- | --- |
| 知识内容 — *Knowledge* | The stable claim itself, one unit per card | Agent on creation only; changing it later needs explicit user authorization |
| 条件与边界 — *Conditions* | When it holds and when it does not | Agent may append |
| 用户补充 — *User notes* | Your own additions | **Only you.** Agents are permanently forbidden here |

**Opinion — five sections:**

| Section | Contains |
| --- | --- |
| 观点 — *Opinion* | The evaluation / judgement itself |
| 论据与推理 — *Argument* | The reasoning and evidence behind it |
| 条件与反例 — *Conditions & counter-examples* | Where it applies and where it breaks |
| 待验证 — *To verify* | Open checks; the `validation` lifecycle lives in frontmatter |
| 用户补充 — *User notes* | **Only you.** |

**Note — four sections:** 整理正文 (tidied body), 提取结果 (extraction results), 存疑与待验证 (open
questions), 用户补充 (user notes).

The legacy v1 Knowledge sections 解释与依据 and 理解自检 are **not** current fixed sections; when an
older vault still carries them they are preserved verbatim as unknown/compatibility sections and
surfaced with an info, never rewritten.

Likewise, the legacy v1 Note sections 材料提炼 (material extraction), Agent 分析 (agent analysis)
and 产出知识卡 (produced knowledge cards) are **not** part of the current fixed Note template; where
an existing vault still carries them they are compatibility-mapped and kept byte-for-byte, never
zeroed out. (The full v1 Note procedure is out of scope here.)

This split is what keeps an agent honest: it may enrich the *reasoning* around a claim, but it cannot
silently redefine the claim, and it can never touch your words.

### Relationships

Two different kinds:

**Material relationships** connect a card to its evidence — four required elements: `source` +
`note` + `rel` (`support` / `against` / `context`) + `reason`.

**Argument relationships** connect cards to each other — `from` + `type` + `target` + `reason`:

| Type | Meaning |
| --- | --- |
| `derives` | target is derived from source |
| `supports` | source strengthens target |
| `limits` | source constrains target's applicability |
| `opposing` | the two contradict each other |

`opposing` is stored **once**, normalized by lexicographic ID order — there is no second mirrored
record to keep in sync. A `reason` that just restates the relationship name earns a `W2` warning:
links must carry real justification. Self-loops are rejected outright (`E5`, exit `2`, zero writes).

Add and remove links without touching card bodies:

```console
$ eg rel add k-a limits k-b --reason "A only holds under the batch sizes B assumes"
$ eg rel remove k-a limits k-b --reason "superseded by a direct measurement"
```

`rel remove` physically removes matching records — no tombstones. Removing something that isn't
there is an idempotent no-op (`W10`, exit `0`), so plans are safe to replay.

### The ChangePlan

The ChangePlan is Evergreen's contract with the outside world: **the only programmatic write
channel.** It has exactly eight top-level keys:

| Key | Purpose |
| --- | --- |
| `plan_version` | `2` for current plans. The supported set is `{1, 2}`; a `plan_version: 1` plan is still accepted for compatibility and flagged with a single `I1` migration info. |
| `verb` | Commit verb — `process` normally, `reprocess` when re-deriving a note |
| `domain` | Exactly one domain per plan |
| `reason` | Why this write happens |
| `requirement_ids` | Traceability tags, surfaced in the commit body |
| `convergence[]` | Per-candidate reasoning about overlap (see below) |
| `base` | file → `content_hash` from `eg context`, for optimistic concurrency |
| `ops[]` | The operations to perform |

The nine canonical operations on the main pipeline:

`add_source` · `write_note` · `create_knowledge` · `append_knowledge` · `create_opinion` ·
`append_opinion` · `add_material_rel` · `add_relation` · `add_open_question`

`create_card` and `append_card` are **compatibility aliases** for `create_knowledge` and
`append_knowledge`: they are normalized to the canonical names and raise an `I1` migration info, so
new plans should use the canonical ops. An unknown op is a hard error (`E5`, exit `2`, zero writes) —
Evergreen fails closed rather than guessing.

---

## Using Evergreen with a coding agent

Evergreen was designed for an agent to drive. `eg init` installs `SKILL.md` into the vault as the
authoritative operating procedure; point your agent at it.

### The pipeline

```text
step 0  agent fetches and cleans the body text        (no network I/O in the CLI)
   ①    eg capture          store the source, add to inbox
   ②    eg context          get knowledge + opinion candidates + base content hashes
   ③    semantic processing the only step where a model is involved
   ④    eg apply --plan     the single write channel
   ⑤    render the report   restate what was written, skipped, and why
```

If fetching fails or the body is empty, the correct behavior is to **stop** — report the failure and
write nothing. Placeholder or summarized text must never stand in for a real source.

### Knowledge or Opinion, and deciding whether one already exists

First classify the material. Stable definitions, compositions, steps, conditions, and data are
**Knowledge** (`create_knowledge`); evaluations, causal or predictive claims, comparisons, and
trade-offs are **Opinion** (`create_opinion`). When you cannot tell, **prefer Opinion** — it carries
the argument/counter-example/validation machinery a contested claim needs.

Before writing, the agent compares the new material against each candidate on three dimensions:

- **core knowledge** — same or different?
- **conditions** — same or different?
- **reuse purpose** — same or different?

The rule is *split rather than merge*: if any dimension differs — or if the agent cannot tell — make
a new entity and record why. Only all-three-`same` may reuse an existing one. Each comparison is
recorded in `convergence[]`, and Evergreen passes it through verbatim without re-judging it.

That verdict maps onto exactly seven outcomes:

| `relation` | Situation | Operations to emit |
| --- | --- | --- |
| `independent_new` | genuinely new | `create_knowledge` (or `create_opinion`) + `add_material_rel` |
| `same_semantics` | already covered | no new entity; just `add_material_rel` |
| `non_core_supplement` | adds a condition or argument | `append_knowledge` / `append_opinion` to a non-core section (never the core *Knowledge*/*Opinion* claim) + `add_material_rel` |
| `core_change` | the claim itself shifted | new `create_knowledge`/`create_opinion` + `add_relation` to the old one; **the old entity is neither edited nor retired** |
| `conflict_coexist` | contradicts an existing entity | both stay `active` + exactly one `opposing` link |
| `uncertain` | cannot be settled from this material | `add_open_question`; no entity |
| `deprecated` | retire an entity | **not available to agents** — user-initiated only |

### Faithful notes: coverage and omissions

`write_note` records the whole source faithfully. `blocks[]` reproduce every non-empty line in order
(agent annotations are interleaved as `role: agent` blocks); `omissions[]` lists — explicitly, even
when empty — the exact line ranges you deliberately dropped and why; and `extraction_coverage[]`
dispositions every source range as `outputs` (it became Knowledge/Opinion), `note_only` (kept in the
note), or `missing`. A `missing` disposition blocks the apply with an `E2`, so coverage is closed by
construction — it is not a place to hide the agent's own omissions, and the agent must not invent
content to fill it.

### What agents may never do

- Edit vault files with an editor, shell, or script — or run `git commit` / `reset` / `checkout`
  themselves. `eg apply` only.
- Write the core *知识内容* section of an existing Knowledge card, or the core *观点* section of an
  existing Opinion, or the *用户补充* section of anything.
- Set an Opinion's `validation` to `validated` / `rejected` — that lifecycle is user-initiated only;
  a plan that writes a non-`pending` validation is rejected (`E2`).
- Emit lifecycle operations (`deprecate`, `restore`, `delete`, `undelete`, `set_replaced_by`).
  Without `initiator: user` these are errors, not warnings — exit `2`, zero writes.
- Write two domains in one plan.
- Turn a multi-hop inference into Knowledge: with no material evidence of its own it would bypass the
  evidence requirement. Such conclusions belong in the note's analysis or in an Opinion.
- Fabricate a source, note, reason, or ID.

---

## Command reference

The CLI exposes 25 top-level commands（顶层命令共 25 个）. Run `eg <command> --help` for the
authoritative argument list — every help page documents its own exit codes.

**Setup**

| Command | Purpose |
| --- | --- |
| `eg init [--domain <d>]...` | Initialize a Git-backed vault |
| `eg config get` / `eg config set <k> <v>` | Read or update `default_domain` / `domains` |

**Main pipeline**

| Command | Purpose |
| --- | --- |
| `eg capture` | Store source material and add it to the inbox |
| `eg context` | Read-only: `draft_candidates` + `knowledge_candidates` + `opinion_candidates` + `base` content hashes |
| `eg apply --plan <file\|->` | Validate and apply a ChangePlan |
| `eg materialize` | User-initiated deterministic Note candidate materialization |
| `eg export --plain` | Export data Markdown without Evergreen-specific protocol syntax |
| `eg report --last` | Read-only replay of the last report-producing write (`eg apply` or `eg capture`) |

**Reading**

| Command | Purpose |
| --- | --- |
| `eg search <query>` | Ranked, paginated search. Defaults to `--kind knowledge`; `--kind opinion` or `--kind all` widen the set. |
| `eg card show <k-id>` | One Knowledge card: three sections, sources, both link directions |
| `eg rel <k-id>` | Argument relationships; `--replaced-by` switches to replacement pointers |
| `eg opinion search <q>` | Read-only opinion search: like `eg search` but fixed to opinions (`o-*`), with the same domain/tag/since/until/include-deleted/limit/offset flags (no `--kind`). Every hit carries `validation` (pending/validated/rejected, all returned — never implicitly filtered) plus `relation_summary` counts (`supports`/`limits`/`opposing`). Zero writes, zero commit. |
| `eg opinion show <o-id>` | Read-only single-opinion view: five sections, `validation`, sources, and the support/limit/opposing relation groups (each with forward and reverse segments; empty segments are shown explicitly). Accepts only `--include-deprecated`/`--limit`/`--offset` (never the search filter flags); dangling targets are flagged, deprecated peers are hidden by default. Zero writes, zero commit. `eg card show` on an `o-*` id exits `1` and points you here. |
| `eg opinion validate <o-id>` / `eg opinion reject <o-id>` | User-initiated validation lifecycle (landed). Rewrites exactly the target opinion's `validation` in a single-file atomic transaction and one `verb=process` commit. `validate` → `validated`; `validate --reopen` → `pending`; `reject` → `rejected`. `--user-request` is mandatory (else exit `2` with `E19`, zero writes, zero commit). Failure is staged, not uniformly "zero write": see the lifecycle notes below. |

**Opinion validation lifecycle (P-U, landed).** `eg opinion validate` / `eg opinion reject` are the
user-initiated write path for an opinion's `validation`. The action is recomputed **solely by the
state machine** from `(from, to)` — the CLI never self-reports it. There are exactly five legal edges:
`pending -> validated` (validate), `pending -> rejected` (reject), `validated -> rejected` (reject),
`validated -> pending` (reopen), and `rejected -> pending` (reopen). The three self-loops and the
reverse jump `rejected -> validated` are illegal — to undo a rejection you must first `reopen` back to
`pending` and then `validate` again (`rejected -> validated` is never a direct edge). `--reopen` is
accepted **only by `validate`** (`validated`/`rejected` → `pending`). Every write demands
`--user-request` (P-U); without it the command exits `2` with a single `E19`, zero writes, zero commit.
Success rewrites exactly one file (the target opinion) and produces one `verb=process` commit. Agents
(the automated P-A path) must not write `validation` on the user's behalf.

Failure is **staged** — "all failures are zero-write" is wrong:

- Bad arguments / missing-or-empty `--reason` (exit `1`); authorization failure / opinion-not-found /
  illegal validation edge (exit `2`); `run.lock` unavailable `E16`, or the `enterTxnCritical`
  transaction-safety recheck / recovery barrier failing closed `E15` (exit `5`) — this command does
  **not** run the plan `--strict` pre-check, so its `E15` arises only from the S1/S2 lock + recovery
  barrier, never from a strict-upgrade surface: the command performs **zero authoritative write, zero
  commit**.
- Pre-write dry-run block — S4 / command-level B3 (the target is concurrently rewritten after the
  in-lock re-read at S3 and before the S4 landing, tripping the `ExpectedHash` guard): the target state
  does **not** take effect; the command **neither overwrites nor restores** the concurrent bytes (the
  concurrent content is preserved verbatim); zero transaction, zero commit; exit `3`.
- Atomic commit fails **and** rollback succeeds (S6): exit `3`; the target keeps its pre-transaction
  image; zero commit.
- Git commit fails (S7 / B4): exit `4`; the validation target state **is already durable on disk and is
  kept** (no rollback), but there is **no Git commit**.
- Success: exactly the target opinion's single file + exactly one `verb=process` commit.

A `validated` opinion with zero effective **incoming** `supports` is surfaced by `W29`
(`opinion_unsupported_validated`) at reconcile/check time — a report-only signal that never edits
`validation` or synthesizes relations. Effective-incoming-supports rule: only `supports` edges **pointing
at this opinion** count (this opinion's own **outgoing** `supports` do **not**); the supporter must
**exist and be not-deleted** (a deleted supporter makes that support ineffective); a supporter that is
**deprecated but not deleted still counts**; duplicate `supports` edges from the same supporter are
**de-duplicated and counted once**.

**Editing and lifecycle** (all user-initiated)

| Command | Purpose |
| --- | --- |
| `eg rel add` / `eg rel remove` | Add or remove one argument relationship |
| `eg edit` | Replace an explicitly authorized card section |
| `eg deprecate` / `eg restore` | Retire or reactivate a card |
| `eg replaced-by` | Point a retired card at its replacement |
| `eg mark-reviewed` / `eg unreviewed` | Record or list review state |

**Risky operations**

| Command | Purpose |
| --- | --- |
| `eg proposal new` | Open a proposal for a high-risk operation (agents may do this) |
| `eg proposal list` / `eg proposal show` | Browse pending proposals and inspect one impact report |
| `eg proposal approve` / `eg proposal reject` | Human-only decision; `--user-request` is mandatory |
| `eg delete` / `eg undelete` | Apply or reverse a logical deletion |

**Maintenance**

| Command | Purpose |
| --- | --- |
| `eg check [--strict]` | Read-only structural check — exactly 7 checks, zero writes |
| `eg reconcile [--dry-run]` | Full-vault reconciliation R1–R7, with repairs |
| `eg index build\|rebuild\|status\|sync` | Manage the derived index |
| `eg bench` | Sample five read/index performance metrics, read-only |

### Global flags

| Flag | Meaning |
| --- | --- |
| `--json` | Structured envelope: `ok` / `data` / `warnings` / `exit_code` / `status`. Valid in either global or subcommand position, and still emitted when argument parsing fails. |
| `--vault <path>` | Vault root. Defaults to searching upward from the cwd for `evergreen.yml`. |
| `--user-request` | Declares that this invocation was explicitly initiated by a human. |
| `--help` / `--version` | Print and exit `0`. |

---

## Reading: sorting, pagination, and fallback

**Scoring.** `eg search` defaults to `--kind knowledge`, so only Knowledge cards appear in hits;
`--kind opinion` fixes the search to opinions and `--kind all` returns both — notes and sources never
appear. A term scores +3 in the title, +2 in tags, +1 in the body, counted at most once per term per
field.

**Total ordering.** Results sort by score desc → `updated_at` desc → `created_at` desc → `id` asc.
Relationship listings order by type (`opposing` → `limits` → `supports` → `derives`) → peer ID →
path → the full entry identity. Nothing depends on the storage backend, so any result is
reproducible by hand.

**Pagination.** `--limit` defaults to `50`; `--limit 0` disables truncation. Paging is applied
*after* sorting, so pages never overlap or drop entries, and `total` always reports the pre-pagination
count. Truncation emits exactly one `W25`. For `eg card show` and `eg rel`, `--limit` is a single
global cap across both directions — you will never get `2 × limit` entries back.

**Visibility.** Three orthogonal dimensions, never inferred from one another:

| Dimension | Default | Opt in with |
| --- | --- | --- |
| deprecated cards | visible, tagged `[失效]` | always visible; no hiding switch |
| deprecated *link peers* | hidden | `--include-deprecated` (`card show`, `rel`) |
| logically deleted | hidden from search | `--include-deleted`, tagged `[已删除]` |

**Graceful degradation.** When the index is stale (`W22`), missing (`W23`), or corrupt (`W24`),
reads fall back to scanning authoritative Markdown and report `Q5`. Markdown is authoritative, so the
answer is the same — only slower. Pagination truncation is reported as `W25`; `W21` remains 不分配.
Unparseable files are always reported as `Q1` and counted in `skipped_files`, never silently dropped.
Dangling references are shown as-is, flagged "target does not exist", with a `Q2` warning — and the
command still exits `0`, because a broken link is a fact about your vault, not a failure of the read.

---

## Safety model

### Two authorization paths

Evergreen distinguishes *who asked*. An agent-initiated write and a user-initiated write are
different code paths, and a plan cannot vouch for itself: the CLI requires `--user-request` on the
command line **and** `initiator: user` inside the plan before it will take the user path. Lifecycle
and destructive operations are unavailable on the agent path — attempting them exits `2` with zero
writes.

### Propose, don't execute

An agent can *ask*. `eg proposal new --type logical_delete --target <id>` is allowed without
`--user-request`; it computes the impact directly from Markdown and records it. Only a human can
approve:

```console
$ eg proposal list --status pending
$ eg proposal show p-20260915-001
$ eg proposal approve p-20260915-001 --confirm --user-request
```

Approval re-computes the impact and compares it against what the proposal recorded. If the premise
changed, the proposal is marked `superseded` and **not** executed. Missing `--confirm` exits `6`
with the authoritative Markdown completely untouched; missing `--user-request` exits `2`.

### Deletion is logical

```console
$ eg delete --target <id> --reason "duplicate" --proposal p-… --confirm --user-request
$ eg undelete --target <id> --reason "still needed"
```

`delete` writes `deleted_at` / `deleted_reason`. The file, its content, and every relationship
survive; deletion is a visibility change, not destruction. Deleting a card does **not** cascade to
links pointing at it. `undelete` removes exactly those two keys and leaves `status` untouched.

There is no physical delete, by design — that's what Git history and your filesystem are for.

### Crash-safe writes

M6 已启用 `run.lock`、事务日志、崩溃恢复、块级安全合并和退出码 `5`。Its reliability guarantees are:

- **`run.lock`** serializes writers within one machine. Contention is reported as `W28`; an
  unavailable lock exits `5` with `E16`.
- **`.index/txn/`** is the transaction journal（事务日志）recording before-images. Accepted
  multi-file write sets commit atomically.
- **Recovery（崩溃恢复）** happens automatically: the next write detects an interrupted transaction,
  restores it, and leaves a `W26` trace.
- **Block-level safety checks（块级安全合并）** refuse to merge a write that would damage a
  user-authored block (`W27`); an unpreservable user block is skipped rather than mangled.
- **`--strict`** promotes pre-write warnings (`W1`/`W2`/`W3`/`W4`/`W6`) to errors, aborting inside the
  lock with zero writes, exit `5` and `E15`.

> ⚠️ **存在未闭合事务时不可删 `.index/`.** The index itself is always rebuildable, but `.index/txn/`
> holds before-images that cannot be reconstructed from Markdown. If a transaction is blocked
> （事务阻断态）, run `eg check --json` to list every affected `txn_id` and `.index/txn/<txn_id>`
> path, back up that directory, then remove it — the next write will recover and record `W26`.

### Byte-preserving writes

Evergreen never round-trips your YAML through a serializer. It parses the original bytes, locates the
authorized span, and rewrites only that span. Unknown keys, comment style, quoting, and key order
are preserved — including content the current version doesn't understand. Writing YAML through a
serializer is blocked in CI by a dependency-direction gate, not just by convention.

---

## Exit codes and diagnostics

Every exit code is a contract. Scripts can branch on them.

| Code | Meaning | Wrote anything? |
| --- | --- | --- |
| `0` | Success (including zero hits, and idempotent no-ops) | maybe |
| `1` | Invalid command or arguments | **no** |
| `2` | Validation failed | **no** |
| `3` | Some requested writes were skipped; completed writes are kept and committed | partially |
| `4` | Git commit or index write failed; disk is left as-is, no destructive rollback | maybe |
| `5` | Strict pre-write check (`E15`) or lock unavailable (`E16`) | **no** |
| `6` | Valid, but explicit `--confirm` is required — Markdown completely unchanged | **no** |

Confirmation is evaluated in a fixed order: bad arguments `1` → validation failure `2` → only
confirmation missing `6`.

Diagnostics carry a `code`, `level`, `path`, `op_index`, `message`, and `target`, so a failure points
at the exact operation to fix. `code` and `exit_code` are independent concepts — a run can exit `0`
with warnings. Warnings and errors are split by `level` into `warnings[]` and `data.errors[]`.

Frequently seen codes: `E15` strict pre-write · `E16` lock · `W22`/`W23`/`W24` index
stale/missing/corrupt · `W25` pagination truncated · `W26` crash recovery · `W27` unsafe block merge ·
`W28` lock contention · `Q1` unparseable file · `Q2` dangling reference · `Q5` Markdown fallback.
(`W21` remains 不分配 — intentionally unassigned.)

---

## The derived index

The index is a SQLite/FTS5 database (via `modernc.org/sqlite`, pure Go — no CGO) under `.index/`. It
is a 可重建派生物 — a **pure accelerator**: not required by any command, never a prerequisite,
gitignored, and never distributed with the repository.

It carries `schema_version = 2` in `index_meta` and holds six tables — `index_meta`, `cards`,
`cards_fts`, `relations`, `files`, `skipped`. Knowledge and Opinion share `cards`/`cards_fts`,
distinguished by a `kind` column (`knowledge` / `opinion`); an opinion's `validation` rides the same
row. There is **no incremental schema migration**: when the on-disk `schema_version` does not match,
the whole index is discarded and rebuilt from Markdown.

```console
$ eg index build              # build it
$ eg index status --strict    # read-only health report
$ eg index sync               # incremental convergence after edits
$ eg index rebuild            # discard and rebuild from Markdown
```

Because Markdown is authoritative, you can always `rm -rf .index/` and rebuild — as long as no
transaction is open (see the warning above).

---

## Performance

```console
$ eg bench
状态：completed（exit_code=0，ok=true）
采样完成：2 张在册卡、2 个 id 采样点、20 个关键词；口径见下（命令面无参数可调）
  search_p95_ms          实测     21 ms   →  建议门槛     40 ms（= ceil(实测 × 1.5 / 10) × 10）
  card_show_p95_ms       实测     33 ms   →  建议门槛     50 ms
  rel_p95_ms             实测     28 ms   →  建议门槛     50 ms
  index_build_ms         实测     86 ms   →  建议门槛    130 ms
  index_incremental_ms   实测     62 ms   →  建议门槛    100 ms
```

Five metrics, read-only: `search_p95_ms`, `card_show_p95_ms`, `rel_p95_ms`, `index_build_ms`,
`index_incremental_ms`. Read metrics discard 3 warm-up rounds and then measure 50, taking the 48th
smallest value without interpolation; the two build metrics are the median of 3 runs on a **temporary
copy**, which is deleted afterwards. Your vault and index see zero writes and zero commits. The
keyword and ID sample sets are hard-coded, not random, so two runs on the same machine are
comparable.

`eg bench` deliberately reports measurements without passing judgment. Thresholds are derived and
enforced by the repository's own performance gate, so the numbers and the pass/fail criteria never
drift apart. It requires a healthy, fresh index and exits `1` with `E17` otherwise, rather than
sampling a degraded path and comparing against thresholds built for a different one.

---

## Development

```bash
make build        # native binary + cross-compiled dist/
make test         # core profile (the acceptance entry point)
make test-full    # every suite, once
make test-race    # -race subset
make lint         # gofmt + vet + write-path guard + dependency direction + public hygiene
```

`make release` produces a clean four-platform build plus `SHA256SUMS` and `PROVENANCE.txt`.
`make print-version` prints the single source of truth for the version string.

Package boundaries and design rationale are in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md);
contribution requirements are in [CONTRIBUTING.md](CONTRIBUTING.md).

Two conventions worth knowing before you send a patch:

1. **`make test` is the acceptance entry point, not `go test ./...`.** Every profile is derived from
   `tests/manifest/suites.yaml`, which also covers shell, contract, fuzz, mutation, and performance
   suites, and detects zero-execution or implicitly skipped tests. `make test-go` exists purely as an
   escape hatch for debugging the runner itself.
2. **Production packages contain no `_test.go` files.** Authoritative Go tests live under
   `tests/_staged/` and are materialized next to production code by the test runner.

---

## Platform support

Linux/amd64 is the original fully exercised platform. Linux/arm64,
Darwin/amd64, and Darwin/arm64 release binaries are cross-compiled. The Darwin
artifacts have not yet completed independent macOS runtime validation and must
be treated as **未经真机运行验证**.

| Target | Status |
| --- | --- |
| `linux/amd64` | Fully exercised by the acceptance suite |
| `linux/arm64` | Cross-compiled; no native run validation |
| `darwin/amd64` | Cross-compiled; **not validated on real hardware** |
| `darwin/arm64` | Cross-compiled; **not validated on real hardware** |

The local `flock`-based lock is intended for processes on one machine. Atomicity
is not guaranteed on a network drive or synchronized filesystem（网络盘或同步盘）— Dropbox, iCloud
Drive, and similar. Keep your vault on local disk and sync via Git.

---

## FAQ

**Does Evergreen call an LLM?**
No. It makes no model calls, no network requests, no telemetry, and never fetches a URL — including
the URL you pass to `eg capture`, which is recorded as metadata only. All semantics happen in your
agent, before `eg` is invoked.

**Can I edit my notes by hand?**
Yes — they're your files. The *User notes* section is reserved for you and no agent may write it.
The constraint applies to agents: automated writes must go through `eg apply` so they are validated
and committed atomically. If you hand-edit, run `eg check` afterwards to confirm the structure still
holds.

**What if I delete `.index/`?**
Fine, as long as no transaction is open — reads fall back to Markdown and report `W23`, and
`eg index build` recreates it. If a transaction *is* open, back up `.index/txn/` first.

**Is my vault locked into Evergreen?**
No. It's Markdown with YAML frontmatter in a normal Git repository. Delete the binary and every note
is still readable, greppable, and diffable.

**Why is everything in Chinese?**
Two separate reasons. The section names are a frozen part of the on-disk contract and are
compared byte-for-byte, so renaming them would break every existing vault. The CLI help text and the
human-readable report renderer are currently Chinese-only — English localization is not implemented.
The machine-facing surface is language-neutral: `--json`, diagnostic codes (`E15`, `W26`, `Q5`, …),
exit codes, IDs, and frontmatter keys are all stable and English/ASCII, so scripts and agents are
unaffected.

**How do I retire a claim that turned out to be wrong?**
`eg deprecate` it, then `eg replaced-by` to point at its successor. The card stays visible and
searchable with a `[失效]` marker — Evergreen keeps superseded knowledge legible instead of erasing
it, so the reasoning trail survives.

**Multiple machines?**
Sync the vault with Git like any other repository. Don't put it on a network or sync drive and write
from two machines at once — the lock is local-only.

---

## Project information

- **Version:** `0.8.0-m8` · **Module:** `github.com/ikaqiu-Lemon/EverGreen`
- **Security:** private vulnerability reporting in [SECURITY.md](SECURITY.md)
- **Privacy:** local-only data-handling model in [PRIVACY.md](PRIVACY.md)
- **Conduct:** [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- **Changelog:** [CHANGELOG.md](CHANGELOG.md)

## License

Licensed under the [Apache License 2.0](LICENSE).
