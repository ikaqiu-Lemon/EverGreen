# Architecture

Evergreen is a local-first CLI. Markdown and frontmatter in a user-selected
vault are the authoritative data. SQLite is used only as a rebuildable local
index.

## Package boundaries

| Package | Responsibility |
| --- | --- |
| `cmd/eg` | Process entry point and exit-code handoff |
| `internal/cli` | Commands, arguments, rendering, and orchestration |
| `internal/model` | Shared data types and closed value sets |
| `internal/mdfile` | Byte-preserving Markdown and frontmatter parsing |
| `internal/store` | Guarded reads and writes to authoritative files |
| `internal/plan` | ChangePlan validation, authorization, and execution |
| `internal/query` | Search, card views, relationships, and pagination |
| `internal/index` | Rebuildable SQLite/FTS5 index |
| `internal/txn` | Local lock, journal, recovery, and atomic file sets |
| `internal/reconcile` | Repository-wide consistency checks and repair plans |
| `internal/proposal` | High-risk operation proposal state machine |
| `internal/report` | Stable human and JSON result models |
| `skill` | Agent operating contract embedded in the binary |

The dependency-direction checks in `make lint` enforce the most important
boundaries.

## Write safety

Evergreen does not deserialize and rewrite complete YAML documents. It parses
the original bytes, identifies bounded spans, and rewrites only the authorized
span. Unknown keys, sections, formatting, and user-authored content outside
that span are preserved.

Mutating commands use a vault-local lock and transaction journal. Accepted
multi-file write sets are committed atomically. A later write detects and
recovers interrupted transactions before making new changes.

## Derived index

The `.index/` directory contains the SQLite index and transaction runtime
state. Markdown remains authoritative. Index corruption or staleness causes
read commands to fall back to scanning Markdown.

Do not delete `.index/` while an unclosed transaction exists: `.index/txn/`
contains recovery data that cannot be reconstructed from the index.

## Tests

Production packages intentionally contain no `_test.go` files. Authoritative Go
tests live under `tests/_staged/` and are materialized beside production code by
the manifest-driven test runner. Shell, contract, fuzz, mutation, and
performance suites are registered in `tests/manifest/suites.yaml`.
