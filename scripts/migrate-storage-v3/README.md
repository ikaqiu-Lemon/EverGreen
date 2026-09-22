# Storage v3 one-time migration

This directory contains the manifest-driven migration executable used for the
Storage v3 rollout. It is not an `eg` subcommand.

```bash
go run ./scripts/migrate-storage-v3 \
  --vault vault-copy \
  --manifest migration-manifest.json
```

The default mode is a read-only dry-run. It prints canonical JSON containing:

- every planned file with before/after hashes, sizes, action, and a complete
  whole-file unified diff;
- every persistent candidate-to-output mapping;
- Knowledge, Opinion, material-relation, argument-relation, coverage, and
  tombstone counts.

Apply only after reviewing that report, and only against a disposable full
copy during T008:

```bash
go run ./scripts/migrate-storage-v3 \
  --vault vault-copy \
  --manifest migration-manifest.json \
  --apply
```

Apply acquires the standard `run.lock`, runs journal recovery first, rebuilds
the plan while holding the lock, and commits the complete write-set through
journal v1. The tool does not create a Git commit. An identical rerun is a
no-op and does not allocate a transaction.

After a successful apply, review the reported paths and commit exactly that
write-set with the Vault's normal Git workflow. Do not use a second migration
as rollback: restore the pre-migration full-copy snapshot instead. A failed
journal commit is recovered to its complete preimage by the existing
transaction implementation.

## Manifest

All paths inside the manifest are canonical slash-separated paths relative to
the Vault root. Input and target hashes use `sha256:<hex>`.

- `note` identifies the current Note, the reviewed v2 Note template, the
  historical review plan, ordered source refs, and sections copied byte for
  byte from the current Note.
- `candidates` maps stable candidate keys to explicit output IDs, canonical
  target paths, reviewed seed artifacts, and source refs.
- `coverage` is the final no-gap matrix. Only `outputs` and `note_only` are
  accepted; `missing` and `unresolved` fail closed.
- `tombstones` records wrong-type legacy artifacts that receive
  `deleted_at`/`deleted_reason` and `replaced_by`.
- `expected` fixes all audit counts before execution.

The tool parses review blocks and artifacts through the product parsers,
renders candidate and coverage protocols through the product writers, proves
the resulting Note is an idempotent `MaterializeCandidates` projection, and
copies only validated deterministic bytes. It performs no network or model
calls.
