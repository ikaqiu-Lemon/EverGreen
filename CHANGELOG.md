# Changelog

All notable changes to Evergreen will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and published versions follow [Semantic Versioning](https://semver.org/).

## Unreleased

### Added

- Public project documentation, contribution guidance, security policy, and
  automated CI and release workflows.

### Changed

- Prepared the existing `0.6.0-m6` implementation for a public source release.

### Fixed

- Retried a transient Darwin `openat` failure when concurrent writers create
  the vault lock file for the first time.

## 0.6.0-m6

- Added atomic multi-file transactions, a local write lock, crash recovery,
  strict pre-write validation, and exit code `5`.
- Added a rebuildable SQLite/FTS5 index with scan fallback.
- Added reconciliation, lifecycle, proposal, review, and relationship commands.
